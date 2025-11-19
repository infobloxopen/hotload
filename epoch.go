package hotload

import (
	"sync"
	"sync/atomic"
)

type Epoch uint64

type epochTracker struct {
	mu      sync.RWMutex
	current atomic.Uint64

	dsn map[Epoch]string

	conns map[Epoch]map[*wrappedConn]struct{}

	logFunc func(format string, args ...interface{})
}

func newEpochTracker(initialDSN string, logFunc func(format string, args ...interface{})) *epochTracker {
	et := &epochTracker{
		dsn:     make(map[Epoch]string),
		conns:   make(map[Epoch]map[*wrappedConn]struct{}),
		logFunc: logFunc,
	}
	et.current.Store(1)
	et.dsn[1] = initialDSN
	et.conns[1] = make(map[*wrappedConn]struct{})

	if logFunc != nil {
		logFunc("epoch 1 created with initial DSN")
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

	et.dsn[newEpoch] = newDSN
	et.conns[newEpoch] = make(map[*wrappedConn]struct{})
	et.current.Store(uint64(newEpoch))

	if et.logFunc != nil {
		et.logFunc("epoch %d -> %d: DSN updated (old epoch has %d connections)",
			oldEpoch, newEpoch, len(et.conns[oldEpoch]))
	}

	return newEpoch
}

func (et *epochTracker) registerConn(epoch Epoch, conn *wrappedConn) {
	et.mu.Lock()
	defer et.mu.Unlock()

	if conns, ok := et.conns[epoch]; ok {
		conns[conn] = struct{}{}
		if et.logFunc != nil {
			et.logFunc("epoch %d: registered connection (total: %d)", epoch, len(conns))
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

		if remaining == 0 && epoch < et.getCurrentEpoch() {
			delete(et.conns, epoch)
			delete(et.dsn, epoch)
			if et.logFunc != nil {
				et.logFunc("epoch %d: fully drained and cleaned up", epoch)
			}
		}
	}
}

func (et *epochTracker) isOldEpoch(epoch Epoch) bool {
	return epoch < et.getCurrentEpoch()
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
