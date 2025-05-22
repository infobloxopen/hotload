package logger

import (
	"fmt"
	"testing"
)

// This is initialized to DefaultLogger at init-time!
var errlogger Logger = GetErrLogger()

func TestErrLogger(t *testing.T) {
	logCount := 0
	logOutput := ""
	WithErrLogger(func(args ...any) {
		logCount = logCount + 1
		logOutput = fmt.Sprintln(args...)
	})

	// Call package errlogger here,
	// which is initialized to DefaultLogger at init-time!
	errlogger("value of pi", 3.14159)
	if logCount == 0 {
		t.Logf("verified logCount was not updated because replacement errlogger was not called")
	} else {
		t.Errorf("logCount should be 0, but logCount=%d", logCount)
	}
	expOutput := ""
	if logOutput == "" {
		t.Logf("verified logOutput was not updated because replacement errlogger was not called")
	} else {
		t.Errorf("logOutput should be \"%s\", but logOutput=\"%s\"", expOutput, logOutput)
	}

	// Declare local errlogger var that hides package errlogger,
	// and initialize with current ErrLogger.
	errlogger := GetErrLogger()
	errlogger("value of pi", 3.14159)
	if logCount != 1 {
		t.Errorf("logCount should be 1, logCount=%d", logCount)
	}
	expOutput = "value of pi 3.14159\n"
	if logOutput != expOutput {
		t.Errorf("logOutput should be \"%s\", but logOutput=\"%s\"", expOutput, logOutput)
	}

	ErrLogf("mylogmsgprefix:", "cube(%d)=%d", 3, (3 * 3 * 3))
	if logCount != 2 {
		t.Errorf("logCount should be 2, logCount=%d", logCount)
	}
	expOutput = "mylogmsgprefix: cube(3)=27\n"
	if logOutput != expOutput {
		t.Errorf("logOutput should be \"%s\", but logOutput=\"%s\"", expOutput, logOutput)
	}
}
