package logger

import "fmt"

// Logger defines the interface for logging.
type Logger func(...interface{})

// DefaultLogger is a no-op logger function that does nothing
var DefaultLogger Logger = func(args ...interface{}) {}

// stdLogger is the standard global logger
var stdLogger Logger = DefaultLogger

// WithLogger sets the standard logger
func WithLogger(l Logger) {
	stdLogger = l
	if stdLogger == nil {
		stdLogger = DefaultLogger
	}
}

// GetLogger gets the standard logger
func GetLogger() Logger {
	if stdLogger == nil {
		return DefaultLogger
	}
	return stdLogger
}

func Logf(prefix, format string, args ...any) {
	loggr := GetLogger()
	logMsg := fmt.Sprintf(format, args...)
	loggr(prefix, logMsg)
}
