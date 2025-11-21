package hotload

import (
	"net/url"
	"sync"
	"sync/atomic"
	"time"
)

type Epoch uint64

type epochTracker struct {
	mu      sync.RWMutex
	current atomic.Uint64

	dsn map[Epoch]string

	conns map[Epoch]map[*wrappedConn]struct{}

	// Track when each epoch transitioned (became non-current)
	transitionTime map[Epoch]time.Time

	// Grace period before marking connections as "old"
	gracePeriod time.Duration

	// Connection name for metrics
	connectionName string

	logFunc func(format string, args ...interface{})
}

func newEpochTracker(initialDSN string, query url.Values, connectionName string, logFunc func(format string, args ...interface{})) *epochTracker {
	// Default grace period
	gracePeriod := 10 * time.Second

	// Allow override via query parameter: grace_period=200ms
	if gp := query.Get("grace_period"); gp != "" {
		if d, err := time.ParseDuration(gp); err == nil && d >= 0 {
			gracePeriod = d
		}
	}

	et := &epochTracker{
		dsn:            make(map[Epoch]string),
		conns:          make(map[Epoch]map[*wrappedConn]struct{}),
		transitionTime: make(map[Epoch]time.Time),
		gracePeriod:    gracePeriod,
		connectionName: connectionName,
		logFunc:        logFunc,
	}
	et.current.Store(1)
	et.dsn[1] = initialDSN
	et.conns[1] = make(map[*wrappedConn]struct{})

	if logFunc != nil {
		logFunc("epoch 1 created with initial DSN (grace period: %v)", gracePeriod)
	}

	return et
}

func (et *epochTracker) getCurrentEpoch() Epoch {
	return Epoch(et.current.Load())
}

func (et *epochTracker) getCurrentDSN() (string, Epoch) {
	epoch := et.getCurrentEpoch()
	et.mu.RLock()
	dsn := et.dsn[epoch]
	et.mu.RUnlock()
	return dsn, epoch
}

func (et *epochTracker) updateDSN(newDSN string) Epoch {
	et.mu.Lock()
	defer et.mu.Unlock()

	oldEpoch := et.getCurrentEpoch()
	newEpoch := oldEpoch + 1

	// Record transition time for the old epoch
	et.transitionTime[oldEpoch] = time.Now()

	et.dsn[newEpoch] = newDSN
	et.conns[newEpoch] = make(map[*wrappedConn]struct{})
	et.current.Store(uint64(newEpoch))

	if et.logFunc != nil {
		et.logFunc("epoch %d -> %d: DSN updated (old epoch has %d connections, grace period: %v)",
			oldEpoch, newEpoch, len(et.conns[oldEpoch]), et.gracePeriod)
	}

	return newEpoch
}

func (et *epochTracker) registerConn(epoch Epoch, conn *wrappedConn) {
	et.mu.Lock()
	defer et.mu.Unlock()

	if conns, ok := et.conns[epoch]; ok {
		conns[conn] = struct{}{}
		count := len(conns)
		if et.logFunc != nil {
			et.logFunc("epoch %d: registered connection (total: %d)", epoch, count)
		}

		// KPI: Track old epoch connection backlog
		if epoch < et.getCurrentEpoch() {
			GetMetricsRecorder().RecordOldEpochConnectionCount(et.connectionName, uint64(epoch), count)
		}
	}
}

func (et *epochTracker) unregisterConn(epoch Epoch, conn *wrappedConn) {
	et.mu.Lock()
	defer et.mu.Unlock()

	if conns, ok := et.conns[epoch]; ok {
		delete(conns, conn)
		remaining := len(conns)

		if et.logFunc != nil {
			et.logFunc("epoch %d: unregistered connection (remaining: %d)", epoch, remaining)
		}

		// KPI: Track old epoch connection backlog
		if epoch < et.getCurrentEpoch() {
			GetMetricsRecorder().RecordOldEpochConnectionCount(et.connectionName, uint64(epoch), remaining)
		}

		if remaining == 0 && epoch < et.getCurrentEpoch() {
			// KPI: Record epoch drain time
			if transitionTime, exists := et.transitionTime[epoch]; exists {
				drainDuration := time.Since(transitionTime)
				GetMetricsRecorder().RecordEpochDrainTime(et.connectionName, uint64(epoch), drainDuration)
			}

			delete(et.conns, epoch)
			delete(et.dsn, epoch)
			delete(et.transitionTime, epoch)
			if et.logFunc != nil {
				et.logFunc("epoch %d: fully drained and cleaned up", epoch)
			}
		}
	}
}

func (et *epochTracker) isOldEpoch(epoch Epoch) bool {
	current := et.getCurrentEpoch()
	if epoch >= current {
		return false
	}

	// Check if grace period has elapsed since transition
	et.mu.RLock()
	transitionTime, exists := et.transitionTime[epoch]
	et.mu.RUnlock()

	if !exists {
		// No transition time recorded (shouldn't happen), treat as old
		return true
	}

	// Only consider old if grace period has elapsed
	return time.Since(transitionTime) > et.gracePeriod
}

func (et *epochTracker) getEpochStats() map[Epoch]int {
	et.mu.RLock()
	defer et.mu.RUnlock()

	stats := make(map[Epoch]int)
	for epoch, conns := range et.conns {
		stats[epoch] = len(conns)
	}
	return stats
}
