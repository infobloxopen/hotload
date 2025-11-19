package fsnotify

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileRemovalAndReAddition(t *testing.T) {
	// Create temp file
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "config.txt")

	// Write initial content
	if err := os.WriteFile(filePath, []byte("initial"), 0644); err != nil {
		t.Fatalf("failed to write initial file: %v", err)
	}

	strategy := NewStrategy()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	initial, updates, err := strategy.Watch(ctx, filePath, url.Values{})
	if err != nil {
		t.Fatalf("Watch failed: %v", err)
	}

	if initial != "initial" {
		t.Errorf("expected initial='initial', got %q", initial)
	}

	time.Sleep(100 * time.Millisecond)

	// Remove file
	if err := os.Remove(filePath); err != nil {
		t.Fatalf("failed to remove file: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	if err := os.WriteFile(filePath, []byte("restored"), 0644); err != nil {
		t.Fatalf("failed to restore file: %v", err)
	}

	select {
	case updated := <-updates:
		if updated != "restored" {
			t.Errorf("expected update='restored', got %q", updated)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for update after file restoration")
	}
}

func TestAtomicFileReplacement(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "config.txt")

	// Write initial content
	if err := os.WriteFile(filePath, []byte("v1"), 0644); err != nil {
		t.Fatalf("failed to write initial file: %v", err)
	}

	strategy := NewStrategy()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	initial, updates, err := strategy.Watch(ctx, filePath, url.Values{})
	if err != nil {
		t.Fatalf("Watch failed: %v", err)
	}

	if initial != "v1" {
		t.Errorf("expected initial='v1', got %q", initial)
	}

	time.Sleep(100 * time.Millisecond)

	tempPath := filepath.Join(tmpDir, "config.txt.tmp")
	if err := os.WriteFile(tempPath, []byte("v2"), 0644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	if err := os.Rename(tempPath, filePath); err != nil {
		t.Fatalf("failed to rename file: %v", err)
	}

	select {
	case updated := <-updates:
		if updated != "v2" {
			t.Errorf("expected update='v2', got %q", updated)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for update after atomic replacement")
	}
}

func TestHashBasedChangeDetection(t *testing.T) {
	// Create temp file
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "config.txt")

	content := "same content"
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write initial file: %v", err)
	}

	strategy := NewStrategy()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	initial, updates, err := strategy.Watch(ctx, filePath, url.Values{})
	if err != nil {
		t.Fatalf("Watch failed: %v", err)
	}

	if initial != content {
		t.Errorf("expected initial=%q, got %q", content, initial)
	}

	time.Sleep(100 * time.Millisecond)

	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	select {
	case update := <-updates:
		t.Errorf("unexpected update received: %q (should not update for same content)", update)
	case <-time.After(1 * time.Second):
		// Expected - no update for same content
	}

	newContent := "different content"
	if err := os.WriteFile(filePath, []byte(newContent), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	select {
	case updated := <-updates:
		if updated != newContent {
			t.Errorf("expected update=%q, got %q", newContent, updated)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for update after content change")
	}
}

func TestMultipleSubscribers(t *testing.T) {
	// Create temp file
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "config.txt")

	if err := os.WriteFile(filePath, []byte("v1"), 0644); err != nil {
		t.Fatalf("failed to write initial file: %v", err)
	}

	strategy := NewStrategy()
	ctx := context.Background()

	ctx1, cancel1 := context.WithCancel(ctx)
	defer cancel1()
	_, updates1, err := strategy.Watch(ctx1, filePath, url.Values{})
	if err != nil {
		t.Fatalf("Watch 1 failed: %v", err)
	}

	ctx2, cancel2 := context.WithCancel(ctx)
	defer cancel2()
	_, updates2, err := strategy.Watch(ctx2, filePath, url.Values{})
	if err != nil {
		t.Fatalf("Watch 2 failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Update file
	if err := os.WriteFile(filePath, []byte("v2"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	// Both should receive update
	timeout := time.After(5 * time.Second)

	select {
	case updated := <-updates1:
		if updated != "v2" {
			t.Errorf("subscriber 1: expected 'v2', got %q", updated)
		}
	case <-timeout:
		t.Fatal("timeout waiting for update on subscriber 1")
	}

	select {
	case updated := <-updates2:
		if updated != "v2" {
			t.Errorf("subscriber 2: expected 'v2', got %q", updated)
		}
	case <-timeout:
		t.Fatal("timeout waiting for update on subscriber 2")
	}
}

func TestContextCancellation(t *testing.T) {
	// Create temp file
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "config.txt")

	if err := os.WriteFile(filePath, []byte("content"), 0644); err != nil {
		t.Fatalf("failed to write initial file: %v", err)
	}

	strategy := NewStrategy()
	ctx, cancel := context.WithCancel(context.Background())

	_, updates, err := strategy.Watch(ctx, filePath, url.Values{})
	if err != nil {
		t.Fatalf("Watch failed: %v", err)
	}

	// Cancel context
	cancel()

	// Channel should be closed
	select {
	case _, ok := <-updates:
		if ok {
			t.Error("expected channel to be closed after context cancellation")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for channel close")
	}
}

func TestWhitespaceTrimming(t *testing.T) {
	// Create temp file
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "config.txt")

	if err := os.WriteFile(filePath, []byte("  \n\t content \t\n  "), 0644); err != nil {
		t.Fatalf("failed to write initial file: %v", err)
	}

	strategy := NewStrategy()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	initial, _, err := strategy.Watch(ctx, filePath, url.Values{})
	if err != nil {
		t.Fatalf("Watch failed: %v", err)
	}

	expected := "content"
	if initial != expected {
		t.Errorf("expected initial=%q, got %q", expected, initial)
	}
}
