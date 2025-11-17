package observability

import (
	"github.com/infobloxopen/hotload/metrics"
)

// Metrics wraps the hotload metrics provider with additional metrics.
type Metrics struct {
	provider metrics.Provider
}

// NewMetrics creates a new metrics wrapper.
func NewMetrics(provider metrics.Provider) *Metrics {
	if provider == nil {
		provider = &metrics.NoOpProvider{}
	}
	return &Metrics{provider: provider}
}

// RecordReload records a DSN reload event using existing metrics.
func (m *Metrics) RecordReload(url string, timestamp float64) {
	m.provider.IncHotloadChangeTotal(url)
	m.provider.SetHotloadLastChangedTimestampSeconds(url, timestamp)
}

// RecordConnectionDrained increments the drained connections counter.
func (m *Metrics) RecordConnectionDrained(driverName string) {
	m.provider.IncHotloadConnectionsDrained(driverName)
}

// RecordConnectionKilled increments the killed connections counter.
func (m *Metrics) RecordConnectionKilled(driverName, reason string) {
	m.provider.IncHotloadConnectionsKilled(driverName, reason)
}

// SetEpoch sets the current epoch gauge.
func (m *Metrics) SetEpoch(driverName string, epoch uint64) {
	m.provider.SetHotloadEpoch(driverName, epoch)
}
