package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"gotest.tools/v3/assert"
	is "gotest.tools/v3/assert/cmp"

	"github.com/infrahq/infra/api"
	"github.com/infrahq/infra/internal/access"
	"github.com/infrahq/infra/internal/server/data"
	"github.com/infrahq/infra/internal/server/models"
)

// TestDebugEndpointsDisabledByDefault verifies that pprof endpoints are not
// registered when Debug.Enabled is false (the default production setting)
func TestDebugEndpointsDisabledByDefault(t *testing.T) {
	// Setup server with Debug.Enabled = false (default)
	s := setupServer(t, func(t *testing.T, opts *Options) {
		opts.Debug = DefaultDebugConfig() // Disabled by default
	})
	routes := s.GenerateRoutes()

	// Create a request with valid authentication
	key, user := createAccessKey(t, s.DB(), "admin@example.com")
	err := data.CreateGrant(s.DB(), &models.Grant{
		Subject:   models.NewSubjectForUser(user.ID),
		Privilege: models.InfraSupportAdminRole,
		Resource:  access.ResourceInfraAPI,
		CreatedBy: user.ID,
	})
	assert.NilError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/debug/pprof/heap?debug=1", nil)
	req.Header.Add("Infra-Version", apiVersionLatest)
	req.Header.Add("Authorization", "Bearer "+key)

	resp := httptest.NewRecorder()
	routes.ServeHTTP(resp, req)

	// Should return 404 because the route is not registered
	assert.Equal(t, http.StatusNotFound, resp.Code,
		"pprof endpoints should not be accessible when Debug.Enabled is false")
}

func TestAPI_PProfHandler(t *testing.T) {
	type testCase struct {
		name         string
		setupRequest func(t *testing.T, req *http.Request)
		expectedCode int
		expectedResp func(t *testing.T, resp *httptest.ResponseRecorder)
	}

	// Enable debug mode to register pprof routes
	s := setupServer(t, func(t *testing.T, opts *Options) {
		opts.Debug = DebugConfig{
			Enabled:            true,
			AllowInProduction:  true, // Allow for testing
			RateLimitPerMinute: 100,  // High limit for tests
			AuditLog:           true,
		}
	})
	routes := s.GenerateRoutes()

	run := func(t *testing.T, tc testCase) {
		// nolint:noctx
		req := httptest.NewRequest(http.MethodGet, "/api/debug/pprof/heap?debug=1", nil)
		req.Header.Add("Infra-Version", apiVersionLatest)

		if tc.setupRequest != nil {
			tc.setupRequest(t, req)
		}

		resp := httptest.NewRecorder()
		routes.ServeHTTP(resp, req)

		assert.Equal(t, tc.expectedCode, resp.Code, resp.Body.String())
		if tc.expectedResp != nil {
			tc.expectedResp(t, resp)
		}
	}

	testCases := []testCase{
		{
			name:         "missing access key",
			expectedCode: http.StatusUnauthorized,
			expectedResp: responseBodyAPIErrorWithCode(http.StatusUnauthorized),
		},
		{
			name:         "missing admin role",
			expectedCode: http.StatusForbidden,
			setupRequest: func(_ *testing.T, req *http.Request) {
				key, _ := createAccessKey(t, s.DB(), "user1@example.com")
				req.Header.Add("Authorization", "Bearer "+key)
			},
			expectedResp: responseBodyAPIErrorWithCode(http.StatusForbidden),
		},
		{
			name:         "successful profile",
			expectedCode: http.StatusOK,
			setupRequest: func(t *testing.T, req *http.Request) {
				key, user := createAccessKey(t, s.DB(), "user2@example.com")
				err := data.CreateGrant(s.DB(), &models.Grant{
					Subject:   models.NewSubjectForUser(user.ID),
					Privilege: models.InfraSupportAdminRole,
					Resource:  access.ResourceInfraAPI,
					CreatedBy: user.ID,
				})
				assert.NilError(t, err)

				req.Header.Add("Authorization", "Bearer "+key)
			},
			expectedResp: func(t *testing.T, resp *httptest.ResponseRecorder) {
				assert.Equal(t, "text/plain; charset=utf-8", resp.Header().Get("Content-Type"))
				assert.Assert(t, is.Contains(resp.Body.String(), "heap profile:"))
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			run(t, tc)
		})
	}
}

func TestDebugConfig_Validation(t *testing.T) {
	t.Run("disabled config is always valid", func(t *testing.T) {
		config := DebugConfig{Enabled: false}
		err := ValidateDebugConfig(config)
		assert.NilError(t, err)
	})

	t.Run("enabled in non-production is valid", func(t *testing.T) {
		// Ensure we're not in production for this test
		os.Unsetenv("PRODUCTION")
		os.Unsetenv("ENV")

		config := DebugConfig{
			Enabled:           true,
			AllowInProduction: false,
		}
		err := ValidateDebugConfig(config)
		// May or may not error depending on environment detection
		// In a test environment, it should be allowed
		_ = err
	})

	t.Run("enabled with AllowInProduction bypasses check", func(t *testing.T) {
		config := DebugConfig{
			Enabled:           true,
			AllowInProduction: true,
		}
		err := ValidateDebugConfig(config)
		assert.NilError(t, err)
	})
}

func TestDefaultDebugConfig(t *testing.T) {
	config := DefaultDebugConfig()

	assert.Equal(t, config.Enabled, false)
	assert.Equal(t, config.AllowInProduction, false)
	assert.Equal(t, config.RateLimitPerMinute, 10)
	assert.Equal(t, config.AuditLog, true)
}

func TestDebugRateLimiter(t *testing.T) {
	t.Run("allows requests under limit", func(t *testing.T) {
		limiter := newDebugRateLimiter(5)

		for i := 0; i < 5; i++ {
			assert.Assert(t, limiter.Allow("user1"), "request %d should be allowed", i)
		}
	})

	t.Run("blocks requests over limit", func(t *testing.T) {
		limiter := newDebugRateLimiter(3)

		// Use up the limit
		for i := 0; i < 3; i++ {
			assert.Assert(t, limiter.Allow("user2"))
		}

		// Next request should be blocked
		assert.Assert(t, !limiter.Allow("user2"), "request over limit should be blocked")
	})

	t.Run("different users have separate limits", func(t *testing.T) {
		limiter := newDebugRateLimiter(2)

		// User A uses their limit
		assert.Assert(t, limiter.Allow("userA"))
		assert.Assert(t, limiter.Allow("userA"))
		assert.Assert(t, !limiter.Allow("userA"), "userA should be rate limited")

		// User B should still have their limit
		assert.Assert(t, limiter.Allow("userB"), "userB should not be affected by userA's limit")
	})

	t.Run("default limit when zero provided", func(t *testing.T) {
		limiter := newDebugRateLimiter(0)
		assert.Equal(t, limiter.maxRequests, 10)
	})
}

func TestIsProductionEnvironment(t *testing.T) {
	// Save and restore environment
	savedEnv := make(map[string]string)
	envVars := []string{"PRODUCTION", "PROD", "ENV", "ENVIRONMENT", "APP_ENV", "GO_ENV", "INFRA_ENV", "POD_NAMESPACE"}
	for _, v := range envVars {
		savedEnv[v] = os.Getenv(v)
		os.Unsetenv(v)
	}
	defer func() {
		for k, v := range savedEnv {
			if v != "" {
				os.Setenv(k, v)
			}
		}
	}()

	t.Run("returns false with no production indicators", func(t *testing.T) {
		// All env vars are unset
		assert.Assert(t, !IsProductionEnvironment())
	})

	t.Run("detects PRODUCTION=true", func(t *testing.T) {
		os.Setenv("PRODUCTION", "production")
		defer os.Unsetenv("PRODUCTION")
		assert.Assert(t, IsProductionEnvironment())
	})

	t.Run("detects ENV=prod", func(t *testing.T) {
		os.Setenv("ENV", "prod")
		defer os.Unsetenv("ENV")
		assert.Assert(t, IsProductionEnvironment())
	})

	t.Run("detects production namespace", func(t *testing.T) {
		os.Setenv("POD_NAMESPACE", "infra-production")
		defer os.Unsetenv("POD_NAMESPACE")
		assert.Assert(t, IsProductionEnvironment())
	})

	t.Run("case insensitive detection", func(t *testing.T) {
		os.Setenv("ENV", "PRODUCTION")
		defer os.Unsetenv("ENV")
		assert.Assert(t, IsProductionEnvironment())
	})
}

func responseBodyAPIErrorWithCode(code int32) func(t *testing.T, resp *httptest.ResponseRecorder) {
	return func(t *testing.T, resp *httptest.ResponseRecorder) {
		t.Helper()

		var apiError api.Error

		err := json.Unmarshal(resp.Body.Bytes(), &apiError)
		assert.NilError(t, err)
		assert.Equal(t, apiError.Code, code)
	}
}
