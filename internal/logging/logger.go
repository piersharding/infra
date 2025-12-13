// Package logging provides a shared logger and log utilities to be used in all internal packages.
package logging

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"golang.org/x/term"
	"gopkg.in/natefinch/lumberjack.v2"
)

var sensitivePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(password|token|key|secret|auth)\s*[:=]\s*[^&\s]+`),
	regexp.MustCompile(`(?i)(username|email)\s*[:=]\s*[^&\s]+`),
}

// secureLogger wraps zerolog.Logger to provide secure logging functionality
type secureLogger struct {
	*zerolog.Logger
}

func sanitizeLogMessage(msg string) string {
	for _, pattern := range sensitivePatterns {
		msg = pattern.ReplaceAllString(msg, "***REDACTED***")
	}
	return msg
}

func isSensitiveField(field string) bool {
	sensitiveFields := []string{"password", "token", "secret", "key", "username", "email", "auth", "credential"}
	for _, sf := range sensitiveFields {
		if strings.Contains(strings.ToLower(field), sf) {
			return true
		}
	}
	return false
}

// secureEvent wraps zerolog.Event to provide secure logging functionality
type secureEvent struct {
	*zerolog.Event
}

func (e secureEvent) Str(k string, v string) *secureEvent {
	if isSensitiveField(k) {
		return &secureEvent{e.Event.Str(k, "***REDACTED***")}
	}
	return &secureEvent{e.Event.Str(k, v)}
}

func (e secureEvent) Int(k string, v int) *secureEvent {
	return &secureEvent{e.Event.Int(k, v)}
}

func (e secureEvent) Bool(k string, v bool) *secureEvent {
	return &secureEvent{e.Event.Bool(k, v)}
}

func (e secureEvent) Float64(k string, v float64) *secureEvent {
	return &secureEvent{e.Event.Float64(k, v)}
}

func (e secureEvent) Msg(msg string) {
	sanitizedMsg := sanitizeLogMessage(msg)
	e.Event.Msg(sanitizedMsg)
}

func sanitizeEvent(event *zerolog.Event) *secureEvent {
	return &secureEvent{event}
}

func (l secureLogger) SecureInfo(msg string) {
	sanitizedMsg := sanitizeLogMessage(msg)
	l.Logger.Info().Msg(sanitizedMsg)
}

func (l secureLogger) SecureError(msg string, err error) {
	sanitizedMsg := sanitizeLogMessage(msg)
	if err != nil {
		l.Logger.Error().Err(err).Msg(sanitizedMsg)
	} else {
		l.Logger.Error().Msg(sanitizedMsg)
	}
}

func (l secureLogger) SecureWarn(msg string, data map[string]interface{}) {
	sanitizedMsg := sanitizeLogMessage(msg)
	safeFields := make(map[string]interface{})
	for k, v := range data {
		if !isSensitiveField(k) {
			safeFields[k] = v
		}
	}
	l.Logger.Warn().Fields(safeFields).Msg(sanitizedMsg)
}

func (l secureLogger) SecureWarnError(msg string, err error) {
	sanitizedMsg := sanitizeLogMessage(msg)
	if err != nil {
		l.Logger.Warn().Err(err).Msg(sanitizedMsg)
	} else {
		l.Logger.Warn().Msg(sanitizedMsg)
	}
}

func (l secureLogger) SecureTrace(msg string) {
	sanitizedMsg := sanitizeLogMessage(msg)
	l.Logger.Trace().Msg(sanitizedMsg)
}

func (l secureLogger) SecureDebug(msg string) {
	sanitizedMsg := sanitizeLogMessage(msg)
	l.Logger.Debug().Msg(sanitizedMsg)
}

func (l secureLogger) Info() *secureEvent {
	return sanitizeEvent(l.Logger.Info())
}

func (l secureLogger) Error() *secureEvent {
	return sanitizeEvent(l.Logger.Error())
}

func (l secureLogger) Debug() *secureEvent {
	return sanitizeEvent(l.Logger.Debug())
}

func (l secureLogger) Warn() *secureEvent {
	return sanitizeEvent(l.Logger.Warn())
}

// SecureLogger creates a secure logger wrapper
func SecureLogger(logger *Logger) secureLogger {
	return secureLogger{Logger: &logger.Logger}
}

var L = &Logger{
	Logger: zerolog.New(zerolog.ConsoleWriter{
		Out:          os.Stderr,
		NoColor:      !isTerminal(),
		PartsExclude: []string{"time"},
		FormatLevel:  consoleFormatLevel,
	}),
}

type Logger struct {
	zerolog.Logger
}

func init() {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnixMs
	zerolog.CallerMarshalFunc = func(pc uintptr, file string, line int) string {
		short := filepath.Join(filepath.Base(filepath.Dir(file)), filepath.Base(file))
		return fmt.Sprintf("%s:%d", short, line)
	}
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
}

func newLogger(writer io.Writer) *Logger {
	return &Logger{
		Logger: zerolog.New(writer).With().Timestamp().Caller().Logger(),
	}
}

// UseServerLogger changes L to a logger appropriate for long-running processes,
// like the infra server and connector. If the process is being run in an
// interactive terminal, use the default console logger.
func UseServerLogger() {
	if isTerminal() {
		return
	}
	L = newLogger(os.Stderr)
}

func isTerminal() bool {
	return os.Stdin != nil && term.IsTerminal(int(os.Stdin.Fd()))
}

// UseFileLogger changes L to a logger that writes log output to a file that is
// rotated.
func UseFileLogger(filepath string) {
	zerolog.TimeFieldFormat = time.RFC3339
	writer := &lumberjack.Logger{
		Filename:   filepath,
		MaxSize:    10, // megabytes
		MaxBackups: 7,
		MaxAge:     28, // days
	}

	L = newLogger(writer)
}

type logWriter struct {
	logger interface {
		WithLevel(level zerolog.Level) *zerolog.Event
	}
	level zerolog.Level
}

func (w logWriter) Write(p []byte) (n int, err error) {
	n = len(p)
	p = trimTrailingNewline(p)
	w.logger.WithLevel(w.level).CallerSkipFrame(1).Msg(string(p))
	return n, nil
}

func trimTrailingNewline(p []byte) []byte {
	n := len(p)
	if n > 0 && p[n-1] == '\n' {
		// Trim CR added by stdlog.
		p = p[0 : n-1]
	}
	return p
}

// HTTPErrorLog returns a stdlib log.Logger configured to write logs at
// level to L. The intended use of this logger is for http.Server.ErrorLog.
func HTTPErrorLog(level zerolog.Level) *log.Logger {
	return log.New(logWriter{logger: L, level: level}, "", 0)
}

func Debugf(format string, v ...interface{}) {
	L.Debug().CallerSkipFrame(1).Msgf(format, v...)
}

func Infof(format string, v ...interface{}) {
	L.Info().CallerSkipFrame(1).Msgf(format, v...)
}

func Warnf(format string, v ...interface{}) {
	L.Warn().CallerSkipFrame(1).Msgf(format, v...)
}

func Errorf(format string, v ...interface{}) {
	L.Error().CallerSkipFrame(1).Msgf(format, v...)
}

func SetLevel(levelName string) error {
	level, err := zerolog.ParseLevel(levelName)
	if err != nil {
		return err
	}

	// logging middleware depends on this level. If we stop using the global level
	// make sure to adjust the logging middleware to check the level of the
	// logger instead of the global level.
	zerolog.SetGlobalLevel(level)
	return nil
}

type TestingT interface {
	Cleanup(func())
}

// PatchLogger sets the global L logger to write logs to t. When the test ends
// the global L logger is reset to the previous value.
// PatchLogger changes a static variable, so tests that use PatchLogger can not
// use t.Parallel.
func PatchLogger(t TestingT, writer io.Writer) {
	origL := L
	L = newLogger(writer)
	t.Cleanup(func() {
		L = origL
	})
}
