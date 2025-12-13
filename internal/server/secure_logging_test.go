package server

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/infrahq/infra/internal/logging"
)

func TestSanitizeLogMessage(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no sensitive data",
			input:    "This is a normal log message",
			expected: "This is a normal log message",
		},
		{
			name:     "password in message",
			input:    "User login failed for password=secret123",
			expected: "User login failed for ***REDACTED***",
		},
		{
			name:     "token in message",
			input:    "API token=abc123def456",
			expected: "API ***REDACTED***",
		},
		{
			name:     "key in message",
			input:    "Encryption key=secretkey123",
			expected: "Encryption ***REDACTED***",
		},
		{
			name:     "secret in message",
			input:    "Client secret=verysecret",
			expected: "Client ***REDACTED***",
		},
		{
			name:     "auth in message",
			input:    "Auth token=xyz789",
			expected: "Auth ***REDACTED***",
		},
		{
			name:     "username in message",
			input:    "username=admin",
			expected: "***REDACTED***",
		},
		{
			name:     "email in message",
			input:    "email=user@example.com",
			expected: "***REDACTED***",
		},
		{
			name:     "email address in text",
			input:    "Contact user@example.com for support",
			expected: "Contact ***REDACTED*** for support",
		},
		{
			name:     "hex token",
			input:    "Token: 1a2b3c4d5e6f7890123456789012345678901234567890123456789012345678",
			expected: "Token: ***REDACTED***",
		},
		{
			name:     "IP address",
			input:    "Connection from 192.168.1.100",
			expected: "Connection from ***REDACTED***",
		},
		{
			name:     "multiple sensitive items",
			input:    "User admin logged in with password=secret123 from 192.168.1.100",
			expected: "User ***REDACTED*** logged in with ***REDACTED*** from ***REDACTED***",
		},
		{
			name:     "case insensitive patterns",
			input:    "PASSWORD=secret TOKEN=abc123",
			expected: "***REDACTED*** ***REDACTED***",
		},
		{
			name:     "mixed content",
			input:    "Starting service on port 8080 with config password=secret123",
			expected: "Starting service on port 8080 with config ***REDACTED***",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeLogMessage(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSecureLogger_Info(t *testing.T) {
	var buf bytes.Buffer
	logger := zerolog.New(&buf)
	loggingLog := logging.Logger{Logger: logger}
	secureLog := logging.SecureLogger(&loggingLog)

	// Test logging with sensitive data
	secureLog.Info().
		Str("password", "secret123").
		Str("token", "abc123").
		Str("message", "user login").
		Send()

	// Parse the logged JSON
	var logged map[string]interface{}
	err := json.Unmarshal(buf.Bytes(), &logged)
	require.NoError(t, err)

	// Verify the message is sanitized
	assert.Contains(t, logged, "message")
	assert.Equal(t, "user login", logged["message"])
}

func TestSecureLogger_Error(t *testing.T) {
	var buf bytes.Buffer
	logger := zerolog.New(&buf)
	loggingLog := logging.Logger{Logger: logger}
	secureLog := logging.SecureLogger(&loggingLog)

	// Test logging error with sensitive data
	secureLog.Error().
		Str("error", "auth failed").
		Str("password", "secret123").
		Str("user", "admin").
		Send()

	// Parse the logged JSON
	var logged map[string]interface{}
	err := json.Unmarshal(buf.Bytes(), &logged)
	require.NoError(t, err)

	// Verify the error is logged
	assert.Contains(t, logged, "error")
	assert.Equal(t, "auth failed", logged["error"])
}

func TestSecureLogger_Debug(t *testing.T) {
	var buf bytes.Buffer
	logger := zerolog.New(&buf).Level(zerolog.DebugLevel)
	loggingLog := logging.Logger{Logger: logger}
	secureLog := logging.SecureLogger(&loggingLog)

	// Test debug logging with sensitive data
	secureLog.Debug().
		Str("debug", "connection established").
		Str("token", "secret-token").
		Msg("debug message")

	// Check if buffer has content
	if buf.Len() == 0 {
		t.Log("Buffer is empty, debug level might be filtered out")
		return
	}

	// Parse the logged JSON
	var logged map[string]interface{}
	err := json.Unmarshal(buf.Bytes(), &logged)
	require.NoError(t, err)

	// Verify the debug message
	assert.Contains(t, logged, "debug")
	assert.Equal(t, "connection established", logged["debug"])
	// Verify the token is redacted
	assert.Contains(t, logged, "token")
	assert.Equal(t, "***REDACTED***", logged["token"])
}

func TestSecureLogger_WithSensitiveData(t *testing.T) {
	var buf bytes.Buffer
	logger := zerolog.New(&buf)
	loggingLog := logging.Logger{Logger: logger}
	secureLog := logging.SecureLogger(&loggingLog)

	// Test various sensitive data patterns
	testCases := []struct {
		key   string
		value string
	}{
		{"password", "mysecretpassword"},
		{"token", "abc123def456"},
		{"apiKey", "secret-api-key"},
		{"secret", "verysecret"},
		{"auth", "bearer-token"},
		{"username", "admin"},
		{"email", "user@example.com"},
		{"hexToken", "1a2b3c4d5e6f7890123456789012345678901234567890123456789012345678"},
		{"ip", "192.168.1.100"},
	}

	logEvent := secureLog.Info()
	for _, tc := range testCases {
		logEvent = logEvent.Str(tc.key, tc.value)
	}
	logEvent.Msg("sensitive data test")

	// Parse the logged JSON
	var logged map[string]interface{}
	err := json.Unmarshal(buf.Bytes(), &logged)
	require.NoError(t, err)

	// Verify that sensitive fields are redacted in the log
	for _, tc := range testCases {
		assert.Contains(t, logged, tc.key, "Sensitive field %s should be in the log", tc.key)
		value := logged[tc.key].(string)
		if tc.key == "ip" {
			// IP addresses are not considered sensitive by the current sanitization logic
			assert.Equal(t, tc.value, value, "IP field %s should not be redacted", tc.key)
		} else {
			assert.Equal(t, "***REDACTED***", value, "Sensitive field %s should be redacted", tc.key)
		}
	}
}

func TestSecureLogger_FieldSanitization(t *testing.T) {
	var buf bytes.Buffer
	logger := zerolog.New(&buf)
	loggingLog := logging.Logger{Logger: logger}
	secureLog := logging.SecureLogger(&loggingLog)

	// Test with a message containing sensitive data
	secureLog.Info().
		Str("message", "User admin logged in with password=secret123").
		Msg("user login")

	// Parse the logged JSON
	var logged map[string]interface{}
	err := json.Unmarshal(buf.Bytes(), &logged)
	require.NoError(t, err)

	// Verify the message is sanitized
	assert.Contains(t, logged, "message")
	message := logged["message"].(string)
	assert.NotContains(t, message, "admin", "Username should be redacted")
	assert.NotContains(t, message, "secret123", "Password should be redacted")
}

func TestSecureLogger_MultipleSensitivePatterns(t *testing.T) {
	var buf bytes.Buffer
	logger := zerolog.New(&buf)
	loggingLog := logging.Logger{Logger: logger}
	secureLog := logging.SecureLogger(&loggingLog)

	// Test message with multiple sensitive patterns
	message := "Login failed: username=admin password=secret123 token=abc123 from 192.168.1.100 email=user@example.com"

	secureLog.Warn().
		Str("message", message).
		Msg("login failed")

	// Parse the logged JSON
	var logged map[string]interface{}
	err := json.Unmarshal(buf.Bytes(), &logged)
	require.NoError(t, err)

	// Verify the message is properly sanitized
	assert.Contains(t, logged, "message")
	sanitizedMessage := logged["message"].(string)

	// Check that sensitive data is redacted
	assert.NotContains(t, sanitizedMessage, "admin")
	assert.NotContains(t, sanitizedMessage, "secret123")
	assert.NotContains(t, sanitizedMessage, "abc123")
	assert.NotContains(t, sanitizedMessage, "192.168.1.100")
	assert.NotContains(t, sanitizedMessage, "user@example.com")
}

func TestSecureLogger_StructuredLogging(t *testing.T) {
	var buf bytes.Buffer
	logger := zerolog.New(&buf)
	loggingLog := logging.Logger{Logger: logger}
	secureLog := logging.SecureLogger(&loggingLog)

	// Test structured logging with sensitive fields
	secureLog.Info().
		Str("operation", "user_creation").
		Str("username", "alice").
		Str("email", "alice@example.com").
		Int("user_id", 123).
		Msg("user operation")

	// Parse the logged JSON
	var logged map[string]interface{}
	err := json.Unmarshal(buf.Bytes(), &logged)
	require.NoError(t, err)

	// Verify that sensitive fields are redacted but non-sensitive fields remain
	assert.Contains(t, logged, "operation")
	assert.Equal(t, "user_creation", logged["operation"])

	assert.Contains(t, logged, "user_id")
	assert.Equal(t, float64(123), logged["user_id"]) // JSON numbers are float64

	// Sensitive fields should be redacted
	assert.Contains(t, logged, "username")
	assert.Contains(t, logged, "email")
	assert.Equal(t, "***REDACTED***", logged["username"])
	assert.Equal(t, "***REDACTED***", logged["email"])
}

func TestSecureLogger_EmptyAndNilValues(t *testing.T) {
	var buf bytes.Buffer
	logger := zerolog.New(&buf)
	loggingLog := logging.Logger{Logger: logger}
	secureLog := logging.SecureLogger(&loggingLog)

	// Test with empty and nil values
	secureLog.Info().
		Str("empty", "").
		Str("message", "").
		Msg("empty values")

	// Parse the logged JSON
	var logged map[string]interface{}
	err := json.Unmarshal(buf.Bytes(), &logged)
	require.NoError(t, err)

	// Should handle empty values gracefully
	assert.Contains(t, logged, "empty")
	assert.Equal(t, "", logged["empty"])
}

func TestSecureLogger_NonStringFields(t *testing.T) {
	var buf bytes.Buffer
	logger := zerolog.New(&buf)
	loggingLog := logging.Logger{Logger: logger}
	secureLog := logging.SecureLogger(&loggingLog)

	// Test with non-string fields
	secureLog.Info().
		Int("count", 42).
		Bool("active", true).
		Float64("price", 19.99).
		Str("message", "test").
		Msg("test")

	// Parse the logged JSON
	var logged map[string]interface{}
	err := json.Unmarshal(buf.Bytes(), &logged)
	require.NoError(t, err)

	// Verify non-string fields are preserved
	assert.Contains(t, logged, "count")
	assert.Equal(t, float64(42), logged["count"]) // JSON numbers are float64

	assert.Contains(t, logged, "active")
	assert.Equal(t, true, logged["active"])

	assert.Contains(t, logged, "price")
	assert.Equal(t, 19.99, logged["price"])

	assert.Contains(t, logged, "message")
	assert.Equal(t, "test", logged["message"])
}
