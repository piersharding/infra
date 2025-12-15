package server

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"github.com/infrahq/infra/internal/logging"
	"github.com/infrahq/infra/uid"
)

type logSampler struct {
	fn       func() zerolog.Sampler
	samplers sync.Map
}

func newLogSampler(fn func() zerolog.Sampler) *logSampler {
	return &logSampler{fn: fn}
}

func (c *logSampler) Get(fields ...string) zerolog.Sampler {
	key := strings.Join(fields, "-")
	raw, ok := c.samplers.Load(key)
	if !ok {
		// Only use LoadOrStore on a failed load, to avoid creating unnecessary samplers
		raw, _ = c.samplers.LoadOrStore(key, c.fn())
	}

	return raw.(zerolog.Sampler) // nolint:forcetypeassert
}

func loggingMiddleware(enableSampling bool) gin.HandlerFunc {
	sampler := newLogSampler(func() zerolog.Sampler {
		return &zerolog.BurstSampler{
			Burst:  1,
			Period: 7 * time.Second,
		}
	})

	return func(c *gin.Context) {
		begin := time.Now()
		c.Next()

		method := c.Request.Method
		status := c.Writer.Status()
		logger := logging.L.Logger

		// sample logs for successful GET request if the log level is INFO or above
		if enableSampling && status < 400 && method == http.MethodGet && zerolog.GlobalLevel() >= zerolog.InfoLevel {
			logger = logger.Sample(sampler.Get(c.Request.Method, c.FullPath()))
		}

		// Sanitize sensitive information from user agent
		userAgent := sanitizeLogMessage(c.Request.UserAgent())

		event := logger.Info().
			Str("method", method).
			Str("path", c.Request.URL.Path).
			Str("localAddr", c.Request.Host).
			Str("remoteAddr", c.ClientIP()).
			Str("userAgent", userAgent)

		if c.Request.ContentLength > 0 {
			event = event.Int64("contentLength", c.Request.ContentLength)
		}

		rCtx := getRequestContext(c)
		if user := rCtx.Authenticated.User; user != nil {
			// Sanitize user information in logs
			event = event.Str("userID", sanitizeUserIDForLogging(user.ID))
		} else if rCtx.Response != nil && rCtx.Response.LoginUserID != 0 {
			event = event.Str("userID", sanitizeUserIDForLogging(rCtx.Response.LoginUserID))
		}

		if org := rCtx.Authenticated.Organization; org != nil {
			event = event.Str("orgID", sanitizeOrgIDForLogging(org.ID))
		} else if rCtx.Response != nil && rCtx.Response.SignupOrgID != 0 {
			event = event.Str("orgID", sanitizeOrgIDForLogging(rCtx.Response.SignupOrgID))
		}

		rCtx.Response.ApplyLogFields(event)

		event.Dur("elapsed", time.Since(begin)).
			Int("statusCode", status).
			Int("size", c.Writer.Size()).
			Msg("API request completed")
	}
}

// Helper functions for sanitizing IDs in logs
func sanitizeUserIDForLogging(userID uid.ID) string {
	if userID == 0 {
		return "unknown"
	}
	idStr := userID.String()
	if len(idStr) > 8 {
		idStr = idStr[:8]
	}
	return fmt.Sprintf("user_%s", idStr)
}

func sanitizeOrgIDForLogging(orgID uid.ID) string {
	if orgID == 0 {
		return "unknown"
	}
	idStr := orgID.String()
	if len(idStr) > 8 {
		idStr = idStr[:8]
	}
	return fmt.Sprintf("org_%s", idStr)
}
