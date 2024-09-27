package logger

// Logger defines the interface for logging.
type Logger func(...interface{})

// DefaultLogger is a no-op logger function that does nothing
var DefaultLogger Logger = func(args ...interface{}) {}
