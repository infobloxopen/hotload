package hotload

import (
	"net/url"
	"sync"
	"testing"
	"time"
)

const (
	defaultGracePeriod = 100 * time.Millisecond
)

func testGracePeriodQuery() url.Values {
	q := url.Values{}
	q.Set("grace_period", defaultGracePeriod.String())
	return q
}

func TestEpochTrackerBasic(t *testing.T) {
	var logMessages []string
	var logMu sync.Mutex
	logFunc := func(format string, args ...interface{}) {
		logMu.Lock()
		defer logMu.Unlock()
		logMessages = append(logMessages, format)
	}

	tracker := newEpochTracker("dsn1", testGracePeriodQuery(), logFunc)

	if epoch := tracker.getCurrentEpoch(); epoch != 1 {
		t.Errorf("expected initial epoch=1, got %d", epoch)
	}

	dsn, epoch := tracker.getCurrentDSN()
	if dsn != "dsn1" {
		t.Errorf("expected dsn='dsn1', got %q", dsn)
	}
	if epoch != 1 {
		t.Errorf("expected epoch=1, got %d", epoch)
	}
}

func TestEpochTransition(t *testing.T) {
	tracker := newEpochTracker("dsn1", testGracePeriodQuery(), nil)

	newEpoch := tracker.updateDSN("dsn2")
	if newEpoch != 2 {
		t.Errorf("expected new epoch=2, got %d", newEpoch)
	}

	if epoch := tracker.getCurrentEpoch(); epoch != 2 {
		t.Errorf("expected current epoch=2, got %d", epoch)
	}

	dsn, epoch := tracker.getCurrentDSN()
	if dsn != "dsn2" {
		t.Errorf("expected dsn='dsn2', got %q", dsn)
	}
	if epoch != 2 {
		t.Errorf("expected epoch=2, got %d", epoch)
	}

	// During grace period, epoch 1 should NOT be considered old
	if tracker.isOldEpoch(1) {
		t.Error("expected epoch 1 to NOT be old during grace period")
	}

	// Wait for grace period to expire
	time.Sleep(defaultGracePeriod + 50*time.Millisecond)

	// After grace period, epoch 1 should be old
	if !tracker.isOldEpoch(1) {
		t.Error("expected epoch 1 to be old after grace period")
	}

	if tracker.isOldEpoch(2) {
		t.Error("expected epoch 2 to not be old")
	}
}

func TestConnectionTracking(t *testing.T) {
	tracker := newEpochTracker("dsn1", testGracePeriodQuery(), nil)

	conn1 := &wrappedConn{epoch: 1}
	conn2 := &wrappedConn{epoch: 1}

	tracker.registerConn(1, conn1)
	tracker.registerConn(1, conn2)

	stats := tracker.getEpochStats()
	if count, ok := stats[1]; !ok || count != 2 {
		t.Errorf("expected 2 connections for epoch 1, got %d", count)
	}

	tracker.unregisterConn(1, conn1)

	stats = tracker.getEpochStats()
	if count, ok := stats[1]; !ok || count != 1 {
		t.Errorf("expected 1 connection for epoch 1, got %d", count)
	}

	tracker.unregisterConn(1, conn2)

	stats = tracker.getEpochStats()
	if count, ok := stats[1]; !ok || count != 0 {
		t.Errorf("expected epoch 1 to have 0 connections, got %d (ok=%v)", count, ok)
	}
}

func TestEpochCleanup(t *testing.T) {
	tracker := newEpochTracker("dsn1", testGracePeriodQuery(), nil)

	conn1 := &wrappedConn{epoch: 1}
	conn2 := &wrappedConn{epoch: 1}
	tracker.registerConn(1, conn1)
	tracker.registerConn(1, conn2)

	tracker.updateDSN("dsn2")

	conn3 := &wrappedConn{epoch: 2}
	tracker.registerConn(2, conn3)

	stats := tracker.getEpochStats()
	if len(stats) != 2 {
		t.Errorf("expected 2 epochs, got %d", len(stats))
	}

	tracker.unregisterConn(1, conn1)
	tracker.unregisterConn(1, conn2)

	stats = tracker.getEpochStats()
	if _, ok := stats[1]; ok {
		t.Error("expected epoch 1 to be cleaned up")
	}
	if count, ok := stats[2]; !ok || count != 1 {
		t.Errorf("expected 1 connection for epoch 2, got %d", count)
	}
}

func TestConcurrentEpochOperations(t *testing.T) {
	tracker := newEpochTracker("dsn1", testGracePeriodQuery(), nil)

	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tracker.updateDSN("dsn" + string(rune('0'+i)))
		}(i)
	}

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			conn := &wrappedConn{epoch: Epoch(i%5 + 1)}
			tracker.registerConn(conn.epoch, conn)
		}(i)
	}

	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tracker.getCurrentDSN()
			tracker.getCurrentEpoch()
			tracker.getEpochStats()
		}()
	}

	wg.Wait()

	finalEpoch := tracker.getCurrentEpoch()
	if finalEpoch < 1 {
		t.Errorf("expected final epoch >= 1, got %d", finalEpoch)
	}
}

func TestMultipleEpochTransitions(t *testing.T) {
	tracker := newEpochTracker("dsn1", testGracePeriodQuery(), nil)

	conns := make(map[Epoch]*wrappedConn)

	for i := 1; i <= 5; i++ {
		epoch := Epoch(i)
		conn := &wrappedConn{epoch: epoch}
		conns[epoch] = conn
		tracker.registerConn(epoch, conn)

		if i < 5 {
			newEpoch := tracker.updateDSN("dsn" + string(rune('0'+i+1)))
			if newEpoch != Epoch(i+1) {
				t.Errorf("expected epoch %d, got %d", i+1, newEpoch)
			}
		}
	}

	stats := tracker.getEpochStats()
	if len(stats) != 5 {
		t.Errorf("expected 5 epochs, got %d", len(stats))
	}

	for i := 1; i <= 4; i++ {
		epoch := Epoch(i)
		tracker.unregisterConn(epoch, conns[epoch])
	}

	stats = tracker.getEpochStats()
	if len(stats) != 1 {
		t.Errorf("expected 1 epoch, got %d", len(stats))
	}
	if count, ok := stats[5]; !ok || count != 1 {
		t.Errorf("expected 1 connection for epoch 5, got %d", count)
	}
}

func TestConfigurableGracePeriod(t *testing.T) {
	const driverDefaultGracePeriod = 10 * time.Second

	tests := []struct {
		name           string
		queryParam     string
		expectedPeriod time.Duration
	}{
		{
			name:           "default grace period",
			queryParam:     "",
			expectedPeriod: driverDefaultGracePeriod,
		},
		{
			name:           "custom grace period 200ms",
			queryParam:     "grace_period=200ms",
			expectedPeriod: 200 * time.Millisecond,
		},
		{
			name:           "custom grace period 500ms",
			queryParam:     "grace_period=500ms",
			expectedPeriod: 500 * time.Millisecond,
		},
		{
			name:           "zero grace period",
			queryParam:     "grace_period=0",
			expectedPeriod: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query := url.Values{}
			if tt.queryParam != "" {
				parts := make(map[string]string)
				for _, kv := range []string{tt.queryParam} {
					idx := 0
					for i, c := range kv {
						if c == '=' {
							idx = i
							break
						}
					}
					if idx > 0 {
						parts[kv[:idx]] = kv[idx+1:]
					}
				}
				for k, v := range parts {
					query.Set(k, v)
				}
			}

			tracker := newEpochTracker("dsn1", query, nil)

			// Verify tracker was created successfully
			if tracker == nil {
				t.Fatal("expected tracker to be created")
			}

			if tracker.gracePeriod != tt.expectedPeriod {
				t.Errorf("expected grace period %v, got %v", tt.expectedPeriod, tracker.gracePeriod)
			}
		})
	}
}
