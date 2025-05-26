package logger

import "fmt"

// Logger defines the interface for logging.
type Logger func(...interface{})

// DefaultLogger is a no-op logger function that does nothing
var DefaultLogger Logger = func(args ...interface{}) {}

// stdLogger is the global standard logger
var stdLogger Logger = DefaultLogger

// WithLogger sets the global standard logger
func WithLogger(l Logger) {
	stdLogger = l
	if stdLogger == nil {
		stdLogger = DefaultLogger
	}
}

// GetLogger gets the global standard logger
func GetLogger() Logger {
	if stdLogger == nil {
		return DefaultLogger
	}
	return stdLogger
}

// Logf formats and prints to global standard logger
func Logf(prefix, format string, args ...any) {
	loggr := GetLogger()
	logMsg := fmt.Sprintf(format, args...)
	loggr(prefix, logMsg)
}
