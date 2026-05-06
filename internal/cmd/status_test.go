package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gotest.tools/v3/assert"

	"github.com/infrahq/infra/api"
	"github.com/infrahq/infra/uid"
)

func TestStatusCmd_ActiveSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	userID := uid.New()
	expiry := time.Now().Add(30 * 24 * time.Hour)
	inactivityDeadline := time.Now().Add(48 * time.Hour)

	handler := func(resp http.ResponseWriter, req *http.Request) {
		switch {
		case requestMatches(req, http.MethodGet, "/api/users/self"):
			resp.WriteHeader(http.StatusOK)
			err := json.NewEncoder(resp).Encode(&api.User{
				ID:            userID,
				Name:          "alice@example.com",
				ProviderNames: []string{"Google"},
			})
			assert.Check(t, err)
		case requestMatches(req, http.MethodGet, "/api/access-keys"):
			resp.WriteHeader(http.StatusOK)
			err := json.NewEncoder(resp).Encode(&api.ListResponse[api.AccessKey]{
				Count: 1,
				Items: []api.AccessKey{
					{
						ID:                uid.New(),
						IssuedForID:       userID,
						InactivityTimeout: api.Time(inactivityDeadline),
					},
				},
			})
			assert.Check(t, err)
		case requestMatches(req, http.MethodGet, "/api/organizations/self"):
			resp.WriteHeader(http.StatusOK)
			err := json.NewEncoder(resp).Encode(&api.Organization{
				Name: "Default",
			})
			assert.Check(t, err)
		default:
			resp.WriteHeader(http.StatusBadRequest)
		}
	}

	srv := httptest.NewTLSServer(http.HandlerFunc(handler))
	t.Cleanup(srv.Close)

	cfg := newTestClientConfig(srv, api.User{ID: userID, Name: "alice@example.com"})
	// Override expiry to a known future time
	cfg.Hosts[0].Expires = api.Time(expiry)
	err := writeConfig(&cfg)
	assert.NilError(t, err)

	ctx, bufs := PatchCLI(t.Context())
	err = Run(ctx, "status")
	assert.NilError(t, err)

	output := bufs.Stdout.String()
	assert.Assert(t, strings.Contains(output, "alice@example.com"), "expected user in output, got: %s", output)
	assert.Assert(t, strings.Contains(output, "Default"), "expected organization in output, got: %s", output)
	assert.Assert(t, strings.Contains(output, "Google"), "expected provider in output, got: %s", output)
	assert.Assert(t, strings.Contains(output, "✓ Active"), "expected active status, got: %s", output)
	assert.Assert(t, strings.Contains(output, "remaining"), "expected expiry info, got: %s", output)
}

func TestStatusCmd_ExpiredSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	handler := func(resp http.ResponseWriter, req *http.Request) {
		if requestMatches(req, http.MethodGet, "/api/users/self") {
			resp.WriteHeader(http.StatusUnauthorized)
			err := json.NewEncoder(resp).Encode(&api.Error{Code: 401, Message: "access key has expired"})
			assert.Check(t, err)
			return
		}
		resp.WriteHeader(http.StatusBadRequest)
	}

	srv := httptest.NewTLSServer(http.HandlerFunc(handler))
	t.Cleanup(srv.Close)

	cfg := newTestClientConfig(srv, api.User{})
	err := writeConfig(&cfg)
	assert.NilError(t, err)

	ctx, bufs := PatchCLI(t.Context())
	err = Run(ctx, "status")
	assert.NilError(t, err) // expired shows info, not an error

	output := bufs.Stdout.String()
	assert.Assert(t, strings.Contains(output, "✗ Expired"), "expected expired status, got: %s", output)
}

func TestStatusCmd_SessionExpiringSoon(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	userID := uid.New()
	expiry := time.Now().Add(1 * time.Hour) // expires within 24h

	handler := func(resp http.ResponseWriter, req *http.Request) {
		switch {
		case requestMatches(req, http.MethodGet, "/api/users/self"):
			resp.WriteHeader(http.StatusOK)
			err := json.NewEncoder(resp).Encode(&api.User{
				ID:   userID,
				Name: "bob@example.com",
			})
			assert.Check(t, err)
		case requestMatches(req, http.MethodGet, "/api/access-keys"):
			resp.WriteHeader(http.StatusOK)
			err := json.NewEncoder(resp).Encode(&api.ListResponse[api.AccessKey]{
				Count: 0,
				Items: []api.AccessKey{},
			})
			assert.Check(t, err)
		case requestMatches(req, http.MethodGet, "/api/organizations/self"):
			resp.WriteHeader(http.StatusOK)
			err := json.NewEncoder(resp).Encode(&api.Organization{
				Name: "Default",
			})
			assert.Check(t, err)
		default:
			resp.WriteHeader(http.StatusBadRequest)
		}
	}

	srv := httptest.NewTLSServer(http.HandlerFunc(handler))
	t.Cleanup(srv.Close)

	cfg := newTestClientConfig(srv, api.User{ID: userID, Name: "bob@example.com"})
	cfg.Hosts[0].Expires = api.Time(expiry)
	err := writeConfig(&cfg)
	assert.NilError(t, err)

	ctx, bufs := PatchCLI(t.Context())
	err = Run(ctx, "status")
	assert.NilError(t, err)

	output := bufs.Stdout.String()
	assert.Assert(t, strings.Contains(output, "⚠"), "expected warning for expiring soon, got: %s", output)
	assert.Assert(t, strings.Contains(output, "expires soon"), "expected 'expires soon' warning, got: %s", output)
}

func TestStatusCmd_DoesNotGuessInactivityDeadline(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	userID := uid.New()
	expiry := time.Now().Add(30 * 24 * time.Hour)
	providerID := uid.New()

	handler := func(resp http.ResponseWriter, req *http.Request) {
		switch {
		case requestMatches(req, http.MethodGet, "/api/users/self"):
			resp.WriteHeader(http.StatusOK)
			err := json.NewEncoder(resp).Encode(&api.User{
				ID:            userID,
				Name:          "alice@example.com",
				ProviderNames: []string{"Okta"},
			})
			assert.Check(t, err)
		case requestMatches(req, http.MethodGet, "/api/access-keys"):
			resp.WriteHeader(http.StatusOK)
			err := json.NewEncoder(resp).Encode(&api.ListResponse[api.AccessKey]{
				Count: 2,
				Items: []api.AccessKey{
					{
						ID:                uid.New(),
						IssuedForID:       userID,
						ProviderID:        providerID,
						Expires:           api.Time(expiry),
						InactivityTimeout: api.Time(time.Now().Add(4 * time.Hour)),
					},
					{
						ID:                uid.New(),
						IssuedForID:       userID,
						ProviderID:        providerID,
						Expires:           api.Time(expiry),
						InactivityTimeout: api.Time(time.Now().Add(12 * time.Hour)),
					},
				},
			})
			assert.Check(t, err)
		case requestMatches(req, http.MethodGet, "/api/organizations/self"):
			resp.WriteHeader(http.StatusOK)
			err := json.NewEncoder(resp).Encode(&api.Organization{
				Name: "Default",
			})
			assert.Check(t, err)
		default:
			resp.WriteHeader(http.StatusBadRequest)
		}
	}

	srv := httptest.NewTLSServer(http.HandlerFunc(handler))
	t.Cleanup(srv.Close)

	cfg := newTestClientConfig(srv, api.User{ID: userID, Name: "alice@example.com"})
	cfg.Hosts[0].Expires = api.Time(expiry)
	cfg.Hosts[0].ProviderID = providerID
	err := writeConfig(&cfg)
	assert.NilError(t, err)

	ctx, bufs := PatchCLI(t.Context())
	err = Run(ctx, "status")
	assert.NilError(t, err)

	output := bufs.Stdout.String()
	assert.Assert(t, !strings.Contains(output, "Inactivity limit:"), "expected inactivity limit to be omitted when ambiguous, got: %s", output)
}
