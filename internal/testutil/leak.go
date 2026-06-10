// Package testutil holds small stdlib-only helpers for hotload's tests.
package testutil

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

// NoLeaks registers a cleanup that fails the test if goroutines are still
// running once the test — including cleanups registered after this call —
// has finished. Call it first in the test, before opening any resources, so
// its cleanup runs last (cleanups run in LIFO order).
//
// Transient goroutines get a grace period: the check retries until the
// stragglers exit or a deadline passes.
func NoLeaks(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		var extra []string
		for {
			extra = leakedGoroutines()
			if len(extra) == 0 {
				return
			}
			if time.Now().After(deadline) {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Errorf("leaked %d goroutine(s):\n%s", len(extra), strings.Join(extra, "\n\n"))
	})
}

// allowedStackFragments mark goroutines that belong to the test harness or
// the runtime rather than the code under test.
var allowedStackFragments = []string{
	"testing.",       // test runner goroutines (tRunner, parallel subtests, …)
	"runtime.goexit", // never matches a frame list alone; kept for safety
	"os/signal.",
	"runtime/trace.",
	"runtime.ReadTrace",
}

func leakedGoroutines() []string {
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	stacks := strings.Split(strings.TrimSpace(string(buf[:n])), "\n\n")

	var out []string
	for i, s := range stacks {
		if i == 0 {
			// The goroutine running this check.
			continue
		}
		if isAllowed(s) {
			continue
		}
		out = append(out, s)
	}
	return out
}

func isAllowed(stack string) bool {
	for _, frag := range allowedStackFragments {
		if strings.Contains(stack, frag) {
			return true
		}
	}
	return false
}

// WaitFor polls cond every millisecond until it returns true or the timeout
// elapses, failing the test in the latter case.
func WaitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %v waiting for %s", timeout, what)
		}
		time.Sleep(time.Millisecond)
	}
}
