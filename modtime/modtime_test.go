package modtime

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	hotload "github.com/infobloxopen/hotload/v3"
	"github.com/infobloxopen/hotload/v3/internal"
)

const testStrategy = "fsnotify"

func mustParseRFC3339(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

// eventRecorder captures ModTimeEvents emitted through hotload hooks.
type eventRecorder struct {
	mu     sync.Mutex
	events []hotload.ModTimeEvent
}

func newEventRecorder(t *testing.T) *eventRecorder {
	t.Helper()
	r := &eventRecorder{}
	hotload.RegisterHooks(hotload.Hooks{
		OnModTimeCheck: func(ev hotload.ModTimeEvent) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.events = append(r.events, ev)
		},
	})
	return r
}

func (r *eventRecorder) forPath(path string) []hotload.ModTimeEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []hotload.ModTimeEvent
	for _, ev := range r.events {
		if ev.Path == path {
			out = append(out, ev)
		}
	}
	return out
}

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestAgainstUnixFS verifies the monitor works against the real filesystem.
func TestAgainstUnixFS(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mtm := NewModTimeMonitor(ctx, WithCheckInterval(20*time.Millisecond))
	pth := "/dev/null"
	if err := mtm.AddMonitoredPath(testStrategy, pth); err != nil {
		t.Fatal(err)
	}

	waitFor(t, 2*time.Second, "a modtime sample", func() bool {
		sts, err := mtm.GetPathStatus(testStrategy, pth)
		return err == nil && !sts.ModTime.IsZero()
	})
}

// TestAgainstMapFS drives the monitor with a mock filesystem and verifies
// path status updates and latency events emitted through hotload hooks.
func TestAgainstMapFS(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rec := newEventRecorder(t)

	pth := "/foo/bar"
	mfs := internal.NewSafeMapFS()
	start := mustParseRFC3339(t, "2026-01-02T03:04:05Z")
	if err := mfs.UpsertMapFile(pth, &fstest.MapFile{ModTime: start}); err != nil {
		t.Fatal(err)
	}

	mtm := NewModTimeMonitor(ctx, WithStatFS(mfs), WithCheckInterval(20*time.Millisecond))
	if err := mtm.AddMonitoredPath(testStrategy, pth); err != nil {
		t.Fatal(err)
	}
	if err := mtm.AddMonitoredPath(testStrategy, pth); err != ErrDuplicatePath {
		t.Errorf("second AddMonitoredPath err = %v, want ErrDuplicatePath", err)
	}

	waitFor(t, 2*time.Second, "first modtime sample", func() bool {
		sts, err := mtm.GetPathStatus(testStrategy, pth)
		return err == nil && sts.ModTime.Equal(start)
	})

	// Bump the file's modtime; the monitor must observe it and the latency
	// events must shrink accordingly.
	now := time.Now()
	if err := mfs.UpsertMapFile(pth, &fstest.MapFile{ModTime: now}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, "updated modtime sample", func() bool {
		sts, err := mtm.GetPathStatus(testStrategy, pth)
		return err == nil && sts.ModTime.Equal(now)
	})

	waitFor(t, 2*time.Second, "latency events", func() bool {
		return len(rec.forPath(pth)) >= 2
	})
	evs := rec.forPath(pth)
	first, last := evs[0], evs[len(evs)-1]
	if first.Strategy != testStrategy {
		t.Errorf("event strategy = %q, want %q", first.Strategy, testStrategy)
	}
	// The first sample's latency is measured against the old modtime (huge);
	// after the update the latency must be small.
	if first.Latency < 24*time.Hour {
		t.Errorf("first latency = %v, want large (old modtime)", first.Latency)
	}
	if last.Latency > time.Hour {
		t.Errorf("latency after modtime update = %v, want small", last.Latency)
	}

	if _, err := mtm.GetPathStatus(testStrategy, "/no/such/path"); err != ErrPathNotFound {
		t.Errorf("GetPathStatus(unknown) err = %v, want ErrPathNotFound", err)
	}
}

// TestConcurrency exercises concurrent AddMonitoredPath/GetPathStatus calls
// against a running monitor; meaningful under -race.
func TestConcurrency(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mfs := internal.NewSafeMapFS()
	mtm := NewModTimeMonitor(ctx, WithStatFS(mfs), WithCheckInterval(5*time.Millisecond))

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			pth := fmt.Sprintf("/concurrency/sub%d", i)
			if err := mfs.UpsertMapFile(pth, &fstest.MapFile{ModTime: time.Now()}); err != nil {
				t.Error(err)
				return
			}
			if err := mtm.AddMonitoredPath(testStrategy, pth); err != nil {
				t.Error(err)
				return
			}
			for j := 0; j < 50; j++ {
				if _, err := mtm.GetPathStatus(testStrategy, pth); err != nil {
					t.Error(err)
					return
				}
			}
		}(i)
	}
	wg.Wait()
}
