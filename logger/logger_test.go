package logger

import (
	"fmt"
	"testing"
)

// This is initialized to DefaultLogger at init-time!
var logger Logger = GetLogger()

func TestLogger(t *testing.T) {
	logCount := 0
	WithLogger(func(args ...any) {
		logCount = logCount + 1
		fmt.Println(args...)
	})

	// Call package logger here,
	// which is initialized to DefaultLogger at init-time!
	logger("value of pi", 3.14159)
	if logCount == 0 {
		t.Logf("verified logCount was not updated because replacement logger was not called")
	} else {
		t.Errorf("logCount should be 0, logCount=%d", logCount)
	}

	// Declare local logger var that hides package logger,
	// and initialize with current Logger.
	logger := GetLogger()
	logger("value of pi", 3.14159)
	if logCount != 1 {
		t.Errorf("logCount should be 1, logCount=%d", logCount)
	}
}
