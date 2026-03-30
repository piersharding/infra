package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"gotest.tools/v3/assert"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestGenerateCSRFToken(t *testing.T) {
	token1, err := generateCSRFToken()
	assert.NilError(t, err)
	assert.Assert(t, len(token1) > 0, "token should not be empty")

	token2, err := generateCSRFToken()
	assert.NilError(t, err)
	assert.Assert(t, token1 != token2, "tokens should be unique")
}

func TestCSRFTokenStore(t *testing.T) {
	store := newCSRFTokenStore()

	token := "test-token"
	store.add(token)

	assert.Assert(t, store.validate(token), "token should be valid")
	assert.Assert(t, !store.validate("invalid-token"), "invalid token should not be valid")

	store.remove(token)
	assert.Assert(t, !store.validate(token), "removed token should not be valid")
}

func TestIsSafeMethod(t *testing.T) {
	testCases := []struct {
		method string
		safe   bool
	}{
		{http.MethodGet, true},
		{http.MethodHead, true},
		{http.MethodOptions, true},
		{http.MethodTrace, true},
		{http.MethodPost, false},
		{http.MethodPut, false},
		{http.MethodPatch, false},
		{http.MethodDelete, false},
	}

	for _, tc := range testCases {
		t.Run(tc.method, func(t *testing.T) {
			result := isSafeMethod(tc.method)
			assert.Equal(t, tc.safe, result)
		})
	}
}

func TestIsPathExempt(t *testing.T) {
	exemptPaths := []string{
		"/api/login",
		"/api/signup",
		"/api/scim/",
	}

	testCases := []struct {
		path   string
		exempt bool
	}{
		{"/api/login", true},
		{"/api/signup", true},
		{"/api/scim/v2/Users", true},
		{"/api/users", false},
		{"/api/grants", false},
	}

	for _, tc := range testCases {
		t.Run(tc.path, func(t *testing.T) {
			result := isPathExempt(tc.path, exemptPaths)
			assert.Equal(t, tc.exempt, result)
		})
	}
}

func TestCSRFMiddleware_SafeMethods(t *testing.T) {
	config := DefaultCSRFConfig()
	config.Secure = false // for testing without HTTPS

	router := gin.New()
	router.Use(CSRFMiddleware(config))
	router.GET("/test", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	resp := httptest.NewRecorder()

	router.ServeHTTP(resp, req)

	assert.Equal(t, http.StatusOK, resp.Code)
}

func TestCSRFMiddleware_UnsafeMethodWithoutToken(t *testing.T) {
	config := DefaultCSRFConfig()
	config.Secure = false

	router := gin.New()
	router.Use(CSRFMiddleware(config))
	router.POST("/api/test", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/api/test", nil)
	// Add a cookie to simulate cookie-based auth
	req.AddCookie(&http.Cookie{Name: cookieAuthName, Value: "test-auth"})
	resp := httptest.NewRecorder()

	router.ServeHTTP(resp, req)

	// Should fail because no CSRF token is provided
	assert.Equal(t, http.StatusForbidden, resp.Code)
}

func TestCSRFMiddleware_UnsafeMethodWithValidToken(t *testing.T) {
	config := DefaultCSRFConfig()
	config.Secure = false

	router := gin.New()
	router.Use(CSRFMiddleware(config))
	router.POST("/api/test", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	// Generate a valid token
	token, err := generateCSRFToken()
	assert.NilError(t, err)
	globalCSRFStore.add(token)

	req := httptest.NewRequest(http.MethodPost, "/api/test", nil)
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: token})
	req.AddCookie(&http.Cookie{Name: cookieAuthName, Value: "test-auth"})
	req.Header.Set(csrfTokenHeader, token)
	resp := httptest.NewRecorder()

	router.ServeHTTP(resp, req)

	assert.Equal(t, http.StatusOK, resp.Code)
}

func TestCSRFMiddleware_UnsafeMethodWithMismatchedToken(t *testing.T) {
	config := DefaultCSRFConfig()
	config.Secure = false

	router := gin.New()
	router.Use(CSRFMiddleware(config))
	router.POST("/api/test", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	// Generate a valid token for the cookie
	cookieToken, err := generateCSRFToken()
	assert.NilError(t, err)
	globalCSRFStore.add(cookieToken)

	// Use a different token in the header
	headerToken, err := generateCSRFToken()
	assert.NilError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/test", nil)
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: cookieToken})
	req.AddCookie(&http.Cookie{Name: cookieAuthName, Value: "test-auth"})
	req.Header.Set(csrfTokenHeader, headerToken)
	resp := httptest.NewRecorder()

	router.ServeHTTP(resp, req)

	// Should fail because tokens don't match
	assert.Equal(t, http.StatusForbidden, resp.Code)
}

func TestCSRFMiddleware_ExemptPath(t *testing.T) {
	config := DefaultCSRFConfig()
	config.Secure = false

	router := gin.New()
	router.Use(CSRFMiddleware(config))
	router.POST("/api/login", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/api/login", nil)
	resp := httptest.NewRecorder()

	router.ServeHTTP(resp, req)

	// Should succeed because /api/login is exempt
	assert.Equal(t, http.StatusOK, resp.Code)
}

func TestCSRFMiddleware_BearerTokenOnly(t *testing.T) {
	config := DefaultCSRFConfig()
	config.Secure = false

	router := gin.New()
	router.Use(CSRFMiddleware(config))
	router.POST("/api/test", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/api/test", nil)
	req.Header.Set("Authorization", "Bearer some-access-key")
	req.Header.Set("Infra-Version", "0.1.0")
	// No auth cookie - this is bearer token only auth
	resp := httptest.NewRecorder()

	router.ServeHTTP(resp, req)

	// Should succeed because no cookie auth is used
	assert.Equal(t, http.StatusOK, resp.Code)
}

func TestCSRFMiddleware_Disabled(t *testing.T) {
	config := DefaultCSRFConfig()
	config.Enabled = false

	router := gin.New()
	router.Use(CSRFMiddleware(config))
	router.POST("/api/test", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/api/test", nil)
	req.AddCookie(&http.Cookie{Name: cookieAuthName, Value: "test-auth"})
	resp := httptest.NewRecorder()

	router.ServeHTTP(resp, req)

	// Should succeed because CSRF is disabled
	assert.Equal(t, http.StatusOK, resp.Code)
}

func TestValidateOrigin(t *testing.T) {
	testCases := []struct {
		name           string
		origin         string
		referer        string
		host           string
		trustedOrigins []string
		valid          bool
	}{
		{
			name:           "same origin",
			origin:         "https://example.com",
			host:           "example.com",
			trustedOrigins: []string{},
			valid:          true,
		},
		{
			name:           "trusted origin",
			origin:         "https://trusted.com",
			host:           "example.com",
			trustedOrigins: []string{"https://trusted.com"},
			valid:          true,
		},
		{
			name:           "no origin header",
			origin:         "",
			referer:        "",
			host:           "example.com",
			trustedOrigins: []string{},
			valid:          true, // Relies on CSRF token validation
		},
		{
			name:           "referer fallback",
			origin:         "",
			referer:        "https://example.com/page",
			host:           "example.com",
			trustedOrigins: []string{},
			valid:          true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/test", nil)
			req.Host = tc.host
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.referer != "" {
				req.Header.Set("Referer", tc.referer)
			}

			result := validateOrigin(req, tc.trustedOrigins)
			assert.Equal(t, tc.valid, result)
		})
	}
}

func TestRefreshCSRFToken(t *testing.T) {
	config := DefaultCSRFConfig()
	config.Secure = false

	router := gin.New()
	router.GET("/test", func(c *gin.Context) {
		token, err := RefreshCSRFToken(c, config)
		assert.NilError(t, err)
		assert.Assert(t, len(token) > 0)
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	resp := httptest.NewRecorder()

	router.ServeHTTP(resp, req)

	assert.Equal(t, http.StatusOK, resp.Code)

	// Check that the CSRF token header is set
	csrfHeader := resp.Header().Get(csrfTokenHeader)
	assert.Assert(t, len(csrfHeader) > 0, "CSRF token header should be set")
}

func TestGetCSRFToken(t *testing.T) {
	router := gin.New()
	router.GET("/test", func(c *gin.Context) {
		token := GetCSRFToken(c)
		c.String(http.StatusOK, token)
	})

	//nolint:gosec // Fixed test token used only to verify cookie/header plumbing in unit tests.
	expectedToken := "test-csrf-token"
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: expectedToken})
	resp := httptest.NewRecorder()

	router.ServeHTTP(resp, req)

	assert.Equal(t, http.StatusOK, resp.Code)
	assert.Equal(t, expectedToken, resp.Body.String())
}
