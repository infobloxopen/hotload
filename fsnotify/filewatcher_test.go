package fsnotify

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// newTestStrategy returns a strategy with a short resync period (the
// recovery path after failed re-watches) that is closed when the test ends.
func newTestStrategy(t *testing.T) *Strategy {
	t.Helper()
	s := NewStrategy()
	s.resyncPeriod = 50 * time.Millisecond
	t.Cleanup(s.Close)
	return s
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// awaitValue reads updates until want arrives, tolerating duplicate
// notifications (fsnotify can deliver several events per change).
func awaitValue(t *testing.T, ch <-chan string, want string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case got, ok := <-ch:
			if !ok {
				t.Fatalf("update channel closed while waiting for %q", want)
			}
			t.Logf("update: %q", got)
			if got == want {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for value %q", want)
		}
	}
}

// awaitClosed waits for the update channel to be closed.
func awaitClosed(t *testing.T, ch <-chan string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for update channel to close")
		}
	}
}

func TestWatchMissingFile(t *testing.T) {
	s := newTestStrategy(t)
	_, _, err := s.Watch(context.Background(), filepath.Join(t.TempDir(), "missing"), "")
	if err == nil {
		t.Fatal("expected an error watching a missing file")
	}
}

func TestWatchInitialValueTrimmed(t *testing.T) {
	s := newTestStrategy(t)
	p := filepath.Join(t.TempDir(), "config")
	writeFile(t, p, "  dsn-1 \n")

	value, _, err := s.Watch(context.Background(), p, "")
	if err != nil {
		t.Fatal(err)
	}
	if value != "dsn-1" {
		t.Errorf("initial value = %q, want %q (whitespace must be trimmed)", value, "dsn-1")
	}
}

func TestWatchSeesWrites(t *testing.T) {
	s := newTestStrategy(t)
	p := filepath.Join(t.TempDir(), "config")
	writeFile(t, p, "dsn-1")

	_, updates, err := s.Watch(context.Background(), p, "")
	if err != nil {
		t.Fatal(err)
	}

	writeFile(t, p, "dsn-2")
	awaitValue(t, updates, "dsn-2")

	writeFile(t, p, "dsn-3")
	awaitValue(t, updates, "dsn-3")
}

// TestWatchSeesAtomicRename covers the write-then-rename pattern used by
// most config writers (and by viper, kustomize, etc.).
func TestWatchSeesAtomicRename(t *testing.T) {
	s := newTestStrategy(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "config")
	writeFile(t, p, "dsn-1")

	_, updates, err := s.Watch(context.Background(), p, "")
	if err != nil {
		t.Fatal(err)
	}

	tmp := filepath.Join(dir, "config.tmp")
	writeFile(t, tmp, "dsn-2")
	if err := os.Rename(tmp, p); err != nil {
		t.Fatal(err)
	}
	awaitValue(t, updates, "dsn-2")
}

// TestWatchRecoversFromRemoveAndRecreate: deleting the file fails the
// resync; the periodic retry must pick the value up once the file returns.
func TestWatchRecoversFromRemoveAndRecreate(t *testing.T) {
	s := newTestStrategy(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "config")
	writeFile(t, p, "dsn-1")

	_, updates, err := s.Watch(context.Background(), p, "")
	if err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	writeFile(t, p, "dsn-2")
	awaitValue(t, updates, "dsn-2")
}

// TestWatchSeesKubernetesConfigMapSwap reproduces the kubelet's ConfigMap
// update dance: the watched path is a symlink chain through a `..data`
// symlink that is swapped atomically to a new timestamped directory.
func TestWatchSeesKubernetesConfigMapSwap(t *testing.T) {
	s := newTestStrategy(t)
	dir := t.TempDir()

	tsDir1 := filepath.Join(dir, "..2026_06_09_01")
	if err := os.Mkdir(tsDir1, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(tsDir1, "config"), "dsn-1")

	dataLink := filepath.Join(dir, "..data")
	if err := os.Symlink(tsDir1, dataLink); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "config")
	if err := os.Symlink(filepath.Join(dataLink, "config"), p); err != nil {
		t.Fatal(err)
	}

	value, updates, err := s.Watch(context.Background(), p, "")
	if err != nil {
		t.Fatal(err)
	}
	if value != "dsn-1" {
		t.Fatalf("initial value = %q, want dsn-1", value)
	}

	// The swap: new timestamped dir, retarget ..data atomically via rename,
	// remove the old dir.
	tsDir2 := filepath.Join(dir, "..2026_06_09_02")
	if err := os.Mkdir(tsDir2, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(tsDir2, "config"), "dsn-2")

	tmpLink := filepath.Join(dir, "..data_tmp")
	if err := os.Symlink(tsDir2, tmpLink); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmpLink, dataLink); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(tsDir1); err != nil {
		t.Fatal(err)
	}

	awaitValue(t, updates, "dsn-2")
}

// TestMultipleWatchersOnePath: watchers with different query strings get
// independent channels fed from one underlying file watch.
func TestMultipleWatchersOnePath(t *testing.T) {
	s := newTestStrategy(t)
	p := filepath.Join(t.TempDir(), "config")
	writeFile(t, p, "dsn-1")

	ctx := context.Background()
	_, ch1, err := s.Watch(ctx, p, "forceKill=true")
	if err != nil {
		t.Fatal(err)
	}
	_, ch2, err := s.Watch(ctx, p, "")
	if err != nil {
		t.Fatal(err)
	}

	writeFile(t, p, "dsn-2")
	awaitValue(t, ch1, "dsn-2")
	awaitValue(t, ch2, "dsn-2")
}

// TestWatchSamePathAndQueryShared: the same path+query pair returns the
// same channel rather than a second watch.
func TestWatchSamePathAndQueryShared(t *testing.T) {
	s := newTestStrategy(t)
	p := filepath.Join(t.TempDir(), "config")
	writeFile(t, p, "dsn-1")

	ctx := context.Background()
	_, ch1, err := s.Watch(ctx, p, "q=1")
	if err != nil {
		t.Fatal(err)
	}
	_, ch2, err := s.Watch(ctx, p, "q=1")
	if err != nil {
		t.Fatal(err)
	}
	if ch1 != ch2 {
		t.Error("same path+query should share one update channel")
	}
}

func TestCloseWatchClosesChannel(t *testing.T) {
	s := newTestStrategy(t)
	p := filepath.Join(t.TempDir(), "config")
	writeFile(t, p, "dsn-1")

	_, updates, err := s.Watch(context.Background(), p, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CloseWatch(p, ""); err != nil {
		t.Fatal(err)
	}
	awaitClosed(t, updates)
}

// TestCloseWatchKeepsOtherQueries: closing one query watch leaves the other
// subscriber of the same path working.
func TestCloseWatchKeepsOtherQueries(t *testing.T) {
	s := newTestStrategy(t)
	p := filepath.Join(t.TempDir(), "config")
	writeFile(t, p, "dsn-1")

	ctx := context.Background()
	_, ch1, err := s.Watch(ctx, p, "q=1")
	if err != nil {
		t.Fatal(err)
	}
	_, ch2, err := s.Watch(ctx, p, "q=2")
	if err != nil {
		t.Fatal(err)
	}

	if err := s.CloseWatch(p, "q=1"); err != nil {
		t.Fatal(err)
	}
	awaitClosed(t, ch1)

	writeFile(t, p, "dsn-2")
	awaitValue(t, ch2, "dsn-2")
}

func TestStrategyCloseClosesAllChannels(t *testing.T) {
	s := newTestStrategy(t)
	dir := t.TempDir()
	p1 := filepath.Join(dir, "a")
	p2 := filepath.Join(dir, "b")
	writeFile(t, p1, "dsn-a")
	writeFile(t, p2, "dsn-b")

	ctx := context.Background()
	_, ch1, err := s.Watch(ctx, p1, "")
	if err != nil {
		t.Fatal(err)
	}
	_, ch2, err := s.Watch(ctx, p2, "")
	if err != nil {
		t.Fatal(err)
	}

	s.Close()
	awaitClosed(t, ch1)
	awaitClosed(t, ch2)
}

// TestWatchAfterClose: the strategy re-initializes itself after Close (the
// registered global instance must survive UnregisterStrategy+re-register
// cycles).
func TestWatchAfterClose(t *testing.T) {
	s := newTestStrategy(t)
	p := filepath.Join(t.TempDir(), "config")
	writeFile(t, p, "dsn-1")

	ctx := context.Background()
	_, _, err := s.Watch(ctx, p, "")
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	value, updates, err := s.Watch(ctx, p, "")
	if err != nil {
		t.Fatalf("Watch after Close: %v", err)
	}
	if value != "dsn-1" {
		t.Errorf("value = %q, want dsn-1", value)
	}
	writeFile(t, p, "dsn-2")
	awaitValue(t, updates, "dsn-2")
}

// TestAbandonedSubscriberDoesNotWedgeStrategy: if a subscriber stops
// receiving (its hotload group is gone), pending updates must not block the
// strategy's delivery or its CloseWatch path.
func TestAbandonedSubscriberDoesNotWedgeStrategy(t *testing.T) {
	s := newTestStrategy(t)
	p := filepath.Join(t.TempDir(), "config")
	writeFile(t, p, "dsn-1")

	ctx := context.Background()
	_, abandoned, err := s.Watch(ctx, p, "q=abandoned")
	if err != nil {
		t.Fatal(err)
	}
	_ = abandoned // never read from

	_, live, err := s.Watch(ctx, p, "q=live")
	if err != nil {
		t.Fatal(err)
	}

	// Generate more updates than the abandoned watch's queue can hold.
	for i := 2; i < 40; i++ {
		writeFile(t, p, "dsn-x")
		writeFile(t, p, "dsn-2")
	}
	awaitValue(t, live, "dsn-2")

	// Closing the abandoned watch must not deadlock.
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		if err := s.CloseWatch(p, "q=abandoned"); err != nil {
			t.Errorf("CloseWatch: %v", err)
		}
	}()
	select {
	case <-doneCh:
	case <-time.After(5 * time.Second):
		t.Fatal("CloseWatch deadlocked on an abandoned subscriber")
	}
	awaitClosed(t, abandoned)
}
