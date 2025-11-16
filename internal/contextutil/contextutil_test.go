package contextutil

import (
	"context"
	"testing"
	"time"
)

func TestMerge_BothNil(t *testing.T) {
	ctx, cancel := Merge(nil, nil)
	defer cancel()

	if ctx == nil {
		t.Fatal("Expected non-nil context")
	}

	select {
	case <-ctx.Done():
		t.Error("Context should not be done")
	default:
		// Expected
	}
}

func TestMerge_FirstContextCancels(t *testing.T) {
	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2 := context.Background()

	merged, mergedCancel := Merge(ctx1, ctx2)
	defer mergedCancel()

	// Cancel first context
	cancel1()

	// Wait for merged context to be cancelled
	select {
	case <-merged.Done():
		// Expected
	case <-time.After(100 * time.Millisecond):
		t.Error("Merged context should be cancelled when ctx1 is cancelled")
	}

	if merged.Err() != context.Canceled {
		t.Errorf("Expected error to be context.Canceled, got %v", merged.Err())
	}
}

func TestMerge_SecondContextCancels(t *testing.T) {
	ctx1 := context.Background()
	ctx2, cancel2 := context.WithCancel(context.Background())

	merged, mergedCancel := Merge(ctx1, ctx2)
	defer mergedCancel()

	// Cancel second context
	cancel2()

	// Wait for merged context to be cancelled
	select {
	case <-merged.Done():
		// Expected
	case <-time.After(100 * time.Millisecond):
		t.Error("Merged context should be cancelled when ctx2 is cancelled")
	}

	if merged.Err() != context.Canceled {
		t.Errorf("Expected error to be context.Canceled, got %v", merged.Err())
	}
}

func TestMerge_MergedCancelFunctionCalled(t *testing.T) {
	ctx1 := context.Background()
	ctx2 := context.Background()

	merged, mergedCancel := Merge(ctx1, ctx2)

	// Call merged cancel function
	mergedCancel()

	// Wait for merged context to be cancelled
	select {
	case <-merged.Done():
		// Expected
	case <-time.After(100 * time.Millisecond):
		t.Error("Merged context should be cancelled when mergedCancel is called")
	}

	if merged.Err() != context.Canceled {
		t.Errorf("Expected error to be context.Canceled, got %v", merged.Err())
	}
}

func TestMerge_DeadlineFromFirstContext(t *testing.T) {
	deadline1 := time.Now().Add(1 * time.Hour)
	ctx1, cancel1 := context.WithDeadline(context.Background(), deadline1)
	defer cancel1()

	ctx2 := context.Background()

	merged, mergedCancel := Merge(ctx1, ctx2)
	defer mergedCancel()

	d, ok := merged.Deadline()
	if !ok {
		t.Error("Expected merged context to have a deadline")
	}
	if !d.Equal(deadline1) {
		t.Errorf("Expected deadline %v, got %v", deadline1, d)
	}
}

func TestMerge_DeadlineFromSecondContext(t *testing.T) {
	ctx1 := context.Background()

	deadline2 := time.Now().Add(1 * time.Hour)
	ctx2, cancel2 := context.WithDeadline(context.Background(), deadline2)
	defer cancel2()

	merged, mergedCancel := Merge(ctx1, ctx2)
	defer mergedCancel()

	d, ok := merged.Deadline()
	if !ok {
		t.Error("Expected merged context to have a deadline")
	}
	if !d.Equal(deadline2) {
		t.Errorf("Expected deadline %v, got %v", deadline2, d)
	}
}

func TestMerge_DeadlineEarliest(t *testing.T) {
	deadline1 := time.Now().Add(2 * time.Hour)
	ctx1, cancel1 := context.WithDeadline(context.Background(), deadline1)
	defer cancel1()

	deadline2 := time.Now().Add(1 * time.Hour) // Earlier
	ctx2, cancel2 := context.WithDeadline(context.Background(), deadline2)
	defer cancel2()

	merged, mergedCancel := Merge(ctx1, ctx2)
	defer mergedCancel()

	d, ok := merged.Deadline()
	if !ok {
		t.Error("Expected merged context to have a deadline")
	}
	if !d.Equal(deadline2) {
		t.Errorf("Expected earliest deadline %v, got %v", deadline2, d)
	}
}

func TestMerge_NoDeadline(t *testing.T) {
	ctx1 := context.Background()
	ctx2 := context.Background()

	merged, mergedCancel := Merge(ctx1, ctx2)
	defer mergedCancel()

	_, ok := merged.Deadline()
	if ok {
		t.Error("Expected no deadline when neither parent has one")
	}
}

func TestMerge_ValueFromSecondContext(t *testing.T) {
	type key string
	ctx1 := context.Background()
	ctx2 := context.WithValue(context.Background(), key("testkey"), "testvalue")

	merged, mergedCancel := Merge(ctx1, ctx2)
	defer mergedCancel()

	val := merged.Value(key("testkey"))
	if val == nil {
		t.Error("Expected value to be present")
	}
	if val.(string) != "testvalue" {
		t.Errorf("Expected value 'testvalue', got %v", val)
	}
}

func TestMerge_ValueFromFirstContext(t *testing.T) {
	type key string
	ctx1 := context.WithValue(context.Background(), key("testkey"), "testvalue")
	ctx2 := context.Background()

	merged, mergedCancel := Merge(ctx1, ctx2)
	defer mergedCancel()

	val := merged.Value(key("testkey"))
	if val == nil {
		t.Error("Expected value to be present")
	}
	if val.(string) != "testvalue" {
		t.Errorf("Expected value 'testvalue', got %v", val)
	}
}

func TestMerge_ValuePriorityFromSecondContext(t *testing.T) {
	type key string
	ctx1 := context.WithValue(context.Background(), key("testkey"), "value1")
	ctx2 := context.WithValue(context.Background(), key("testkey"), "value2")

	merged, mergedCancel := Merge(ctx1, ctx2)
	defer mergedCancel()

	val := merged.Value(key("testkey"))
	if val == nil {
		t.Error("Expected value to be present")
	}
	// ctx2 should take priority
	if val.(string) != "value2" {
		t.Errorf("Expected value 'value2' from ctx2, got %v", val)
	}
}

func TestMerge_CancelSafe(t *testing.T) {
	ctx1 := context.Background()
	ctx2 := context.Background()

	merged, mergedCancel := Merge(ctx1, ctx2)

	// Call cancel multiple times - should be safe
	mergedCancel()
	mergedCancel()
	mergedCancel()

	select {
	case <-merged.Done():
		// Expected
	case <-time.After(100 * time.Millisecond):
		t.Error("Context should be cancelled")
	}
}

func TestMerge_ErrorPreservation(t *testing.T) {
	ctx1, cancel1 := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel1()
	ctx2 := context.Background()

	merged, mergedCancel := Merge(ctx1, ctx2)
	defer mergedCancel()

	// Wait for timeout
	time.Sleep(10 * time.Millisecond)

	select {
	case <-merged.Done():
		if merged.Err() != context.DeadlineExceeded {
			t.Errorf("Expected DeadlineExceeded error, got %v", merged.Err())
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Context should be cancelled by timeout")
	}
}

func TestMerge_ConcurrentAccess(t *testing.T) {
	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())

	merged, mergedCancel := Merge(ctx1, ctx2)

	// Spawn multiple goroutines accessing the context
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				_ = merged.Err()
				_, _ = merged.Deadline()
				_ = merged.Value("key")
				select {
				case <-merged.Done():
					done <- true
					return
				default:
				}
			}
			done <- true
		}()
	}

	// Cancel after a short delay
	time.Sleep(10 * time.Millisecond)
	cancel1()

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	cancel2()
	mergedCancel()
}
