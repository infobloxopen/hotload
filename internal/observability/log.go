package observability

import (
	"github.com/infobloxopen/hotload/logger"
)

// Logger wraps the hotload logger.
type Logger struct {
	log        logger.Logger
	prefix     string
	withPrefix func(string) string
}

// NewLogger creates a new observability logger.
func NewLogger(prefix string, log logger.Logger) *Logger {
	if log == nil {
		log = logger.GetLogger()
	}
	l := &Logger{
		log:    log,
		prefix: prefix,
	}

	// Set the withPrefix function based on whether prefix is set
	if prefix == "" {
		l.withPrefix = func(format string) string { return format }
	} else {
		l.withPrefix = func(format string) string { return prefix + ": " + format }
	}

	return l
}

// Logf logs a message.
func (l *Logger) Logf(format string, args ...interface{}) {
	logger.Logf("", l.withPrefix(format), args...)
}

// ErrLogf logs an error message.
func (l *Logger) ErrLogf(format string, args ...interface{}) {
	logger.ErrLogf("", l.withPrefix(format), args...)
}
