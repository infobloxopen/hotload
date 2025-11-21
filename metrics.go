package hotload

import (
	"sync"
	"time"
)

// MetricsRecorder defines the interface for recording KPI metrics.
// Applications can provide their own implementation or use the Prometheus-based
// implementation from the observability package.
type MetricsRecorder interface {
	// RecordEpochDrainTime records how long it took for an epoch to fully drain.
	RecordEpochDrainTime(connection string, epoch uint64, drainDuration time.Duration)

	// RecordOldEpochConnectionCount updates the gauge for old epoch connection backlog.
	RecordOldEpochConnectionCount(connection string, epoch uint64, count int)

	// RecordPreparedStatementReprepare records attempts needed to re-prepare a statement.
	RecordPreparedStatementReprepare(connection string, attempts int)

	// RecordTransactionResult records transaction outcome (commit/rollback).
	RecordTransactionResult(connection string, epoch uint64, success bool, wasOldEpoch bool)

	// RecordDSNUpdatePropagation records DSN update propagation latency.
	RecordDSNUpdatePropagation(connection string, detectionTime, updateTime time.Time)

	// RecordQueryResult records query execution results.
	RecordQueryResult(connection string, success bool, reason string)

	// RecordEpochTransition records when an epoch changes.
	RecordEpochTransition(connection string, newEpoch uint64)

	// RecordConnectionCount records the number of connections per epoch.
	RecordConnectionCount(connection string, epoch uint64, count int)

	// RecordOldEpochDiscard records when a connection is discarded due to old epoch.
	RecordOldEpochDiscard(connection string)
}

// noopMetricsRecorder is the default implementation that does nothing.
type noopMetricsRecorder struct{}

func (n *noopMetricsRecorder) RecordEpochDrainTime(connection string, epoch uint64, drainDuration time.Duration) {
}

func (n *noopMetricsRecorder) RecordOldEpochConnectionCount(connection string, epoch uint64, count int) {
}

func (n *noopMetricsRecorder) RecordPreparedStatementReprepare(connection string, attempts int) {
}

func (n *noopMetricsRecorder) RecordTransactionResult(connection string, epoch uint64, success bool, wasOldEpoch bool) {
}

func (n *noopMetricsRecorder) RecordDSNUpdatePropagation(connection string, detectionTime, updateTime time.Time) {
}

func (n *noopMetricsRecorder) RecordQueryResult(connection string, success bool, reason string) {
}

func (n *noopMetricsRecorder) RecordEpochTransition(connection string, newEpoch uint64) {
}

func (n *noopMetricsRecorder) RecordConnectionCount(connection string, epoch uint64, count int) {
}

func (n *noopMetricsRecorder) RecordOldEpochDiscard(connection string) {
}

var (
	globalMetricsRecorder MetricsRecorder = &noopMetricsRecorder{}
	metricsRecorderMu     sync.RWMutex
)

// SetMetricsRecorder sets the global metrics recorder.
// This is typically called by the observability package's init() function
// when it's imported.
func SetMetricsRecorder(recorder MetricsRecorder) {
	metricsRecorderMu.Lock()
	defer metricsRecorderMu.Unlock()
	if recorder == nil {
		globalMetricsRecorder = &noopMetricsRecorder{}
	} else {
		globalMetricsRecorder = recorder
	}
}

// GetMetricsRecorder returns the currently configured metrics recorder.
func GetMetricsRecorder() MetricsRecorder {
	metricsRecorderMu.RLock()
	defer metricsRecorderMu.RUnlock()
	return globalMetricsRecorder
}
