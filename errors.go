package hotload

import "errors"

var (
	// ErrUnsupportedStrategy is returned when the DSN names a strategy that
	// has not been registered with RegisterStrategy.
	ErrUnsupportedStrategy = errors.New("unsupported hotload strategy")

	// ErrMalformedConnectionString is returned when the hotload DSN cannot
	// be parsed.
	ErrMalformedConnectionString = errors.New("malformed hotload connection string")

	// ErrUnknownDriver is returned when the DSN names an underlying driver
	// that has not been registered with RegisterSQLDriver.
	ErrUnknownDriver = errors.New("target driver is not registered with hotload")

	// ErrHotSwap is the cancellation cause used when hotload retires a
	// connection because the connection string changed. Callers can detect
	// it with context.Cause and errors.Is.
	ErrHotSwap = errors.New("hotload: connection retired by config change")
)
