package fsnotify

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/infobloxopen/hotload/logger"
)

// getTestTempDir returns the best directory for test files.
// Prefers /dev/shm (tmpfs) in CI for reliability, falls back to os.TempDir().
func getTestTempDir(t *testing.T) string {
	// In CI environments, prefer tmpfs to avoid disk I/O issues
	if os.Getenv("CI") != "" || os.Getenv("GITHUB_ACTIONS") != "" {
		if stat, err := os.Stat("/dev/shm"); err == nil && stat.IsDir() {
			t.Logf("Using tmpfs for reliable filesystem operations: /dev/shm")
			return "/dev/shm"
		}
	}
	return os.TempDir()
}

// createTestFile creates a temporary test file in the best available location.
// Returns the file handle and cleanup function.
func createTestFile(t *testing.T, pattern string) (*os.File, func()) {
	tmpDir := getTestTempDir(t)
	tmpFile, err := os.CreateTemp(tmpDir, pattern)
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}

	cleanup := func() {
		os.Remove(tmpFile.Name())
	}

	return tmpFile, cleanup
}

// writeAndSync writes content to a file atomically and forces sync to ensure fsnotify sees it.
// Uses atomic write pattern: write to temp file, sync, then rename.
func writeAndSync(t *testing.T, filename string, content []byte) {
	// Create temp file in same directory for atomic rename
	dir := filepath.Dir(filename)

	tmpFile, err := os.CreateTemp(dir, ".hotload-tmp-*")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	tmpName := tmpFile.Name()

	// Write content
	if _, err := tmpFile.Write(content); err != nil {
		tmpFile.Close()
		os.Remove(tmpName)
		t.Fatalf("failed to write temp file: %v", err)
	}

	// Force sync before rename
	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		os.Remove(tmpName)
		t.Fatalf("failed to sync temp file: %v", err)
	}

	tmpFile.Close()

	// Atomic rename
	if err := os.Rename(tmpName, filename); err != nil {
		os.Remove(tmpName)
		t.Fatalf("failed to rename temp file: %v", err)
	}

	// Sync directory to ensure rename is visible
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
}

// TestWatch_FileNotFound verifies error handling for non-existent files.
func TestWatch_FileNotFound(t *testing.T) {
	s := NewStrategy()
	defer s.Close()

	ctx := context.Background()
	_, _, err := s.Watch(ctx, "/nonexistent/file.txt", nil)
	if err == nil {
		t.Fatal("expected error for non-existent file, got nil")
	}
}

// TestWatch_InitialValue verifies that Watch returns the correct initial file content.
func TestWatch_InitialValue(t *testing.T) {
	s := NewStrategy()
	defer s.Close()

	// Create temp file
	tmpFile, cleanup := createTestFile(t, "hotload-test-*")
	defer cleanup()
	filename := tmpFile.Name()
	tmpFile.Close()

	content := "initial content"
	writeAndSync(t, filename, []byte(content))

	ctx := context.Background()
	value, _, err := s.Watch(ctx, filename, nil)
	if err != nil {
		t.Fatalf("Watch failed: %v", err)
	}

	if value != content {
		t.Errorf("expected initial value %q, got %q", content, value)
	}
}

// TestWatch_WhitespaceTrimmming verifies that content is trimmed.
func TestWatch_WhitespaceTrimming(t *testing.T) {
	s := NewStrategy()
	defer s.Close()

	tmpFile, cleanup := createTestFile(t, "hotload-test-*")
	defer cleanup()
	filename := tmpFile.Name()
	tmpFile.Close()

	// Write content with surrounding whitespace
	content := "  \t\n  trimmed content  \r\n\t  "
	writeAndSync(t, filename, []byte(content))

	ctx := context.Background()
	value, _, err := s.Watch(ctx, filename, nil)
	if err != nil {
		t.Fatalf("Watch failed: %v", err)
	}

	expected := "trimmed content"
	if value != expected {
		t.Errorf("expected trimmed value %q, got %q", expected, value)
	}
}

// TestWatch_FileUpdate verifies that updates are broadcast to subscribers.
func TestWatch_FileUpdate(t *testing.T) {
	s := NewStrategyWithResync(50 * time.Millisecond)
	defer s.Close()

	tmpFile, cleanup := createTestFile(t, "hotload-test-*")
	defer cleanup()
	filename := tmpFile.Name()
	tmpFile.Close()

	// Write initial content
	writeAndSync(t, filename, []byte("v1"))

	ctx := context.Background()
	value, updates, err := s.Watch(ctx, filename, nil)
	if err != nil {
		t.Fatalf("Watch failed: %v", err)
	}

	if value != "v1" {
		t.Errorf("expected initial value 'v1', got %q", value)
	}

	// Update file
	time.Sleep(100 * time.Millisecond) // Let watcher initialize
	writeAndSync(t, filename, []byte("v2"))

	// Wait for update
	select {
	case updated := <-updates:
		if updated != "v2" {
			t.Errorf("expected update 'v2', got %q", updated)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for update")
	}
}

// TestWatch_MultipleUpdates verifies multiple consecutive updates.
func TestWatch_MultipleUpdates(t *testing.T) {
	// Enable detailed logging for debugging CI issues
	logger.WithLogger(func(args ...interface{}) {
		t.Log(args...)
	})
	defer logger.WithLogger(nil)

	s := NewStrategyWithResync(50 * time.Millisecond)
	defer s.Close()

	tmpFile, cleanup := createTestFile(t, "hotload-test-*")
	defer cleanup()
	filename := tmpFile.Name()
	tmpFile.Close()

	writeAndSync(t, filename, []byte("v1"))

	ctx := context.Background()
	_, updates, err := s.Watch(ctx, filename, nil)
	if err != nil {
		t.Fatalf("Watch failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Multiple updates
	values := []string{"v2", "v3", "v4"}
	for _, v := range values {
		t.Logf("Writing update: %q", v)
		writeAndSync(t, filename, []byte(v))

		t.Logf("Waiting for update %q on channel...", v)
		select {
		case updated := <-updates:
			t.Logf("Received update: %q", updated)
			if updated != v {
				t.Errorf("expected update %q, got %q", v, updated)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timeout waiting for update %q", v)
		}

		time.Sleep(100 * time.Millisecond) // Avoid rapid updates
	}
}

// TestWatch_NoUpdateOnSameContent verifies deduplication.
func TestWatch_NoUpdateOnSameContent(t *testing.T) {
	s := NewStrategyWithResync(50 * time.Millisecond)
	defer s.Close()

	tmpFile, cleanup := createTestFile(t, "hotload-test-*")
	defer cleanup()
	filename := tmpFile.Name()
	tmpFile.Close()

	content := "same content"
	writeAndSync(t, filename, []byte(content))

	ctx := context.Background()
	_, updates, err := s.Watch(ctx, filename, nil)
	if err != nil {
		t.Fatalf("Watch failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Write same content
	writeAndSync(t, filename, []byte(content))

	// Should not receive update
	select {
	case update := <-updates:
		t.Errorf("unexpected update received: %q", update)
	case <-time.After(500 * time.Millisecond):
		// Expected - no update for same content
	}
}

// TestWatch_FileRemoveAndRecreate verifies handling of file deletion and recreation.
func TestWatch_FileRemoveAndRecreate(t *testing.T) {
	// Enable detailed logging for debugging CI issues
	logger.WithLogger(func(args ...interface{}) {
		t.Log(args...)
	})
	defer logger.WithLogger(nil)

	// Use very aggressive resync interval in CI environments where filesystem events may be unreliable
	resyncInterval := 100 * time.Millisecond
	sleepAfterOps := 150 * time.Millisecond

	if os.Getenv("CI") != "" || os.Getenv("GITHUB_ACTIONS") != "" {
		// CI filesystems can be extremely slow - poll every 10ms and wait longer for FS ops
		resyncInterval = 10 * time.Millisecond
		sleepAfterOps = 500 * time.Millisecond
		t.Log("Running in CI environment - using very aggressive resync interval (10ms)")
	}

	s := NewStrategyWithResync(resyncInterval)
	defer s.Close()

	tmpFile, cleanup := createTestFile(t, "hotload-test-*")
	defer cleanup()
	filename := tmpFile.Name()
	tmpFile.Close()

	t.Logf("Using temp file: %s", filename)

	writeAndSync(t, filename, []byte("v1"))
	t.Logf("Wrote initial content 'v1' to %s", filename)

	ctx := context.Background()
	initialValue, updates, err := s.Watch(ctx, filename, nil)
	if err != nil {
		t.Fatalf("Watch failed: %v", err)
	}
	t.Logf("Watch started, initial value: %q", initialValue)

	time.Sleep(sleepAfterOps)
	t.Logf("Slept %v after Watch initialization", sleepAfterOps)

	// Remove file
	t.Logf("About to remove file: %s", filename)
	if err := os.Remove(filename); err != nil {
		t.Fatalf("failed to remove file: %v", err)
	}
	t.Logf("File removed successfully")

	time.Sleep(sleepAfterOps)
	t.Logf("Slept %v after file removal", sleepAfterOps)

	// Recreate with new content
	t.Logf("About to recreate file with content 'v2'")
	writeAndSync(t, filename, []byte("v2"))
	t.Logf("File recreated successfully with 'v2' (with fsync)")

	// Verify file is readable immediately
	if content, err := os.ReadFile(filename); err != nil {
		t.Logf("ERROR: Cannot read recreated file: %v", err)
	} else {
		t.Logf("Verified recreated file readable, content: %q", string(content))
	}

	// Should receive update after recreation (with retry mechanism)
	// Relaxed timeout for CI environments
	t.Logf("Waiting for update on channel (timeout: 15s)...")
	select {
	case updated := <-updates:
		t.Logf("Received update: %q", updated)
		if updated != "v2" {
			t.Errorf("expected update 'v2', got %q", updated)
		}
	case <-time.After(15 * time.Second):
		t.Logf("TIMEOUT: No update received after 15 seconds")
		t.Fatal("timeout waiting for update after file recreation")
	}
} // TestWatch_MultipleSubscribers verifies that multiple watchers receive updates.
func TestWatch_MultipleSubscribers(t *testing.T) {
	s := NewStrategyWithResync(50 * time.Millisecond)
	defer s.Close()

	tmpFile, cleanup := createTestFile(t, "hotload-test-*")
	defer cleanup()
	filename := tmpFile.Name()
	tmpFile.Close()

	writeAndSync(t, filename, []byte("v1"))

	ctx := context.Background()

	// Subscribe with empty query
	_, updates1, err := s.Watch(ctx, filename, nil)
	if err != nil {
		t.Fatalf("Watch 1 failed: %v", err)
	}

	// Subscribe with query params (should still get updates)
	_, updates2, err := s.Watch(ctx, filename, map[string][]string{"forceKill": {"true"}})
	if err != nil {
		t.Fatalf("Watch 2 failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Update file
	writeAndSync(t, filename, []byte("v2"))

	// Both should receive update
	timeout := time.After(2 * time.Second)

	select {
	case updated := <-updates1:
		if updated != "v2" {
			t.Errorf("subscriber 1: expected update 'v2', got %q", updated)
		}
	case <-timeout:
		t.Fatal("timeout waiting for update on subscriber 1")
	}

	select {
	case updated := <-updates2:
		if updated != "v2" {
			t.Errorf("subscriber 2: expected update 'v2', got %q", updated)
		}
	case <-timeout:
		t.Fatal("timeout waiting for update on subscriber 2")
	}
}

// TestWatch_ContextCancellation verifies cleanup on context cancellation.
func TestWatch_ContextCancellation(t *testing.T) {
	s := NewStrategy()
	defer s.Close()

	tmpFile, cleanup := createTestFile(t, "hotload-test-*")
	defer cleanup()
	filename := tmpFile.Name()
	tmpFile.Close()

	writeAndSync(t, filename, []byte("v1"))

	ctx, cancel := context.WithCancel(context.Background())
	_, updates, err := s.Watch(ctx, filename, nil)
	if err != nil {
		t.Fatalf("Watch failed: %v", err)
	}

	// Cancel context
	cancel()

	// Channel should be closed
	time.Sleep(100 * time.Millisecond)

	select {
	case _, ok := <-updates:
		if ok {
			t.Error("expected channel to be closed after context cancellation")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for channel close")
	}
}

// TestWatch_PathCleaning verifies path normalization.
func TestWatch_PathCleaning(t *testing.T) {
	s := NewStrategy()
	defer s.Close()

	tmpFile, cleanup := createTestFile(t, "hotload-test-*")
	defer cleanup()
	filename := tmpFile.Name()
	tmpFile.Close()

	writeAndSync(t, filename, []byte("content"))

	ctx := context.Background()

	// Watch with extra slashes
	dirtyPath := "//" + filename
	value, _, err := s.Watch(ctx, dirtyPath, nil)
	if err != nil {
		t.Fatalf("Watch failed with dirty path: %v", err)
	}

	if value != "content" {
		t.Errorf("expected content 'content', got %q", value)
	}
}

// TestCloseWatch verifies CloseWatch functionality.
func TestCloseWatch(t *testing.T) {
	s := NewStrategy()
	defer s.Close()

	tmpFile, cleanup := createTestFile(t, "hotload-test-*")
	defer cleanup()
	filename := tmpFile.Name()
	tmpFile.Close()

	writeAndSync(t, filename, []byte("v1"))

	ctx, c2 := context.WithCancel(context.Background())
	_, updates, err := s.Watch(ctx, filename, nil)
	if err != nil {
		t.Fatalf("Watch failed: %v", err)
	}
	// Close watch (closes all subscriptions for this path)
	c2()

	// Channel should close
	time.Sleep(100 * time.Millisecond)

	select {
	case _, ok := <-updates:
		if ok {
			t.Error("expected channel to be closed after CloseWatch")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for channel close")
	}
}

// TestClose verifies that Close cleans up all resources.
func TestClose(t *testing.T) {
	s := NewStrategy()

	tmpFile, cleanup := createTestFile(t, "hotload-test-*")
	defer cleanup()
	filename := tmpFile.Name()
	tmpFile.Close()

	writeAndSync(t, filename, []byte("v1"))

	ctx := context.Background()
	_, updates1, err := s.Watch(ctx, filename, nil)
	if err != nil {
		t.Fatalf("Watch 1 failed: %v", err)
	}

	_, updates2, err := s.Watch(ctx, filename, map[string][]string{"param": {"value"}})
	if err != nil {
		t.Fatalf("Watch 2 failed: %v", err)
	}

	// Close strategy
	s.Close()

	// Both channels should be closed
	time.Sleep(100 * time.Millisecond)

	select {
	case _, ok := <-updates1:
		if ok {
			t.Error("expected updates1 channel to be closed after Close")
		}
	default:
		t.Error("updates1 channel was not closed")
	}

	select {
	case _, ok := <-updates2:
		if ok {
			t.Error("expected updates2 channel to be closed after Close")
		}
	default:
		t.Error("updates2 channel was not closed")
	}
}

// TestWatch_ConcurrentAccess verifies thread-safety.
func TestWatch_ConcurrentAccess(t *testing.T) {
	s := NewStrategy()
	defer s.Close()

	tmpFile, cleanup := createTestFile(t, "hotload-test-*")
	defer cleanup()
	filename := tmpFile.Name()
	tmpFile.Close()

	writeAndSync(t, filename, []byte("v1"))

	ctx := context.Background()

	// Start multiple watchers concurrently
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(id int) {
			defer func() { done <- true }()

			// Create cancelable context for each watcher
			watchCtx, cancel := context.WithCancel(ctx)
			defer cancel()

			// Use unique query string for each goroutine to avoid subscription conflicts
			query := map[string][]string{"id": {fmt.Sprintf("%d", id)}}
			_, updates, err := s.Watch(watchCtx, filename, query)
			if err != nil {
				t.Errorf("Watch %d failed: %v", id, err)
				return
			}

			// Wait a bit then cancel context to close the channel
			time.Sleep(50 * time.Millisecond)
			cancel()

			// Drain channel
			for range updates {
			}
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}

// TestWatch_DifferentFiles verifies watching multiple files simultaneously.
func TestWatch_DifferentFiles(t *testing.T) {
	s := NewStrategyWithResync(50 * time.Millisecond)
	defer s.Close()

	// Create two temp files
	tmpFile1, cleanup1 := createTestFile(t, "hotload-test-1-*")
	defer cleanup1()
	filename1 := tmpFile1.Name()
	tmpFile1.Close()

	tmpFile2, cleanup2 := createTestFile(t, "hotload-test-2-*")
	defer cleanup2()
	filename2 := tmpFile2.Name()
	tmpFile2.Close()

	writeAndSync(t, filename1, []byte("file1"))
	writeAndSync(t, filename2, []byte("file2"))

	ctx := context.Background()

	_, updates1, err := s.Watch(ctx, filename1, nil)
	if err != nil {
		t.Fatalf("Watch file 1 failed: %v", err)
	}

	_, updates2, err := s.Watch(ctx, filename2, nil)
	if err != nil {
		t.Fatalf("Watch file 2 failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Update file 1
	writeAndSync(t, filename1, []byte("file1-updated"))

	// Only updates1 should receive update
	select {
	case updated := <-updates1:
		if updated != "file1-updated" {
			t.Errorf("file1: expected 'file1-updated', got %q", updated)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for file1 update")
	}

	// updates2 should not have received anything
	select {
	case update := <-updates2:
		t.Errorf("file2: unexpected update: %q", update)
	case <-time.After(200 * time.Millisecond):
		// Expected - no update for file2
	}
}

// BenchmarkWatch_Subscribe measures subscription overhead.
func BenchmarkWatch_Subscribe(b *testing.B) {
	s := NewStrategy()
	defer s.Close()

	// Note: benchmarks don't have t.Logf, so we use a simplified version
	tmpDir := os.TempDir()
	if os.Getenv("CI") != "" || os.Getenv("GITHUB_ACTIONS") != "" {
		if stat, err := os.Stat("/dev/shm"); err == nil && stat.IsDir() {
			tmpDir = "/dev/shm"
		}
	}

	tmpFile, err := os.CreateTemp(tmpDir, "hotload-bench-*")
	if err != nil {
		b.Fatalf("failed to create temp file: %v", err)
	}
	filename := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(filename)

	if err := os.WriteFile(filename, []byte("content"), 0644); err != nil {
		b.Fatalf("failed to write temp file: %v", err)
	}
	if f, err := os.Open(filename); err == nil {
		f.Sync()
		f.Close()
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx, c2 := context.WithCancel(context.Background())
		query := map[string][]string{"id": {fmt.Sprintf("%d", i)}}
		_, _, err := s.Watch(ctx, filename, query)
		if err != nil {
			b.Fatalf("Watch failed: %v", err)
		}
		c2()
	}
}
