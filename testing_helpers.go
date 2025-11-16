package hotload

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Test helpers for converting from ginkgo/gomega to standard library testing

// assertEqual checks if two values are equal
func assertEqual(t *testing.T, expected, actual interface{}, msgAndArgs ...interface{}) {
	t.Helper()
	if !reflect.DeepEqual(expected, actual) {
		msg := fmt.Sprintf("Expected: %v\nActual: %v", expected, actual)
		if len(msgAndArgs) > 0 {
			msg = fmt.Sprintf(msgAndArgs[0].(string), msgAndArgs[1:]...) + "\n" + msg
		}
		t.Fatal(msg)
	}
}

// assertTrue checks if a condition is true
func assertTrue(t *testing.T, condition bool, msgAndArgs ...interface{}) {
	t.Helper()
	if !condition {
		msg := "Expected condition to be true"
		if len(msgAndArgs) > 0 {
			msg = fmt.Sprintf(msgAndArgs[0].(string), msgAndArgs[1:]...)
		}
		t.Fatal(msg)
	}
}

// assertFalse checks if a condition is false
func assertFalse(t *testing.T, condition bool, msgAndArgs ...interface{}) {
	t.Helper()
	if condition {
		msg := "Expected condition to be false"
		if len(msgAndArgs) > 0 {
			msg = fmt.Sprintf(msgAndArgs[0].(string), msgAndArgs[1:]...)
		}
		t.Fatal(msg)
	}
}

// assertNil checks if a value is nil
func assertNil(t *testing.T, value interface{}, msgAndArgs ...interface{}) {
	t.Helper()
	if value != nil && !reflect.ValueOf(value).IsNil() {
		msg := fmt.Sprintf("Expected nil but got: %v", value)
		if len(msgAndArgs) > 0 {
			msg = fmt.Sprintf(msgAndArgs[0].(string), msgAndArgs[1:]...) + "\n" + msg
		}
		t.Fatal(msg)
	}
}

// assertNotNil checks if a value is not nil
func assertNotNil(t *testing.T, value interface{}, msgAndArgs ...interface{}) {
	t.Helper()
	if value == nil || (reflect.ValueOf(value).Kind() == reflect.Ptr && reflect.ValueOf(value).IsNil()) {
		msg := "Expected non-nil value"
		if len(msgAndArgs) > 0 {
			msg = fmt.Sprintf(msgAndArgs[0].(string), msgAndArgs[1:]...)
		}
		t.Fatal(msg)
	}
}

// assertError checks if an error occurred
func assertError(t *testing.T, err error, msgAndArgs ...interface{}) {
	t.Helper()
	if err == nil {
		msg := "Expected an error but got nil"
		if len(msgAndArgs) > 0 {
			msg = fmt.Sprintf(msgAndArgs[0].(string), msgAndArgs[1:]...)
		}
		t.Fatal(msg)
	}
}

// assertNoError checks if no error occurred
func assertNoError(t *testing.T, err error, msgAndArgs ...interface{}) {
	t.Helper()
	if err != nil {
		msg := fmt.Sprintf("Expected no error but got: %v", err)
		if len(msgAndArgs) > 0 {
			msg = fmt.Sprintf(msgAndArgs[0].(string), msgAndArgs[1:]...) + "\n" + msg
		}
		t.Fatal(msg)
	}
}

// assertPanic checks if a function panics
func assertPanic(t *testing.T, f func(), msgAndArgs ...interface{}) {
	t.Helper()
	defer func() {
		if r := recover(); r == nil {
			msg := "Expected function to panic"
			if len(msgAndArgs) > 0 {
				msg = fmt.Sprintf(msgAndArgs[0].(string), msgAndArgs[1:]...)
			}
			t.Fatal(msg)
		}
	}()
	f()
}

// assertPanicWith checks if a function panics with a message containing the expected substring
func assertPanicWith(t *testing.T, expectedSubstring string, f func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			msg := fmt.Sprintf("%v", r)
			if !strings.Contains(msg, expectedSubstring) {
				t.Fatalf("Expected panic message to contain %q but got: %s", expectedSubstring, msg)
			}
		} else {
			t.Fatal("Expected function to panic")
		}
	}()
	f()
}

// assertErrorIs checks if an error is a specific error
func assertErrorIs(t *testing.T, err, target error) {
	t.Helper()
	if err != target {
		t.Fatalf("Expected error to be %v but got: %v", target, err)
	}
}

// assertContains checks if a string contains a substring
func assertContains(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("Expected %q to contain %q", haystack, needle)
	}
}

// assertSliceContains checks if a slice contains an element
func assertSliceContains(t *testing.T, slice interface{}, element interface{}) {
	t.Helper()
	v := reflect.ValueOf(slice)
	if v.Kind() != reflect.Slice {
		t.Fatal("Expected a slice")
	}
	for i := 0; i < v.Len(); i++ {
		if reflect.DeepEqual(v.Index(i).Interface(), element) {
			return
		}
	}
	t.Fatalf("Expected slice to contain %v", element)
}

// assertLessThanOrEqual checks if a number is less than or equal to another
func assertLessThanOrEqual(t *testing.T, actual, expected int, msgAndArgs ...interface{}) {
	t.Helper()
	if actual > expected {
		msg := fmt.Sprintf("Expected %d <= %d", actual, expected)
		if len(msgAndArgs) > 0 {
			msg = fmt.Sprintf(msgAndArgs[0].(string), msgAndArgs[1:]...)
		}
		t.Fatal(msg)
	}
}

// assertConsistently checks that a condition remains false for a duration
func assertConsistently(t *testing.T, check func() bool, duration time.Duration, msgAndArgs ...interface{}) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.After(duration)

	for {
		select {
		case <-ticker.C:
			if check() {
				msg := "Expected condition to remain false"
				if len(msgAndArgs) > 0 {
					msg = fmt.Sprintf(msgAndArgs[0].(string), msgAndArgs[1:]...)
				}
				t.Fatal(msg)
			}
		case <-timeout:
			return
		}
	}
}
