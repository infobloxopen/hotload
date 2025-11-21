//go:build go1.21

package observability

import (
	"log/slog"
	"sync"
)

var (
	mu         sync.RWMutex
	logFunc    func(format string, args ...interface{})
	errLogFunc func(format string, args ...interface{})
)

func SetLogger(fn func(format string, args ...interface{})) {
	mu.Lock()
	defer mu.Unlock()
	logFunc = fn
}

func SetErrorLogger(fn func(format string, args ...interface{})) {
	mu.Lock()
	defer mu.Unlock()
	errLogFunc = fn
}

func Logf(format string, args ...interface{}) {
	mu.RLock()
	fn := logFunc
	mu.RUnlock()

	if fn != nil {
		fn(format, args...)
	}
}

func ErrLogf(format string, args ...interface{}) {
	mu.RLock()
	fn := errLogFunc
	mu.RUnlock()

	if fn != nil {
		fn(format, args...)
	}
}

func GetLoggerFunc() func(format string, args ...interface{}) {
	return func(format string, args ...interface{}) {
		Logf(format, args...)
	}
}

func DefaultLogger() func(format string, args ...interface{}) {
	return func(format string, args ...interface{}) {
		slog.Info(format, args...)
	}
}
