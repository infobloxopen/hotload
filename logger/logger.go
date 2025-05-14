package logger

import ()

// Logger defines the interface for logging.
// Deprecated: Use LevelLogger instead (internally all hotload logging now uses LevelLogger)
type Logger func(...interface{})

// DefaultLogger is a no-op logger function that does nothing
// Deprecated: Use LevelLogger instead (internally all hotload logging now uses LevelLogger)
var DefaultLogger Logger = func(args ...interface{}) {}

// stdLogger is the standard global logger
var stdLogger Logger = DefaultLogger

// WithLogger sets the standard logger
// Deprecated: Use LevelLogger instead (internally all hotload logging now uses LevelLogger)
func WithLogger(l Logger) {
	stdLogger = l
	if stdLogger == nil {
		stdLogger = DefaultLogger
	}
	SetDefaultLevelLogger(NewV1LevelLogger(stdLogger))
}

// GetLogger gets the standard logger
// Deprecated: Use LevelLogger instead (internally all hotload logging now uses LevelLogger)
func GetLogger() Logger {
	if stdLogger == nil {
		return DefaultLogger
	}
	return stdLogger
}
