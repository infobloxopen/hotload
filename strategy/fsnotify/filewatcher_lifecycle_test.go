package fsnotify

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestFileLifecycle_NonExistentAtStartup verifies that Watch() returns an error
// when the file does not exist at startup.
//
// Scenario:
//   - Attempt to watch a file that does not exist
//
// Expected Outcome:
//   - Watch() should return an error immediately
//   - No watcher should be created
//   - No channels should be returned
func TestFileLifecycle_NonExistentAtStartup(t *testing.T) {
	s := NewStrategy()
	defer s.Close()

	ctx := context.Background()
	nonExistentPath := filepath.Join(t.TempDir(), "does-not-exist.txt")

	// Attempt to watch non-existent file
	initialValue, updatesChan, err := s.Watch(ctx, nonExistentPath, nil)

	// Should fail immediately with an error
	if err == nil {
		t.Fatal("Expected error when watching non-existent file, got nil")
	}

	// Should not return any channels or values
	if initialValue != "" {
		t.Errorf("Expected empty initial value, got %q", initialValue)
	}

	if updatesChan != nil {
		t.Error("Expected nil updates channel when Watch fails")
	}

	t.Logf("✓ Correctly rejected non-existent file with error: %v", err)
}

// TestFileLifecycle_RemovedThenRecreated verifies that the watcher can recover
// when a file is deleted and then recreated after a delay.
//
// Scenario:
//  1. Start watching an existing file with content "initial"
//  2. Delete the file
//  3. Wait for a period (simulating temporary unavailability)
//  4. Verify NO update is sent while file is missing
//  5. Recreate the file with new content "restored"
//  6. Verify update IS sent after recreation
//
// Expected Outcome:
//   - Initial watch succeeds with "initial" value
//   - After file deletion, watcher enters failed state (logged internally)
//   - NO updates are sent while file is deleted (principle of least astonishment)
//   - After file recreation, watcher recovers via resync mechanism
//   - Updates channel receives "restored" value (and only that value)
//   - No panics or goroutine leaks occur
func TestFileLifecycle_RemovedThenRecreated(t *testing.T) {
	// Use shorter resync period for faster test execution
	resyncPeriod := 100 * time.Millisecond
	s := NewStrategyWithResync(resyncPeriod)
	defer s.Close()

	// Create initial file
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "config.txt")
	if err := os.WriteFile(filePath, []byte("initial"), 0644); err != nil {
		t.Fatalf("Failed to create initial file: %v", err)
	}

	// Start watching
	ctx := context.Background()
	initialValue, updatesChan, err := s.Watch(ctx, filePath, nil)
	if err != nil {
		t.Fatalf("Watch failed: %v", err)
	}

	if initialValue != "initial" {
		t.Errorf("Expected initial value 'initial', got %q", initialValue)
	}

	t.Log("✓ Initial watch successful, file contains:", initialValue)

	// Allow watcher to fully initialize
	time.Sleep(150 * time.Millisecond)

	// Step 1: Delete the file
	if err := os.Remove(filePath); err != nil {
		t.Fatalf("Failed to remove file: %v", err)
	}
	t.Log("✓ File deleted")

	// Step 2: Wait and verify NO update is sent while file is missing
	unavailableDelay := 400 * time.Millisecond
	t.Logf("⏳ Waiting %v to simulate temporary unavailability...", unavailableDelay)

	// This timeout should be longer than multiple resync periods to ensure
	// the resync mechanism has had multiple chances to try (and fail) to read the file
	verifyNoUpdateTimeout := time.After(unavailableDelay)

	select {
	case unexpectedUpdate := <-updatesChan:
		t.Fatalf("❌ Received unexpected update while file was deleted: %q (should not send updates for missing files)", unexpectedUpdate)
	case <-verifyNoUpdateTimeout:
		t.Log("✓ Correctly sent NO updates while file was missing")
	}

	// Step 3: Recreate file with new content
	if err := os.WriteFile(filePath, []byte("restored"), 0644); err != nil {
		t.Fatalf("Failed to recreate file: %v", err)
	}
	t.Log("✓ File recreated with new content")

	// Step 4: Wait for update via resync mechanism
	// Resync should detect and recover from failed state
	maxWaitTime := 3 * time.Second
	timeout := time.After(maxWaitTime)

	select {
	case updated := <-updatesChan:
		if updated != "restored" {
			t.Errorf("Expected update 'restored', got %q", updated)
		} else {
			t.Log("✓ Received expected update after recreation:", updated)
		}
	case <-timeout:
		t.Fatalf("Timeout after %v waiting for file recreation update", maxWaitTime)
	}

	// Step 5: Verify no additional spurious updates
	select {
	case spuriousUpdate := <-updatesChan:
		t.Errorf("❌ Received spurious additional update: %q", spuriousUpdate)
	case <-time.After(200 * time.Millisecond):
		t.Log("✓ No spurious updates after recreation")
	}
}

// TestFileLifecycle_RenamedIntoPlace verifies that the watcher detects when
// a file is atomically renamed into the watched location.
//
// Scenario:
//  1. Start watching file "config.txt" with content "v1"
//  2. Create a temporary file "config.txt.tmp" with content "v2"
//  3. Atomically rename "config.txt.tmp" to "config.txt"
//
// Expected Outcome:
//   - Initial watch succeeds with "v1" value
//   - After atomic rename, fsnotify detects the change
//   - Updates channel receives "v2" value
//   - This simulates common atomic update pattern (write-tmp-rename)
func TestFileLifecycle_RenamedIntoPlace(t *testing.T) {
	resyncPeriod := 50 * time.Millisecond
	s := NewStrategyWithResync(resyncPeriod)
	defer s.Close()

	// Create initial file
	tmpDir := t.TempDir()
	targetPath := filepath.Join(tmpDir, "config.txt")
	if err := os.WriteFile(targetPath, []byte("v1"), 0644); err != nil {
		t.Fatalf("Failed to create initial file: %v", err)
	}

	// Start watching
	ctx := context.Background()
	initialValue, updatesChan, err := s.Watch(ctx, targetPath, nil)
	if err != nil {
		t.Fatalf("Watch failed: %v", err)
	}

	if initialValue != "v1" {
		t.Errorf("Expected initial value 'v1', got %q", initialValue)
	}

	t.Log("✓ Initial watch successful, file contains:", initialValue)

	// Allow watcher to fully initialize
	time.Sleep(100 * time.Millisecond)

	// Create temporary file with new content
	tmpPath := filepath.Join(tmpDir, "config.txt.tmp")
	if err := os.WriteFile(tmpPath, []byte("v2"), 0644); err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	t.Log("✓ Created temporary file with new content")

	// Atomically rename into place (common pattern for safe updates)
	if err := os.Rename(tmpPath, targetPath); err != nil {
		t.Fatalf("Failed to rename file: %v", err)
	}
	t.Log("✓ Atomically renamed temp file to target location")

	// Wait for update notification
	maxWaitTime := 2 * time.Second
	timeout := time.After(maxWaitTime)

	select {
	case updated := <-updatesChan:
		if updated != "v2" {
			t.Errorf("Expected update 'v2' after rename, got %q", updated)
		} else {
			t.Log("✓ Received expected update after atomic rename:", updated)
		}
	case <-timeout:
		t.Fatalf("Timeout after %v waiting for rename notification", maxWaitTime)
	}
}

// TestFileLifecycle_CopiedIntoPlace verifies that the watcher detects when
// a file is copied (overwritten) into the watched location.
//
// Scenario:
//  1. Start watching file "config.txt" with content "original"
//  2. Create a source file "source.txt" with content "copied"
//  3. Copy "source.txt" over "config.txt" (overwrite)
//
// Expected Outcome:
//   - Initial watch succeeds with "original" value
//   - After copy operation, fsnotify detects Write event
//   - Updates channel receives "copied" value
//   - This simulates copy-based update pattern
func TestFileLifecycle_CopiedIntoPlace(t *testing.T) {
	resyncPeriod := 50 * time.Millisecond
	s := NewStrategyWithResync(resyncPeriod)
	defer s.Close()

	// Create initial file
	tmpDir := t.TempDir()
	targetPath := filepath.Join(tmpDir, "config.txt")
	if err := os.WriteFile(targetPath, []byte("original"), 0644); err != nil {
		t.Fatalf("Failed to create initial file: %v", err)
	}

	// Start watching
	ctx := context.Background()
	initialValue, updatesChan, err := s.Watch(ctx, targetPath, nil)
	if err != nil {
		t.Fatalf("Watch failed: %v", err)
	}

	if initialValue != "original" {
		t.Errorf("Expected initial value 'original', got %q", initialValue)
	}

	t.Log("✓ Initial watch successful, file contains:", initialValue)

	// Allow watcher to fully initialize
	time.Sleep(100 * time.Millisecond)

	// Create source file with new content
	sourcePath := filepath.Join(tmpDir, "source.txt")
	if err := os.WriteFile(sourcePath, []byte("copied"), 0644); err != nil {
		t.Fatalf("Failed to create source file: %v", err)
	}
	t.Log("✓ Created source file with new content")

	// Copy source to target (simulates 'cp source.txt config.txt')
	sourceData, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("Failed to read source file: %v", err)
	}
	if err := os.WriteFile(targetPath, sourceData, 0644); err != nil {
		t.Fatalf("Failed to copy file: %v", err)
	}
	t.Log("✓ Copied source file to target location")

	// Wait for update notification
	maxWaitTime := 2 * time.Second
	timeout := time.After(maxWaitTime)

	select {
	case updated := <-updatesChan:
		if updated != "copied" {
			t.Errorf("Expected update 'copied' after copy, got %q", updated)
		} else {
			t.Log("✓ Received expected update after copy operation:", updated)
		}
	case <-timeout:
		t.Fatalf("Timeout after %v waiting for copy notification", maxWaitTime)
	}
}

// TestFileLifecycle_MultipleRemovals verifies robustness when file is removed
// multiple times in succession.
//
// Scenario:
//  1. Start watching an existing file with "v1"
//  2. Delete file (first removal) - verify no update sent
//  3. Recreate file with "v2" - verify update sent
//  4. Delete file again (second removal) - verify no update sent
//  5. Recreate file with "v3" - verify update sent
//
// Expected Outcome:
//   - Watcher should handle multiple delete/recreate cycles
//   - NO updates sent while file is deleted (principle of least astonishment)
//   - Updates ARE sent after each recreation (v2, then v3)
//   - No goroutine leaks or panics
func TestFileLifecycle_MultipleRemovals(t *testing.T) {
	resyncPeriod := 100 * time.Millisecond
	s := NewStrategyWithResync(resyncPeriod)
	defer s.Close()

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "volatile.txt")

	// Create initial file
	if err := os.WriteFile(filePath, []byte("v1"), 0644); err != nil {
		t.Fatalf("Failed to create initial file: %v", err)
	}

	ctx := context.Background()
	initialValue, updatesChan, err := s.Watch(ctx, filePath, nil)
	if err != nil {
		t.Fatalf("Watch failed: %v", err)
	}

	if initialValue != "v1" {
		t.Errorf("Expected initial value 'v1', got %q", initialValue)
	}

	t.Log("✓ Initial watch successful")
	time.Sleep(150 * time.Millisecond)

	// First removal - verify no update during deletion
	t.Log("⏳ First removal...")
	if err := os.Remove(filePath); err != nil {
		t.Fatalf("Failed first removal: %v", err)
	}

	select {
	case unexpectedUpdate := <-updatesChan:
		t.Fatalf("❌ Received unexpected update after first removal: %q", unexpectedUpdate)
	case <-time.After(300 * time.Millisecond):
		t.Log("✓ No update sent while file missing (first deletion)")
	}

	// First recreation
	t.Log("⏳ First recreation with v2...")
	if err := os.WriteFile(filePath, []byte("v2"), 0644); err != nil {
		t.Fatalf("Failed first recreation: %v", err)
	}

	// Wait for v2 update
	select {
	case updated := <-updatesChan:
		if updated != "v2" {
			t.Errorf("Expected 'v2', got %q", updated)
		} else {
			t.Log("✓ Received v2 update after first recreation")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Timeout waiting for v2")
	}

	time.Sleep(150 * time.Millisecond)

	// Second removal - verify no update during deletion
	t.Log("⏳ Second removal...")
	if err := os.Remove(filePath); err != nil {
		t.Fatalf("Failed second removal: %v", err)
	}

	select {
	case unexpectedUpdate := <-updatesChan:
		t.Fatalf("❌ Received unexpected update after second removal: %q", unexpectedUpdate)
	case <-time.After(300 * time.Millisecond):
		t.Log("✓ No update sent while file missing (second deletion)")
	}

	// Final recreation
	t.Log("⏳ Final recreation with v3...")
	if err := os.WriteFile(filePath, []byte("v3"), 0644); err != nil {
		t.Fatalf("Failed final recreation: %v", err)
	}

	// Wait for v3 update
	select {
	case updated := <-updatesChan:
		if updated != "v3" {
			t.Errorf("Expected 'v3', got %q", updated)
		} else {
			t.Log("✓ Received v3 update after second recreation")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Timeout waiting for v3")
	}

	// Verify no additional spurious updates
	select {
	case spuriousUpdate := <-updatesChan:
		t.Errorf("❌ Received spurious update: %q", spuriousUpdate)
	case <-time.After(200 * time.Millisecond):
		t.Log("✓ No spurious updates after all operations")
	}
}

// TestFileLifecycle_RenameWithDelay verifies that rename operations are detected
// even when there's a delay between operations.
//
// Scenario:
//  1. Start watching file "target.txt" with content "initial"
//  2. Wait for a period
//  3. Create "new.txt" with content "delayed-rename"
//  4. Wait for another period
//  5. Rename "new.txt" to "target.txt"
//
// Expected Outcome:
//   - Initial watch succeeds
//   - Delayed rename is detected successfully
//   - Update contains new content from renamed file
func TestFileLifecycle_RenameWithDelay(t *testing.T) {
	resyncPeriod := 50 * time.Millisecond
	s := NewStrategyWithResync(resyncPeriod)
	defer s.Close()

	tmpDir := t.TempDir()
	targetPath := filepath.Join(tmpDir, "target.txt")

	// Create initial file
	if err := os.WriteFile(targetPath, []byte("initial"), 0644); err != nil {
		t.Fatalf("Failed to create initial file: %v", err)
	}

	ctx := context.Background()
	initialValue, updatesChan, err := s.Watch(ctx, targetPath, nil)
	if err != nil {
		t.Fatalf("Watch failed: %v", err)
	}

	if initialValue != "initial" {
		t.Errorf("Expected initial value 'initial', got %q", initialValue)
	}

	t.Log("✓ Initial watch successful")
	time.Sleep(100 * time.Millisecond)

	// Simulate delayed preparation of new file
	t.Log("⏳ Preparing new file...")
	time.Sleep(200 * time.Millisecond)

	newPath := filepath.Join(tmpDir, "new.txt")
	if err := os.WriteFile(newPath, []byte("delayed-rename"), 0644); err != nil {
		t.Fatalf("Failed to create new file: %v", err)
	}

	t.Log("⏳ Waiting before rename...")
	time.Sleep(200 * time.Millisecond)

	// Perform rename
	if err := os.Rename(newPath, targetPath); err != nil {
		t.Fatalf("Failed to rename: %v", err)
	}
	t.Log("✓ Executed delayed rename")

	// Wait for update
	select {
	case updated := <-updatesChan:
		if updated != "delayed-rename" {
			t.Errorf("Expected 'delayed-rename', got %q", updated)
		} else {
			t.Log("✓ Received expected update from delayed rename")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for delayed rename update")
	}
}

// TestFileLifecycle_CopyPreservesContent verifies that copying a file
// correctly transfers all content including whitespace.
//
// Scenario:
//  1. Start watching "target.txt" with simple content
//  2. Create source file with content containing newlines and spaces
//  3. Copy source to target
//
// Expected Outcome:
//   - Update contains trimmed content (as per readConfigFile behavior)
//   - Multi-line content is handled correctly
func TestFileLifecycle_CopyPreservesContent(t *testing.T) {
	resyncPeriod := 50 * time.Millisecond
	s := NewStrategyWithResync(resyncPeriod)
	defer s.Close()

	tmpDir := t.TempDir()
	targetPath := filepath.Join(tmpDir, "target.txt")

	// Create initial file
	if err := os.WriteFile(targetPath, []byte("simple"), 0644); err != nil {
		t.Fatalf("Failed to create initial file: %v", err)
	}

	ctx := context.Background()
	initialValue, updatesChan, err := s.Watch(ctx, targetPath, nil)
	if err != nil {
		t.Fatalf("Watch failed: %v", err)
	}

	if initialValue != "simple" {
		t.Errorf("Expected initial value 'simple', got %q", initialValue)
	}

	t.Log("✓ Initial watch successful")
	time.Sleep(100 * time.Millisecond)

	// Create source with complex content (with leading/trailing whitespace)
	complexContent := "  \n\tline1\nline2\nline3\n  \t"
	expectedTrimmed := "line1\nline2\nline3" // readConfigFile trims spaces

	sourcePath := filepath.Join(tmpDir, "source.txt")
	if err := os.WriteFile(sourcePath, []byte(complexContent), 0644); err != nil {
		t.Fatalf("Failed to create source file: %v", err)
	}

	// Copy to target
	sourceData, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("Failed to read source: %v", err)
	}
	if err := os.WriteFile(targetPath, sourceData, 0644); err != nil {
		t.Fatalf("Failed to copy: %v", err)
	}

	t.Log("✓ Copied multi-line content to target")

	// Wait for update
	select {
	case updated := <-updatesChan:
		if updated != expectedTrimmed {
			t.Errorf("Expected trimmed multi-line content %q, got %q", expectedTrimmed, updated)
		} else {
			t.Log("✓ Received correctly trimmed multi-line update")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for copy update")
	}
}
