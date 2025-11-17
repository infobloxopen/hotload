package clock

import "time"

// Clock provides a testable abstraction over time operations.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
	Sleep(d time.Duration)
	NewTimer(d time.Duration) Timer
}

// Timer is a testable timer abstraction.
type Timer interface {
	C() <-chan time.Time
	Stop() bool
	Reset(d time.Duration) bool
}

// Real is a Clock implementation using actual time functions.
type Real struct{}

func (Real) Now() time.Time {
	return time.Now()
}

func (Real) After(d time.Duration) <-chan time.Time {
	return time.After(d)
}

func (Real) Sleep(d time.Duration) {
	time.Sleep(d)
}

func (Real) NewTimer(d time.Duration) Timer {
	return &realTimer{Timer: time.NewTimer(d)}
}

type realTimer struct {
	*time.Timer
}

func (t *realTimer) C() <-chan time.Time {
	return t.Timer.C
}
