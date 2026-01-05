package server

import (
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
)

// SecurityHeadersConfig holds configuration for the security headers middleware
type SecurityHeadersConfig struct {
	// Enabled determines whether security headers middleware is active
	// Default: true
	Enabled bool

	// HSTS (HTTP Strict Transport Security) settings
	HSTS HSTSConfig

	// ContentSecurityPolicy defines the Content-Security-Policy header value
	// If empty, a restrictive default policy is used
	ContentSecurityPolicy string

	// XFrameOptions controls the X-Frame-Options header
	// Valid values: "DENY", "SAMEORIGIN", or "ALLOW-FROM uri"
	// Default: "DENY"
	XFrameOptions string

	// XContentTypeOptions controls the X-Content-Type-Options header
	// Default: "nosniff"
	XContentTypeOptions string

	// ReferrerPolicy controls the Referrer-Policy header
	// Default: "strict-origin-when-cross-origin"
	ReferrerPolicy string

	// PermissionsPolicy controls the Permissions-Policy header
	// If empty, a restrictive default policy is used
	PermissionsPolicy string

	// CrossOriginEmbedderPolicy controls the Cross-Origin-Embedder-Policy header
	// Default: "require-corp"
	CrossOriginEmbedderPolicy string

	// CrossOriginOpenerPolicy controls the Cross-Origin-Opener-Policy header
	// Default: "same-origin"
	CrossOriginOpenerPolicy string

	// CrossOriginResourcePolicy controls the Cross-Origin-Resource-Policy header
	// Default: "same-origin"
	CrossOriginResourcePolicy string

	// ExcludePaths are paths that should not have security headers applied
	// This is useful for paths that serve embedded content or have special requirements
	ExcludePaths []string
}

// HSTSConfig holds configuration for HTTP Strict Transport Security
type HSTSConfig struct {
	// Enabled determines whether HSTS header is sent
	// Default: true
	Enabled bool

	// MaxAge is the time in seconds that the browser should remember to only use HTTPS
	// Default: 31536000 (1 year)
	MaxAge int

	// IncludeSubDomains specifies whether HSTS applies to subdomains
	// Default: true
	IncludeSubDomains bool

	// Preload indicates the site should be included in browser HSTS preload lists
	// Only enable this if you're sure your site will always support HTTPS
	// Default: false
	Preload bool
}

// DefaultSecurityHeadersConfig returns a secure default configuration
func DefaultSecurityHeadersConfig() SecurityHeadersConfig {
	return SecurityHeadersConfig{
		Enabled: true,
		HSTS: HSTSConfig{
			Enabled:           true,
			MaxAge:            31536000, // 1 year
			IncludeSubDomains: true,
			Preload:           false,
		},
		// Restrictive default CSP that allows same-origin resources
		// This should be customized based on application needs
		ContentSecurityPolicy: "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'",
		XFrameOptions:         "DENY",
		XContentTypeOptions:   "nosniff",
		ReferrerPolicy:        "strict-origin-when-cross-origin",
		// Restrictive permissions policy
		PermissionsPolicy:         "accelerometer=(), camera=(), geolocation=(), gyroscope=(), magnetometer=(), microphone=(), payment=(), usb=()",
		CrossOriginEmbedderPolicy: "require-corp",
		CrossOriginOpenerPolicy:   "same-origin",
		CrossOriginResourcePolicy: "same-origin",
		ExcludePaths:              []string{},
	}
}

// APISecurityHeadersConfig returns a configuration suitable for API-only servers
// This is less restrictive than the default but still secure for API usage
func APISecurityHeadersConfig() SecurityHeadersConfig {
	return SecurityHeadersConfig{
		Enabled: true,
		HSTS: HSTSConfig{
			Enabled:           true,
			MaxAge:            31536000,
			IncludeSubDomains: true,
			Preload:           false,
		},
		// API-friendly CSP - no need for script/style sources
		ContentSecurityPolicy:     "default-src 'none'; frame-ancestors 'none'",
		XFrameOptions:             "DENY",
		XContentTypeOptions:       "nosniff",
		ReferrerPolicy:            "no-referrer",
		PermissionsPolicy:         "accelerometer=(), camera=(), geolocation=(), gyroscope=(), magnetometer=(), microphone=(), payment=(), usb=()",
		CrossOriginEmbedderPolicy: "", // Not typically needed for APIs
		CrossOriginOpenerPolicy:   "", // Not typically needed for APIs
		CrossOriginResourcePolicy: "same-origin",
		ExcludePaths:              []string{},
	}
}

// SecurityHeadersMiddleware returns a Gin middleware that sets HTTP security headers
func SecurityHeadersMiddleware(config SecurityHeadersConfig) gin.HandlerFunc {
	// Pre-compute the HSTS header value for performance
	hstsValue := buildHSTSValue(config.HSTS)

	return func(c *gin.Context) {
		if !config.Enabled {
			c.Next()
			return
		}

		// Check if path is excluded
		if isPathExcluded(c.Request.URL.Path, config.ExcludePaths) {
			c.Next()
			return
		}

		// Set HSTS header (only meaningful over HTTPS, but browsers ignore it over HTTP)
		if config.HSTS.Enabled && hstsValue != "" {
			c.Header("Strict-Transport-Security", hstsValue)
		}

		// Set Content-Security-Policy
		if config.ContentSecurityPolicy != "" {
			c.Header("Content-Security-Policy", config.ContentSecurityPolicy)
		}

		// Set X-Frame-Options
		if config.XFrameOptions != "" {
			c.Header("X-Frame-Options", config.XFrameOptions)
		}

		// Set X-Content-Type-Options
		if config.XContentTypeOptions != "" {
			c.Header("X-Content-Type-Options", config.XContentTypeOptions)
		}

		// Set Referrer-Policy
		if config.ReferrerPolicy != "" {
			c.Header("Referrer-Policy", config.ReferrerPolicy)
		}

		// Set Permissions-Policy
		if config.PermissionsPolicy != "" {
			c.Header("Permissions-Policy", config.PermissionsPolicy)
		}

		// Set Cross-Origin headers
		if config.CrossOriginEmbedderPolicy != "" {
			c.Header("Cross-Origin-Embedder-Policy", config.CrossOriginEmbedderPolicy)
		}
		if config.CrossOriginOpenerPolicy != "" {
			c.Header("Cross-Origin-Opener-Policy", config.CrossOriginOpenerPolicy)
		}
		if config.CrossOriginResourcePolicy != "" {
			c.Header("Cross-Origin-Resource-Policy", config.CrossOriginResourcePolicy)
		}

		// Additional security headers
		// Prevent browsers from performing MIME type sniffing
		c.Header("X-Content-Type-Options", "nosniff")

		// Disable client-side caching for sensitive responses
		// Individual handlers can override this for static assets
		if isAPIPaths(c.Request.URL.Path) {
			c.Header("Cache-Control", "no-store, no-cache, must-revalidate, proxy-revalidate")
			c.Header("Pragma", "no-cache")
			c.Header("Expires", "0")
		}

		c.Next()
	}
}

// buildHSTSValue constructs the Strict-Transport-Security header value
func buildHSTSValue(config HSTSConfig) string {
	if !config.Enabled || config.MaxAge <= 0 {
		return ""
	}

	var parts []string
	parts = append(parts, fmt.Sprintf("max-age=%d", config.MaxAge))

	if config.IncludeSubDomains {
		parts = append(parts, "includeSubDomains")
	}

	if config.Preload {
		parts = append(parts, "preload")
	}

	return strings.Join(parts, "; ")
}

// isPathExcluded checks if a path should be excluded from security headers
func isPathExcluded(path string, excludePaths []string) bool {
	for _, excluded := range excludePaths {
		if strings.HasPrefix(path, excluded) {
			return true
		}
	}
	return false
}

// isAPIPaths checks if the path is an API endpoint
func isAPIPaths(path string) bool {
	return strings.HasPrefix(path, "/api/")
}

// WithContentSecurityPolicy returns a copy of the config with a custom CSP
func (c SecurityHeadersConfig) WithContentSecurityPolicy(csp string) SecurityHeadersConfig {
	c.ContentSecurityPolicy = csp
	return c
}

// WithHSTSPreload returns a copy of the config with HSTS preload enabled
// WARNING: Only enable this if your site will always support HTTPS
func (c SecurityHeadersConfig) WithHSTSPreload() SecurityHeadersConfig {
	c.HSTS.Preload = true
	return c
}

// WithExcludedPaths returns a copy of the config with additional excluded paths
func (c SecurityHeadersConfig) WithExcludedPaths(paths ...string) SecurityHeadersConfig {
	c.ExcludePaths = append(c.ExcludePaths, paths...)
	return c
}

// Disable returns a copy of the config with security headers disabled
func (c SecurityHeadersConfig) Disable() SecurityHeadersConfig {
	c.Enabled = false
	return c
}
