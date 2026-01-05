package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/golden"

	"github.com/infrahq/infra/internal/cmd/types"
)

// TestACMEHostPolicy tests the ACME HostPolicy function to ensure it properly
// validates certificate requests against the allowed hosts whitelist.
// This is critical for preventing DoS attacks on Let's Encrypt rate limits.
func TestACMEHostPolicy(t *testing.T) {
	testCases := []struct {
		name         string
		allowedHosts []string
		requestHost  string
		expectError  bool
		errorMsg     string
	}{
		{
			name:         "no allowed hosts configured - reject all",
			allowedHosts: nil,
			requestHost:  "example.com",
			expectError:  true,
			errorMsg:     "no allowed hosts configured",
		},
		{
			name:         "empty allowed hosts - reject all",
			allowedHosts: []string{},
			requestHost:  "example.com",
			expectError:  true,
			errorMsg:     "no allowed hosts configured",
		},
		{
			name:         "exact match allowed",
			allowedHosts: []string{"example.com"},
			requestHost:  "example.com",
			expectError:  false,
		},
		{
			name:         "exact match case insensitive",
			allowedHosts: []string{"Example.COM"},
			requestHost:  "example.com",
			expectError:  false,
		},
		{
			name:         "host not in allowed list",
			allowedHosts: []string{"example.com"},
			requestHost:  "attacker.com",
			expectError:  true,
			errorMsg:     "not in the allowed hosts list",
		},
		{
			name:         "wildcard match - direct subdomain",
			allowedHosts: []string{"*.example.com"},
			requestHost:  "api.example.com",
			expectError:  false,
		},
		{
			name:         "wildcard match - case insensitive",
			allowedHosts: []string{"*.Example.COM"},
			requestHost:  "API.example.com",
			expectError:  false,
		},
		{
			name:         "wildcard does not match base domain",
			allowedHosts: []string{"*.example.com"},
			requestHost:  "example.com",
			expectError:  true,
			errorMsg:     "not in the allowed hosts list",
		},
		{
			name:         "wildcard does not match nested subdomain",
			allowedHosts: []string{"*.example.com"},
			requestHost:  "deep.api.example.com",
			expectError:  true,
			errorMsg:     "not in the allowed hosts list",
		},
		{
			name:         "multiple allowed hosts - match first",
			allowedHosts: []string{"example.com", "example.org"},
			requestHost:  "example.com",
			expectError:  false,
		},
		{
			name:         "multiple allowed hosts - match second",
			allowedHosts: []string{"example.com", "example.org"},
			requestHost:  "example.org",
			expectError:  false,
		},
		{
			name:         "host with port - port stripped",
			allowedHosts: []string{"example.com"},
			requestHost:  "example.com:443",
			expectError:  false,
		},
		{
			name:         "invalid hostname format rejected",
			allowedHosts: []string{"example.com"},
			requestHost:  "not a valid hostname!",
			expectError:  true,
			errorMsg:     "invalid hostname format",
		},
		{
			name:         "IP address allowed when in list",
			allowedHosts: []string{"192.168.1.1"},
			requestHost:  "192.168.1.1",
			expectError:  false,
		},
		{
			name:         "empty host rejected",
			allowedHosts: []string{"example.com"},
			requestHost:  "",
			expectError:  true,
			errorMsg:     "invalid hostname format",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			policy := hostPolicy(tc.allowedHosts)
			err := policy(context.Background(), tc.requestHost)

			if tc.expectError {
				assert.Assert(t, err != nil, "expected error but got nil")
				if tc.errorMsg != "" {
					assert.ErrorContains(t, err, tc.errorMsg)
				}
			} else {
				assert.NilError(t, err)
			}
		})
	}
}

// TestIsValidHostname tests the hostname validation function
func TestIsValidHostname(t *testing.T) {
	testCases := []struct {
		name     string
		hostname string
		valid    bool
	}{
		{"valid domain", "example.com", true},
		{"valid subdomain", "api.example.com", true},
		{"valid with numbers", "api123.example.com", true},
		{"valid with hyphens", "my-api.example.com", true},
		{"valid IP v4", "192.168.1.1", true},
		{"valid IP v6", "::1", true},
		{"empty string", "", false},
		{"too long", string(make([]byte, 254)), false},
		{"invalid characters", "exam ple.com", false},
		{"starts with hyphen", "-example.com", false},
		{"ends with hyphen", "example-.com", false},
		{"single char TLD", "example.c", false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := isValidHostname(tc.hostname)
			assert.Equal(t, result, tc.valid)
		})
	}
}

func TestTLSConfigFromOptions(t *testing.T) {
	ca := golden.Get(t, "pki/ca.crt")
	t.Run("user provided certificate", func(t *testing.T) {
		opts := TLSOptions{
			CA:          types.StringOrFile(ca),
			Certificate: types.StringOrFile(golden.Get(t, "pki/localhost.crt")),
			PrivateKey:  types.StringOrFile(golden.Get(t, "pki/localhost.key")),
		}
		config, err := tlsConfigFromOptions(opts)
		assert.NilError(t, err)

		srv := httptest.NewUnstartedServer(noopHandler)
		srv.TLS = config
		srv.StartTLS()
		t.Cleanup(srv.Close)

		roots := x509.NewCertPool()
		roots.AppendCertsFromPEM(ca)
		client := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
			},
		}

		// nolint:noctx
		resp, err := client.Get(srv.URL)
		assert.NilError(t, err)
		assert.Equal(t, resp.StatusCode, http.StatusOK)
	})

	t.Run("generate TLS cert from CA", func(t *testing.T) {
		if testing.Short() {
			t.Skip("too slow for short run")
		}
		opts := TLSOptions{
			CA:           types.StringOrFile(ca),
			CAPrivateKey: types.StringOrFile(golden.Get(t, "pki/ca.key")),
		}
		config, err := tlsConfigFromOptions(opts)
		assert.NilError(t, err)

		l, err := net.Listen("tcp", "127.0.0.1:0")
		assert.NilError(t, err)

		l = tls.NewListener(l, config)
		// nolint:gosec
		srv := http.Server{Handler: noopHandler}

		go func() {
			// nolint:errorlint
			if err := srv.Serve(l); err != http.ErrServerClosed {
				t.Log(err)
			}
		}()
		t.Cleanup(func() { _ = srv.Close() })

		roots := x509.NewCertPool()
		roots.AppendCertsFromPEM(ca)
		client := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
			},
		}

		// nolint:noctx
		resp, err := client.Get("https://" + l.Addr().String())
		assert.NilError(t, err)
		assert.Equal(t, resp.StatusCode, http.StatusOK)
	})
}

var noopHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
})
