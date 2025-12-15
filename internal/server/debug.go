package server

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/pprof"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/infrahq/infra/api"
	"github.com/infrahq/infra/internal/access"
	"github.com/infrahq/infra/internal/logging"
	"github.com/infrahq/infra/internal/server/models"
)

// DebugConfig holds configuration for debug endpoints
type DebugConfig struct {
	// Enabled determines whether debug endpoints are registered
	Enabled bool

	// AllowInProduction allows debug endpoints even when production is detected
	// This should only be true in exceptional circumstances
	AllowInProduction bool

	// RateLimitPerMinute limits how many debug requests per minute are allowed
	// Default: 10
	RateLimitPerMinute int

	// AuditLog enables detailed audit logging of all debug endpoint access
	// Default: true
	AuditLog bool
}

// DefaultDebugConfig returns safe default configuration for debug endpoints
func DefaultDebugConfig() DebugConfig {
	return DebugConfig{
		Enabled:            false,
		AllowInProduction:  false,
		RateLimitPerMinute: 10,
		AuditLog:           true,
	}
}

// debugRateLimiter provides simple rate limiting for debug endpoints
type debugRateLimiter struct {
	mu          sync.Mutex
	requests    map[string][]time.Time
	maxRequests int
	window      time.Duration
}

func newDebugRateLimiter(maxRequestsPerMinute int) *debugRateLimiter {
	if maxRequestsPerMinute <= 0 {
		maxRequestsPerMinute = 10
	}
	return &debugRateLimiter{
		requests:    make(map[string][]time.Time),
		maxRequests: maxRequestsPerMinute,
		window:      time.Minute,
	}
}

func (r *debugRateLimiter) Allow(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	windowStart := now.Add(-r.window)

	// Clean old entries
	var validRequests []time.Time
	for _, t := range r.requests[key] {
		if t.After(windowStart) {
			validRequests = append(validRequests, t)
		}
	}

	if len(validRequests) >= r.maxRequests {
		return false
	}

	r.requests[key] = append(validRequests, now)
	return true
}

// Global rate limiter for debug endpoints
var debugLimiter *debugRateLimiter
var debugLimiterOnce sync.Once

func getDebugRateLimiter(config DebugConfig) *debugRateLimiter {
	debugLimiterOnce.Do(func() {
		debugLimiter = newDebugRateLimiter(config.RateLimitPerMinute)
	})
	return debugLimiter
}

// IsProductionEnvironment attempts to detect if the server is running in production
// This checks multiple signals to make a best-effort determination
func IsProductionEnvironment() bool {
	// Check common environment variables that indicate production
	productionIndicators := []string{
		"PRODUCTION",
		"PROD",
		"ENV",
		"ENVIRONMENT",
		"APP_ENV",
		"GO_ENV",
		"INFRA_ENV",
	}

	productionValues := []string{
		"production",
		"prod",
		"live",
	}

	for _, envVar := range productionIndicators {
		value := strings.ToLower(os.Getenv(envVar))
		for _, prodValue := range productionValues {
			if value == prodValue {
				return true
			}
		}
	}

	// Check if running in a known production orchestration environment
	// Kubernetes production namespaces often contain "prod"
	namespace := os.Getenv("POD_NAMESPACE")
	if namespace != "" && (strings.Contains(strings.ToLower(namespace), "prod") ||
		strings.Contains(strings.ToLower(namespace), "production")) {
		return true
	}

	// Check for Kubernetes service account (indicates container deployment)
	if _, err := os.Stat("/var/run/secrets/kubernetes.io/serviceaccount"); err == nil {
		// Running in Kubernetes - check if it looks like production
		hostname, _ := os.Hostname()
		if strings.Contains(strings.ToLower(hostname), "prod") {
			return true
		}
	}

	return false
}

// ValidateDebugConfig checks if debug configuration is safe
// Returns an error if debug is enabled in an unsafe configuration
func ValidateDebugConfig(config DebugConfig) error {
	if !config.Enabled {
		return nil
	}

	if IsProductionEnvironment() && !config.AllowInProduction {
		return fmt.Errorf("debug endpoints cannot be enabled in production environment; " +
			"set AllowInProduction=true to override (not recommended)")
	}

	return nil
}

var pprofRoute = route[pprofRequest, *api.EmptyResponse]{
	handler: pprofHandler,
	routeSettings: routeSettings{
		omitFromTelemetry:          true,
		omitFromDocs:               true,
		infraVersionHeaderOptional: true,
		txnOptions:                 &sql.TxOptions{ReadOnly: true},
	},
}

type pprofRequest struct{}

func (pprofRequest) IsBlockingRequest() bool {
	return true
}

// pprofHandler handles pprof debug endpoint requests with enhanced security
func pprofHandler(rCtx access.RequestContext, _ *pprofRequest) (*api.EmptyResponse, error) {
	// Verify authorization - requires InfraSupportAdminRole
	if err := access.IsAuthorized(rCtx, models.InfraSupportAdminRole); err != nil {
		logDebugAccess(rCtx, "unauthorized", "authorization failed")
		return nil, access.HandleAuthErr(err, "debug", "run", models.InfraSupportAdminRole)
	}

	// Get user info for rate limiting and audit
	userID := "unknown"
	if rCtx.Authenticated.User != nil {
		userID = rCtx.Authenticated.User.ID.String()
	}

	// Apply rate limiting
	limiter := getDebugRateLimiter(DefaultDebugConfig())
	if !limiter.Allow(userID) {
		logDebugAccess(rCtx, "rate_limited", "too many debug requests")
		return nil, fmt.Errorf("rate limit exceeded for debug endpoints; please wait before retrying")
	}

	// Audit log the access
	_, profile := path.Split(rCtx.Request.URL.Path)
	logDebugAccess(rCtx, "accessed", fmt.Sprintf("profile=%s", profile))

	// End the transaction before blocking
	if err := rCtx.DBTxn.Rollback(); err != nil {
		return nil, err
	}

	switch profile {
	case "trace":
		pprof.Trace(rCtx.Response.HTTPWriter, rCtx.Request)
	case "profile":
		pprof.Profile(rCtx.Response.HTTPWriter, rCtx.Request)
	default:
		// All other types of profiles are served from Index
		http.StripPrefix("/api", http.HandlerFunc(pprof.Index)).ServeHTTP(rCtx.Response.HTTPWriter, rCtx.Request)
	}
	return nil, nil
}

// logDebugAccess logs access to debug endpoints for security auditing
func logDebugAccess(rCtx access.RequestContext, action, details string) {
	logger := logging.L.Warn()

	// Add user information if available
	if rCtx.Authenticated.User != nil {
		logger = logger.Str("user_id", rCtx.Authenticated.User.ID.String())
		logger = logger.Str("user_name", rCtx.Authenticated.User.Name)
	}

	// Add organization if available
	if rCtx.Authenticated.Organization != nil {
		logger = logger.Str("org_id", rCtx.Authenticated.Organization.ID.String())
		logger = logger.Str("org_name", rCtx.Authenticated.Organization.Name)
	}

	// Add request information
	logger = logger.Str("path", rCtx.Request.URL.Path)
	logger = logger.Str("remote_addr", rCtx.Request.RemoteAddr)
	logger = logger.Str("action", action)

	if details != "" {
		logger = logger.Str("details", details)
	}

	logger.Msg("debug endpoint access")
}

// RegisterDebugRoutes registers debug routes if enabled and safe to do so
// Returns true if routes were registered, false otherwise
func RegisterDebugRoutes(a *API, authn *routeGroup, config DebugConfig) bool {
	if !config.Enabled {
		logging.L.Debug().Msg("debug endpoints disabled")
		return false
	}

	// Validate configuration
	if err := ValidateDebugConfig(config); err != nil {
		logging.L.Error().Err(err).Msg("debug endpoints not registered due to unsafe configuration")
		return false
	}

	// Register the route
	add(a, authn, http.MethodGet, "/api/debug/pprof/*profile", pprofRoute)

	// Log warning about debug endpoints being enabled
	if IsProductionEnvironment() {
		logging.L.Error().
			Bool("production_detected", true).
			Bool("allow_in_production", config.AllowInProduction).
			Msg("SECURITY WARNING: debug endpoints enabled in production environment")
	} else {
		logging.L.Warn().Msg("debug endpoints enabled - this should not be enabled in production")
	}

	return true
}
