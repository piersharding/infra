package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"regexp"
	"strings"
	"sync"

	"golang.org/x/crypto/acme/autocert"

	"github.com/infrahq/infra/internal/certs"
	"github.com/infrahq/infra/internal/logging"
)

// MapCache is a simple in-memory caching mechanism, it is not thread safe
type MapCache map[string][]byte

func (m MapCache) Get(_ context.Context, name string) ([]byte, error) {
	data, cached := m[name]
	if !cached {
		return nil, autocert.ErrCacheMiss
	}
	return data, nil
}

func (m MapCache) Put(_ context.Context, name string, data []byte) error {
	m[name] = data
	return nil
}

func (m MapCache) Delete(_ context.Context, name string) error {
	delete(m, name)
	return nil
}

// ACMEOptions contains configuration for ACME certificate management
type ACMEOptions struct {
	// AllowedHosts is a list of hostnames that are allowed to request certificates.
	// Supports exact matches and wildcard patterns (e.g., "*.example.com").
	// If empty, all hosts are rejected for security.
	AllowedHosts []string

	// Email is the contact email for Let's Encrypt notifications
	Email string

	// CacheDir is the directory to cache certificates
	CacheDir string
}

// hostPolicy creates a HostPolicy function that validates certificate requests
// against a whitelist of allowed hostnames. This prevents DoS attacks where
// attackers could exhaust the certificate rate limit by requesting certificates
// for arbitrary domains.
func hostPolicy(allowedHosts []string) autocert.HostPolicy {
	return func(ctx context.Context, host string) error {
		// If no allowed hosts are configured, reject all requests for security
		if len(allowedHosts) == 0 {
			secureLogger := logging.SecureLogger(logging.L)
			secureLogger.SecureWarn("ACME certificate request rejected: no allowed hosts configured", map[string]interface{}{
				"requested_host": host,
			})
			return fmt.Errorf("ACME host policy: no allowed hosts configured, rejecting %q", host)
		}

		// Normalize the host (lowercase, remove port if present)
		host = strings.ToLower(host)
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}

		// Validate the hostname format
		if !isValidHostname(host) {
			secureLogger := logging.SecureLogger(logging.L)
			secureLogger.SecureWarn("ACME certificate request rejected: invalid hostname format", map[string]interface{}{
				"requested_host": host,
			})
			return fmt.Errorf("ACME host policy: invalid hostname format %q", host)
		}

		for _, allowed := range allowedHosts {
			allowed = strings.ToLower(allowed)

			// Check for exact match
			if allowed == host {
				return nil
			}

			// Check for wildcard match (e.g., "*.example.com")
			if strings.HasPrefix(allowed, "*.") {
				suffix := allowed[1:] // Remove the "*" but keep the "."
				if strings.HasSuffix(host, suffix) && strings.Count(host, ".") == strings.Count(suffix, ".") {
					// Ensure it's a direct subdomain match, not a deeper nested subdomain
					return nil
				}
			}
		}

		secureLogger := logging.SecureLogger(logging.L)
		secureLogger.SecureWarn("ACME certificate request rejected: host not in allowed list", map[string]interface{}{
			"requested_host": host,
			"allowed_hosts":  allowedHosts,
		})
		return fmt.Errorf("ACME host policy: host %q is not in the allowed hosts list", host)
	}
}

// isValidHostname validates that a hostname is well-formed
var hostnameRegex = regexp.MustCompile(`^([a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?\.)*[a-zA-Z]{2,}$`)

func isValidHostname(host string) bool {
	if len(host) == 0 || len(host) > 253 {
		return false
	}
	// Allow IP addresses
	if net.ParseIP(host) != nil {
		return true
	}
	return hostnameRegex.MatchString(host)
}

func tlsConfigFromOptions(opts TLSOptions) (*tls.Config, error) {
	if opts.ACME {
		// Extract allowed hosts from the ACME configuration
		allowedHosts := opts.ACMEAllowedHosts

		// If no allowed hosts are explicitly configured, we should fail secure
		// This prevents misconfiguration from exposing the server to DoS attacks
		if len(allowedHosts) == 0 {
			logging.L.Warn().Msg("ACME enabled but no allowed hosts configured - this is a security risk. " +
				"Configure tls.acme-allowed-hosts to specify which domains can request certificates.")
		}

		manager := &autocert.Manager{
			Prompt: autocert.AcceptTOS,
			// HostPolicy is critical for security - it prevents attackers from
			// exhausting Let's Encrypt rate limits by requesting certificates
			// for arbitrary domains they control.
			// See https://github.com/infrahq/infra/issues/2484
			HostPolicy: hostPolicy(allowedHosts),
		}

		// Set email for Let's Encrypt notifications if provided
		if opts.ACMEEmail != "" {
			manager.Email = opts.ACMEEmail
		}

		// Set cache directory if provided
		if opts.ACMECacheDir != "" {
			manager.Cache = autocert.DirCache(opts.ACMECacheDir)
		}

		tlsConfig := manager.TLSConfig()
		tlsConfig.MinVersion = tls.VersionTLS12
		return tlsConfig, nil
	}

	roots, err := x509.SystemCertPool()
	if err != nil {
		logging.L.Err(err).Msgf("failed to load TLS roots from system")
		roots = x509.NewCertPool()
	}

	if opts.CA != "" {
		raw := pemDecode([]byte(opts.CA))
		if len(raw) == 0 {
			secureLogger := logging.SecureLogger(logging.L)
			secureLogger.SecureError(fmt.Sprintf("could not read CA %q", opts.CA), nil)
		} else {
			secureLogger := logging.SecureLogger(logging.L)
			secureLogger.SecureInfo(fmt.Sprintf("TLS CA SHA256 fingerprint: %s", certs.Fingerprint(raw)))

			if !roots.AppendCertsFromPEM([]byte(opts.CA)) {
				secureLogger := logging.SecureLogger(logging.L)
				secureLogger.SecureWarn("failed to load TLS CA, invalid PEM", nil)
			}
		}
	}

	cfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		// enable HTTP/2
		NextProtos: []string{"h2", "http/1.1"},
		// enabled optional mTLS
		ClientAuth: tls.VerifyClientCertIfGiven,
		ClientCAs:  roots,
	}

	if opts.Certificate != "" && opts.PrivateKey != "" {
		cert, err := tls.X509KeyPair([]byte(opts.Certificate), []byte(opts.PrivateKey))
		if err != nil {
			return nil, fmt.Errorf("failed to load TLS key pair: %w", err)
		}

		cfg.Certificates = []tls.Certificate{cert}
		return cfg, nil
	}

	if opts.CA == "" || opts.CAPrivateKey == "" {
		return nil, fmt.Errorf("either a TLS certificate and key or a TLS CA and key is required")
	}

	ca := keyPair{cert: []byte(opts.CA), key: []byte(opts.CAPrivateKey)}
	certCache := make(MapCache)

	cfg.GetCertificate = getCertificate(certCache, ca)
	return cfg, nil
}

type keyPair struct {
	cert []byte
	key  []byte
}

func getCertificate(cache autocert.Cache, ca keyPair) func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	var lock sync.RWMutex

	getKeyPair := func(serverName string) (cert, key []byte) {
		certBytes, _ := cache.Get(context.TODO(), serverName+".crt")
		keyBytes, _ := cache.Get(context.TODO(), serverName+".key")
		if certBytes == nil || keyBytes == nil {
			secureLogger := logging.SecureLogger(logging.L)
			secureLogger.SecureInfo(fmt.Sprintf("no cached TLS cert for %v", serverName))
		}
		return certBytes, keyBytes
	}

	return func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		serverName := hello.ServerName

		if serverName == "" {
			var err error
			serverName, _, err = net.SplitHostPort(hello.Conn.LocalAddr().String())
			if err != nil {
				return nil, err
			}
		}

		lock.RLock()
		certBytes, keyBytes := getKeyPair(serverName)
		lock.RUnlock()
		if certBytes != nil && keyBytes != nil {
			return tlsCertFromKeyPair(certBytes, keyBytes)
		}

		lock.Lock()
		// must check again after write lock is acquired
		certBytes, keyBytes = getKeyPair(serverName)
		defer lock.Unlock()
		if certBytes != nil && keyBytes != nil {
			return tlsCertFromKeyPair(certBytes, keyBytes)
		}

		// if either cert or key is missing, create it
		caTLSCert, err := tls.X509KeyPair(ca.cert, ca.key)
		if err != nil {
			return nil, err
		}

		caCert, err := x509.ParseCertificate(caTLSCert.Certificate[0])
		if err != nil {
			return nil, err
		}

		hosts := []string{"127.0.0.1", "::1", serverName}
		certBytes, keyBytes, err = certs.GenerateCertificate(hosts, caCert, caTLSCert.PrivateKey)
		if err != nil {
			return nil, err
		}

		// append the CA PEM to the cert PEM so that the full chain is available
		// to clients. Not strictly required by TLS, but we do this so that the
		// CLI can prompt the user to trust the CA.
		certBytes = append(certBytes, ca.cert...)

		if err := cache.Put(context.TODO(), serverName+".crt", certBytes); err != nil {
			return nil, err
		}

		if err := cache.Put(context.TODO(), serverName+".key", keyBytes); err != nil {
			return nil, err
		}

		logging.L.Info().
			Str("Server name", serverName).
			Str("SHA256 fingerprint", certs.Fingerprint(pemDecode(certBytes))).
			Msg("new server certificate")

		keypair, err := tls.X509KeyPair(certBytes, keyBytes)
		if err != nil {
			return nil, err
		}

		return &keypair, nil
	}
}

func pemDecode(raw []byte) []byte {
	block, _ := pem.Decode(raw)
	if block != nil {
		return block.Bytes
	}
	return []byte{}
}

func tlsCertFromKeyPair(cert, key []byte) (*tls.Certificate, error) {
	keypair, err := tls.X509KeyPair(cert, key)
	if err != nil {
		return nil, err
	}
	return &keypair, nil
}
