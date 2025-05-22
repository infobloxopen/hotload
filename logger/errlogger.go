package logger

import (
	"fmt"
	"log"
)

// errLogger is the global error logger
// (default is to print using standard go log pkg)
var errLogger Logger = DefaultErrLogger

// DefaultErrLogger is the default error logger which prints using standard go log pkg
func DefaultErrLogger(args ...any) {
	log.Println(args...)
}

// WithErrLogger sets the global error logger
func WithErrLogger(l Logger) {
	errLogger = l
	if errLogger == nil {
		errLogger = DefaultErrLogger
	}
}

// GetErrLogger gets the global error logger
func GetErrLogger() Logger {
	if errLogger == nil {
		return DefaultErrLogger
	}
	return errLogger
}

// ErrLogf formats and prints to global error logger
func ErrLogf(prefix, format string, args ...any) {
	loggr := GetErrLogger()
	logMsg := fmt.Sprintf(format, args...)
	loggr(prefix, logMsg)
}
