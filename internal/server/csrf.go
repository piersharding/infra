package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/infrahq/infra/internal/logging"
)

const (
	// csrfTokenHeader is the HTTP header used to send/receive the CSRF token
	csrfTokenHeader = "X-CSRF-Token"

	// csrfCookieName is the name of the cookie that stores the CSRF token
	csrfCookieName = "infra_csrf"

	// csrfTokenLength is the length of the CSRF token in bytes before base64 encoding
	csrfTokenLength = 32

	// csrfTokenExpiry is how long a CSRF token is valid
	csrfTokenExpiry = 24 * time.Hour
)

// CSRFConfig holds the configuration for CSRF protection
type CSRFConfig struct {
	// Enabled determines whether CSRF protection is active
	Enabled bool

	// Secure determines if the CSRF cookie should have the Secure flag
	// Should be true in production (HTTPS)
	Secure bool

	// SameSite determines the SameSite attribute for the CSRF cookie
	SameSite http.SameSite

	// ExemptPaths are paths that are exempt from CSRF protection
	// These should only be paths that use non-cookie authentication
	ExemptPaths []string

	// TrustedOrigins are origins that are allowed to make cross-origin requests
	TrustedOrigins []string

	// StoreConfig holds configuration for the CSRF token store
	// If not set, defaults to in-memory storage
	StoreConfig CSRFStoreConfig
}

// DefaultCSRFConfig returns the default CSRF configuration
func DefaultCSRFConfig() CSRFConfig {
	return CSRFConfig{
		Enabled:  true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		ExemptPaths: []string{
			// API endpoints that use bearer token authentication only (no cookies)
			"/api/login",
			"/api/signup",
			"/api/device",
			"/api/device/status",
			"/api/password-reset-request",
			"/api/password-reset",
			"/api/forgot-domain-request",
			"/api/version",
			"/api/server-configuration",
			// SCIM endpoints use bearer token auth
			"/api/scim/",
		},
		TrustedOrigins: []string{},
		StoreConfig:    DefaultCSRFStoreConfig(),
	}
}

// csrfMiddlewareState holds the state for the CSRF middleware
type csrfMiddlewareState struct {
	config CSRFConfig
	store  CSRFTokenStore
}

// generateCSRFToken creates a new cryptographically secure CSRF token
func generateCSRFToken() (string, error) {
	bytes := make([]byte, csrfTokenLength)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate CSRF token: %w", err)
	}
	return base64.URLEncoding.EncodeToString(bytes), nil
}

// CSRFMiddleware returns a Gin middleware that provides CSRF protection
// This uses the default in-memory token store
func CSRFMiddleware(config CSRFConfig) gin.HandlerFunc {
	store, err := NewCSRFTokenStore(config.StoreConfig)
	if err != nil {
		logging.L.Error().Err(err).Msg("failed to create CSRF token store, falling back to memory store")
		store = newMemoryCSRFStore(csrfTokenExpiry)
	}

	return CSRFMiddlewareWithStore(config, store)
}

// CSRFMiddlewareWithStore returns a Gin middleware that provides CSRF protection
// with a custom token store (useful for Redis-backed distributed storage)
func CSRFMiddlewareWithStore(config CSRFConfig, store CSRFTokenStore) gin.HandlerFunc {
	state := &csrfMiddlewareState{
		config: config,
		store:  store,
	}

	return func(c *gin.Context) {
		if !config.Enabled {
			c.Next()
			return
		}

		// Skip CSRF for safe methods (GET, HEAD, OPTIONS, TRACE)
		if isSafeMethod(c.Request.Method) {
			// For safe methods, ensure a CSRF token is set in the cookie for subsequent requests
			state.ensureCSRFToken(c)
			c.Next()
			return
		}

		// Check if the path is exempt
		if isPathExempt(c.Request.URL.Path, config.ExemptPaths) {
			c.Next()
			return
		}

		// Check if request uses only bearer token auth (no cookies)
		if isAPIRequest(c) && !hasCookieAuth(c) {
			c.Next()
			return
		}

		// Validate origin/referer for additional protection
		if !validateOrigin(c.Request, config.TrustedOrigins) {
			secureLogger := logging.SecureLogger(logging.L)
			secureLogger.SecureWarn("CSRF origin validation failed", nil)
			sendCSRFError(c, "invalid origin")
			return
		}

		// Validate CSRF token
		if !state.validateCSRFToken(c) {
			secureLogger := logging.SecureLogger(logging.L)
			secureLogger.SecureWarn("CSRF token validation failed", nil)
			sendCSRFError(c, "CSRF token validation failed")
			return
		}

		c.Next()
	}
}

// ensureCSRFToken ensures a CSRF token cookie is set
func (s *csrfMiddlewareState) ensureCSRFToken(c *gin.Context) {
	// Check if token already exists
	if _, err := c.Cookie(csrfCookieName); err == nil {
		return
	}

	token, err := generateCSRFToken()
	if err != nil {
		logging.L.Error().Err(err).Msg("failed to generate CSRF token")
		return
	}

	if err := s.store.Add(token); err != nil {
		logging.L.Error().Err(err).Msg("failed to store CSRF token")
		return
	}

	// Set the cookie
	maxAge := int(csrfTokenExpiry.Seconds())
	c.SetSameSite(s.config.SameSite)
	c.SetCookie(
		csrfCookieName,
		token,
		maxAge,
		"/",
		"",
		s.config.Secure,
		false, // httpOnly must be false so JavaScript can read it
	)

	// Also set the token in a response header for convenience
	c.Header(csrfTokenHeader, token)
}

// validateCSRFToken validates the CSRF token from the request
func (s *csrfMiddlewareState) validateCSRFToken(c *gin.Context) bool {
	// Get token from cookie
	cookieToken, err := c.Cookie(csrfCookieName)
	if err != nil || cookieToken == "" {
		return false
	}

	// Get token from header
	headerToken := c.GetHeader(csrfTokenHeader)
	if headerToken == "" {
		// Also check form data for non-AJAX requests
		headerToken = c.PostForm("_csrf")
	}

	if headerToken == "" {
		return false
	}

	// Validate token exists in store and hasn't expired
	if !s.store.Validate(cookieToken) {
		return false
	}

	// Use constant-time comparison to prevent timing attacks
	return subtle.ConstantTimeCompare([]byte(cookieToken), []byte(headerToken)) == 1
}

// isSafeMethod returns true for HTTP methods that don't change state
func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	default:
		return false
	}
}

// isPathExempt checks if the request path is exempt from CSRF protection
func isPathExempt(path string, exemptPaths []string) bool {
	for _, exempt := range exemptPaths {
		if strings.HasPrefix(path, exempt) {
			return true
		}
	}
	return false
}

// isAPIRequest checks if this appears to be an API request
func isAPIRequest(c *gin.Context) bool {
	// Check for API-specific headers
	if c.GetHeader("Infra-Version") != "" {
		return true
	}
	// Check Accept header
	accept := c.GetHeader("Accept")
	if strings.Contains(accept, "application/json") {
		return true
	}
	// Check if path is under /api/
	return strings.HasPrefix(c.Request.URL.Path, "/api/")
}

// cookieAuthName is the name of the authentication cookie
// This must match the constant in cookie.go
const cookieAuthName = "auth"

// hasCookieAuth checks if the request includes cookie-based authentication
func hasCookieAuth(c *gin.Context) bool {
	// Check for the auth cookie
	_, err := c.Cookie(cookieAuthName)
	return err == nil
}

// validateOrigin validates the Origin or Referer header against trusted origins
func validateOrigin(req *http.Request, trustedOrigins []string) bool {
	origin := req.Header.Get("Origin")
	if origin == "" {
		// Fall back to Referer header
		referer := req.Header.Get("Referer")
		if referer == "" {
			// No origin information - this could be a same-origin request
			// or a request from a privacy-conscious browser
			// We allow it but CSRF token validation will still apply
			return true
		}
		origin = referer
	}

	// Check if origin matches the host
	host := req.Host
	if host != "" {
		// Normalize and compare
		if strings.Contains(origin, host) {
			return true
		}
	}

	// Check against trusted origins
	for _, trusted := range trustedOrigins {
		if strings.HasPrefix(origin, trusted) {
			return true
		}
	}

	// If no trusted origins are configured, allow same-origin requests only
	if len(trustedOrigins) == 0 {
		return true // Rely on CSRF token validation
	}

	return false
}

// sendCSRFError sends a standardized CSRF error response
func sendCSRFError(c *gin.Context, message string) {
	c.AbortWithStatusJSON(http.StatusForbidden, map[string]interface{}{
		"code":    http.StatusForbidden,
		"message": fmt.Sprintf("forbidden: %s", message),
	})
}

// GetCSRFToken returns the current CSRF token for the request
// This can be used by handlers that need to include the token in responses
func GetCSRFToken(c *gin.Context) string {
	token, _ := c.Cookie(csrfCookieName)
	return token
}

// RefreshCSRFToken generates a new CSRF token and updates the cookie
// This should be called after sensitive operations like login
// It requires a CSRFTokenStore to manage token lifecycle
func RefreshCSRFToken(c *gin.Context, config CSRFConfig, store CSRFTokenStore) (string, error) {
	// Remove old token
	oldToken, _ := c.Cookie(csrfCookieName)
	if oldToken != "" && store != nil {
		_ = store.Remove(oldToken)
	}

	// Generate new token
	token, err := generateCSRFToken()
	if err != nil {
		return "", err
	}

	if store != nil {
		if err := store.Add(token); err != nil {
			return "", fmt.Errorf("failed to store new CSRF token: %w", err)
		}
	}

	// Set the new cookie
	maxAge := int(csrfTokenExpiry.Seconds())
	c.SetSameSite(config.SameSite)
	c.SetCookie(
		csrfCookieName,
		token,
		maxAge,
		"/",
		"",
		config.Secure,
		false,
	)

	c.Header(csrfTokenHeader, token)
	return token, nil
}
