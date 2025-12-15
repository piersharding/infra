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

func TestDefaultSecurityHeadersConfig(t *testing.T) {
	config := DefaultSecurityHeadersConfig()

	assert.Equal(t, config.Enabled, true)
	assert.Equal(t, config.HSTS.Enabled, true)
	assert.Equal(t, config.HSTS.MaxAge, 31536000)
	assert.Equal(t, config.HSTS.IncludeSubDomains, true)
	assert.Equal(t, config.HSTS.Preload, false)
	assert.Equal(t, config.XFrameOptions, "DENY")
	assert.Equal(t, config.XContentTypeOptions, "nosniff")
	assert.Equal(t, config.ReferrerPolicy, "strict-origin-when-cross-origin")
	assert.Assert(t, config.ContentSecurityPolicy != "")
	assert.Assert(t, config.PermissionsPolicy != "")
}

func TestAPISecurityHeadersConfig(t *testing.T) {
	config := APISecurityHeadersConfig()

	assert.Equal(t, config.Enabled, true)
	assert.Equal(t, config.HSTS.Enabled, true)
	assert.Equal(t, config.XFrameOptions, "DENY")
	assert.Equal(t, config.ReferrerPolicy, "no-referrer")
	// API config should have minimal CSP
	assert.Equal(t, config.ContentSecurityPolicy, "default-src 'none'; frame-ancestors 'none'")
}

func TestSecurityHeadersMiddleware(t *testing.T) {
	t.Run("Default Headers Applied", func(t *testing.T) {
		router := gin.New()
		router.Use(SecurityHeadersMiddleware(DefaultSecurityHeadersConfig()))
		router.GET("/test", func(c *gin.Context) {
			c.String(http.StatusOK, "OK")
		})

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, w.Code, http.StatusOK)

		// Check HSTS header
		hsts := w.Header().Get("Strict-Transport-Security")
		assert.Assert(t, hsts != "", "HSTS header should be set")
		assert.Assert(t, contains(hsts, "max-age=31536000"), "HSTS should have max-age")
		assert.Assert(t, contains(hsts, "includeSubDomains"), "HSTS should include subdomains")

		// Check other security headers
		assert.Equal(t, w.Header().Get("X-Frame-Options"), "DENY")
		assert.Equal(t, w.Header().Get("X-Content-Type-Options"), "nosniff")
		assert.Assert(t, w.Header().Get("Content-Security-Policy") != "")
		assert.Assert(t, w.Header().Get("Referrer-Policy") != "")
		assert.Assert(t, w.Header().Get("Permissions-Policy") != "")
	})

	t.Run("Disabled Middleware", func(t *testing.T) {
		config := DefaultSecurityHeadersConfig()
		config.Enabled = false

		router := gin.New()
		router.Use(SecurityHeadersMiddleware(config))
		router.GET("/test", func(c *gin.Context) {
			c.String(http.StatusOK, "OK")
		})

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, w.Code, http.StatusOK)

		// Headers should NOT be set when disabled
		assert.Equal(t, w.Header().Get("Strict-Transport-Security"), "")
		assert.Equal(t, w.Header().Get("X-Frame-Options"), "")
		assert.Equal(t, w.Header().Get("Content-Security-Policy"), "")
	})

	t.Run("Excluded Paths", func(t *testing.T) {
		config := DefaultSecurityHeadersConfig()
		config.ExcludePaths = []string{"/excluded/"}

		router := gin.New()
		router.Use(SecurityHeadersMiddleware(config))
		router.GET("/excluded/resource", func(c *gin.Context) {
			c.String(http.StatusOK, "OK")
		})
		router.GET("/included/resource", func(c *gin.Context) {
			c.String(http.StatusOK, "OK")
		})

		// Test excluded path - should NOT have security headers
		req := httptest.NewRequest(http.MethodGet, "/excluded/resource", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, w.Code, http.StatusOK)
		assert.Equal(t, w.Header().Get("X-Frame-Options"), "")

		// Test included path - should have security headers
		req = httptest.NewRequest(http.MethodGet, "/included/resource", nil)
		w = httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, w.Code, http.StatusOK)
		assert.Equal(t, w.Header().Get("X-Frame-Options"), "DENY")
	})

	t.Run("API Path Caching Headers", func(t *testing.T) {
		router := gin.New()
		router.Use(SecurityHeadersMiddleware(DefaultSecurityHeadersConfig()))
		router.GET("/api/users", func(c *gin.Context) {
			c.String(http.StatusOK, "OK")
		})

		req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, w.Code, http.StatusOK)

		// API paths should have no-cache headers
		cacheControl := w.Header().Get("Cache-Control")
		assert.Assert(t, contains(cacheControl, "no-store"), "API should have no-store cache control")
		assert.Equal(t, w.Header().Get("Pragma"), "no-cache")
	})

	t.Run("Non-API Path Without Extra Caching Headers", func(t *testing.T) {
		router := gin.New()
		router.Use(SecurityHeadersMiddleware(DefaultSecurityHeadersConfig()))
		router.GET("/static/file.js", func(c *gin.Context) {
			c.String(http.StatusOK, "OK")
		})

		req := httptest.NewRequest(http.MethodGet, "/static/file.js", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, w.Code, http.StatusOK)

		// Non-API paths should not have no-cache headers forced
		assert.Equal(t, w.Header().Get("Pragma"), "")
	})

	t.Run("HSTS With Preload", func(t *testing.T) {
		config := DefaultSecurityHeadersConfig()
		config.HSTS.Preload = true

		router := gin.New()
		router.Use(SecurityHeadersMiddleware(config))
		router.GET("/test", func(c *gin.Context) {
			c.String(http.StatusOK, "OK")
		})

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		hsts := w.Header().Get("Strict-Transport-Security")
		assert.Assert(t, contains(hsts, "preload"), "HSTS should include preload when enabled")
	})

	t.Run("HSTS Disabled", func(t *testing.T) {
		config := DefaultSecurityHeadersConfig()
		config.HSTS.Enabled = false

		router := gin.New()
		router.Use(SecurityHeadersMiddleware(config))
		router.GET("/test", func(c *gin.Context) {
			c.String(http.StatusOK, "OK")
		})

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, w.Header().Get("Strict-Transport-Security"), "")
	})

	t.Run("Cross-Origin Headers", func(t *testing.T) {
		router := gin.New()
		router.Use(SecurityHeadersMiddleware(DefaultSecurityHeadersConfig()))
		router.GET("/test", func(c *gin.Context) {
			c.String(http.StatusOK, "OK")
		})

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, w.Header().Get("Cross-Origin-Embedder-Policy"), "require-corp")
		assert.Equal(t, w.Header().Get("Cross-Origin-Opener-Policy"), "same-origin")
		assert.Equal(t, w.Header().Get("Cross-Origin-Resource-Policy"), "same-origin")
	})
}

func TestBuildHSTSValue(t *testing.T) {
	t.Run("Full HSTS Value", func(t *testing.T) {
		config := HSTSConfig{
			Enabled:           true,
			MaxAge:            31536000,
			IncludeSubDomains: true,
			Preload:           true,
		}

		value := buildHSTSValue(config)
		assert.Assert(t, contains(value, "max-age=31536000"))
		assert.Assert(t, contains(value, "includeSubDomains"))
		assert.Assert(t, contains(value, "preload"))
	})

	t.Run("HSTS Without Preload", func(t *testing.T) {
		config := HSTSConfig{
			Enabled:           true,
			MaxAge:            31536000,
			IncludeSubDomains: true,
			Preload:           false,
		}

		value := buildHSTSValue(config)
		assert.Assert(t, contains(value, "max-age=31536000"))
		assert.Assert(t, contains(value, "includeSubDomains"))
		assert.Assert(t, !contains(value, "preload"))
	})

	t.Run("HSTS Without IncludeSubDomains", func(t *testing.T) {
		config := HSTSConfig{
			Enabled:           true,
			MaxAge:            31536000,
			IncludeSubDomains: false,
			Preload:           false,
		}

		value := buildHSTSValue(config)
		assert.Assert(t, contains(value, "max-age=31536000"))
		assert.Assert(t, !contains(value, "includeSubDomains"))
	})

	t.Run("HSTS Disabled", func(t *testing.T) {
		config := HSTSConfig{
			Enabled: false,
			MaxAge:  31536000,
		}

		value := buildHSTSValue(config)
		assert.Equal(t, value, "")
	})

	t.Run("HSTS Zero MaxAge", func(t *testing.T) {
		config := HSTSConfig{
			Enabled: true,
			MaxAge:  0,
		}

		value := buildHSTSValue(config)
		assert.Equal(t, value, "")
	})
}

func TestIsPathExcluded(t *testing.T) {
	excludePaths := []string{"/health", "/metrics/", "/api/public/"}

	tests := []struct {
		path     string
		excluded bool
	}{
		{"/health", true},
		{"/healthz", true}, // Prefix match - /healthz starts with /health
		{"/metrics/", true},
		{"/metrics/prometheus", true}, // Prefix match
		{"/api/public/", true},
		{"/api/public/resource", true}, // Prefix match
		{"/api/private/resource", false},
		{"/", false},
		{"/other", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result := isPathExcluded(tt.path, excludePaths)
			assert.Equal(t, result, tt.excluded, "path %s exclusion mismatch", tt.path)
		})
	}
}

func TestIsAPIPaths(t *testing.T) {
	tests := []struct {
		path  string
		isAPI bool
	}{
		{"/api/users", true},
		{"/api/", true},
		{"/api", false}, // Must have trailing slash to be prefix
		{"/static/file.js", false},
		{"/", false},
		{"/apikeys", false}, // Not under /api/
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result := isAPIPaths(tt.path)
			assert.Equal(t, result, tt.isAPI, "path %s API check mismatch", tt.path)
		})
	}
}

func TestSecurityHeadersConfigBuilders(t *testing.T) {
	t.Run("WithContentSecurityPolicy", func(t *testing.T) {
		config := DefaultSecurityHeadersConfig()
		customCSP := "default-src 'self'; script-src 'self' https://cdn.example.com"

		newConfig := config.WithContentSecurityPolicy(customCSP)

		assert.Equal(t, newConfig.ContentSecurityPolicy, customCSP)
		// Original should be unchanged
		assert.Assert(t, config.ContentSecurityPolicy != customCSP)
	})

	t.Run("WithHSTSPreload", func(t *testing.T) {
		config := DefaultSecurityHeadersConfig()
		assert.Equal(t, config.HSTS.Preload, false)

		newConfig := config.WithHSTSPreload()

		assert.Equal(t, newConfig.HSTS.Preload, true)
		// Original should be unchanged
		assert.Equal(t, config.HSTS.Preload, false)
	})

	t.Run("WithExcludedPaths", func(t *testing.T) {
		config := DefaultSecurityHeadersConfig()
		originalLen := len(config.ExcludePaths)

		newConfig := config.WithExcludedPaths("/health", "/metrics")

		assert.Equal(t, len(newConfig.ExcludePaths), originalLen+2)
		assert.Assert(t, containsString(newConfig.ExcludePaths, "/health"))
		assert.Assert(t, containsString(newConfig.ExcludePaths, "/metrics"))
	})

	t.Run("Disable", func(t *testing.T) {
		config := DefaultSecurityHeadersConfig()
		assert.Equal(t, config.Enabled, true)

		newConfig := config.Disable()

		assert.Equal(t, newConfig.Enabled, false)
		// Original should be unchanged
		assert.Equal(t, config.Enabled, true)
	})
}

// Helper function to check if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && searchString(s, substr)))
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// Helper function to check if a slice contains a string
func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}
