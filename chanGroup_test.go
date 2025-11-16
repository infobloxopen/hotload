package hotload

import (
	"context"
	"database/sql/driver"
	"log"
	"sync"
	"testing"
	"time"
)

type testConn struct {
	closed bool
}

func (tc *testConn) Open(name string) (driver.Conn, error) {
	return tc, nil
}

func (tc *testConn) Prepare(query string) (driver.Stmt, error) {
	return nil, nil
}

func (tc *testConn) Begin() (driver.Tx, error) {
	return nil, nil
}

func (tc *testConn) Close() error {
	tc.closed = true
	return nil
}

type mockWatcher struct {
	values chan string
}

func newMockWatcher() *mockWatcher {
	return &mockWatcher{
		values: make(chan string),
	}
}

func (mw mockWatcher) getReceiveChan() <-chan string {
	return mw.values
}

func (mw mockWatcher) sendValue(value string) {
	log.Printf("mockWatcher: sending value '%s'...", value)
	mw.values <- value
	log.Printf("mockWatcher: sent value '%s'", value)
}

func TestChanGroup_ChangeValueOnUpdate_ForceKillFalse(t *testing.T) {
	testChanGroupChangeValueOnUpdate(t, false)
}

func TestChanGroup_ChangeValueOnUpdate_ForceKillTrue(t *testing.T) {
	testChanGroupChangeValueOnUpdate(t, true)
}

func testChanGroupChangeValueOnUpdate(t *testing.T, forceKill bool) {
	pctx := context.Background()
	ctx, cancel := context.WithCancel(pctx)
	defer cancel()

	mockw := newMockWatcher()
	cg := &chanGroup{
		name:       "fsnotify://postgres/tmp/mydsn.txt",
		value:      "1st-dsn",
		newValChan: mockw.getReceiveChan(),
		parentCtx:  pctx,
		ctx:        ctx,
		cancel:     cancel,
		sqlDriver:  nil,
		mu:         sync.RWMutex{},
		forceKill:  forceKill,
	}
	cg.conns = []*managedConn{
		newManagedConn(ctx, cg.value, cg.value, &testConn{}, cg.removeMgdConn),
		newManagedConn(ctx, cg.value, cg.value, &testConn{}, cg.removeMgdConn),
		newManagedConn(ctx, cg.value, cg.value, &testConn{}, cg.removeMgdConn),
	}
	mgdConns := cg.conns

	newVal := "2nd-dsn"
	go cg.runLoop()
	mockw.sendValue(newVal)

	// Yield to cg.runLoop() background thread
	time.Sleep(200 * time.Millisecond)

	cg.mu.RLock()
	if cg.value != newVal {
		cg.mu.RUnlock()
		t.Fatalf("Expected value to be %q but got %q", newVal, cg.value)
	}

	if len(cg.conns) != 0 {
		cg.mu.RUnlock()
		t.Fatalf("Expected number of managed conns to be reset to zero but got %d", len(cg.conns))
	}
	cg.mu.RUnlock()

	for _, mc := range mgdConns {
		if !mc.GetReset() {
			t.Fatal("Expected managed connection to be marked reset")
		}
		if forceKill {
			if !mc.GetKill() {
				t.Fatal("Expected managed connection to be marked killed")
			}
			if !mc.conn.(*testConn).closed {
				t.Fatal("Expected Close() to have been called on the underlying connection")
			}
		}
	}
}

func TestChanGroup_NoResetOnSameValue_ForceKillFalse(t *testing.T) {
	testChanGroupNoResetOnSameValue(t, false)
}

func TestChanGroup_NoResetOnSameValue_ForceKillTrue(t *testing.T) {
	testChanGroupNoResetOnSameValue(t, true)
}

func testChanGroupNoResetOnSameValue(t *testing.T, forceKill bool) {
	pctx := context.Background()
	ctx, cancel := context.WithCancel(pctx)
	defer cancel()

	mockw := newMockWatcher()
	cg := &chanGroup{
		name:       "fsnotify://postgres/tmp/mydsn.txt",
		value:      "1st-dsn",
		newValChan: mockw.getReceiveChan(),
		parentCtx:  pctx,
		ctx:        ctx,
		cancel:     cancel,
		sqlDriver:  nil,
		mu:         sync.RWMutex{},
		forceKill:  forceKill,
	}
	cg.conns = []*managedConn{
		newManagedConn(ctx, cg.value, cg.value, &testConn{}, cg.removeMgdConn),
		newManagedConn(ctx, cg.value, cg.value, &testConn{}, cg.removeMgdConn),
		newManagedConn(ctx, cg.value, cg.value, &testConn{}, cg.removeMgdConn),
	}
	mgdConns := cg.conns

	sameVal := "1st-dsn"
	go cg.runLoop()
	mockw.sendValue(sameVal)

	// Yield to cg.runLoop() background thread
	time.Sleep(200 * time.Millisecond)

	if cg.value != sameVal {
		t.Fatalf("Expected value to be %q but got %q", sameVal, cg.value)
	}

	if len(cg.conns) != 3 {
		t.Fatalf("Expected number of managed conns to remain at 3 but got %d", len(cg.conns))
	}

	for _, c := range cg.conns {
		if c.GetReset() {
			t.Fatal("Expected connection not to be marked reset")
		}
		if c.GetKill() {
			t.Fatal("Expected connection not to be marked killed")
		}
		if c.conn.(*testConn).closed {
			t.Fatal("Expected Close() not to have been called")
		}
	}

	for _, mc := range mgdConns {
		if mc.GetReset() {
			t.Fatal("Expected connection not to be marked reset")
		}
		if mc.GetKill() {
			t.Fatal("Expected connection not to be marked killed")
		}
		if mc.conn.(*testConn).closed {
			t.Fatal("Expected Close() not to have been called")
		}
	}
}

func TestChanGroup_ProcessNewValue_ForceKillFalse(t *testing.T) {
	testChanGroupProcessNewValue(t, false)
}

func TestChanGroup_ProcessNewValue_ForceKillTrue(t *testing.T) {
	testChanGroupProcessNewValue(t, true)
}

func testChanGroupProcessNewValue(t *testing.T, forceKill bool) {
	pctx := context.Background()
	ctx, cancel := context.WithCancel(pctx)
	defer cancel()

	mockw := newMockWatcher()
	cg := &chanGroup{
		name:       "fsnotify://postgres/tmp/mydsn.txt",
		value:      "1st-dsn",
		newValChan: mockw.getReceiveChan(),
		parentCtx:  pctx,
		ctx:        ctx,
		cancel:     cancel,
		sqlDriver:  nil,
		mu:         sync.RWMutex{},
		forceKill:  forceKill,
	}
	cg.conns = []*managedConn{
		newManagedConn(ctx, cg.value, cg.value, &testConn{}, cg.removeMgdConn),
		newManagedConn(ctx, cg.value, cg.value, &testConn{}, cg.removeMgdConn),
		newManagedConn(ctx, cg.value, cg.value, &testConn{}, cg.removeMgdConn),
	}
	mgdConns := cg.conns

	newVal := "2nd-dsn"
	cg.processNewValue(newVal)

	if cg.value != newVal {
		t.Fatalf("Expected value to be %q but got %q", newVal, cg.value)
	}

	if len(cg.conns) != 0 {
		t.Fatalf("Expected number of managed conns to be reset to zero but got %d", len(cg.conns))
	}

	for _, mc := range mgdConns {
		if !mc.GetReset() {
			t.Fatal("Expected managed connection to be marked reset")
		}
		if forceKill {
			if !mc.GetKill() {
				t.Fatal("Expected managed connection to be marked killed")
			}
			if !mc.conn.(*testConn).closed {
				t.Fatal("Expected Close() to have been called on the underlying connection")
			}
		}
	}
}
