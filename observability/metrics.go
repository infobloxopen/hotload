package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	EpochGauge = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "hotload_epoch",
			Help: "Current DSN epoch version",
		},
		[]string{"connection"},
	)
	
	EpochTransitionsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "hotload_epoch_transitions_total",
			Help: "Total number of DSN changes (epoch transitions)",
		},
		[]string{"connection"},
	)
	
	ConnectionsPerEpoch = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "hotload_connections_per_epoch",
			Help: "Number of active connections for each epoch",
		},
		[]string{"connection", "epoch"},
	)
	
	OldEpochConnectionsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "hotload_old_epoch_connections_total",
			Help: "Total number of connections discarded due to old epoch",
		},
		[]string{"connection"},
	)
)

func RecordEpochTransition(connection string, newEpoch uint64) {
	EpochGauge.WithLabelValues(connection).Set(float64(newEpoch))
	EpochTransitionsTotal.WithLabelValues(connection).Inc()
}

func RecordConnectionCount(connection string, epoch uint64, count int) {
	ConnectionsPerEpoch.WithLabelValues(connection, string(rune(epoch))).Set(float64(count))
}

func RecordOldEpochDiscard(connection string) {
	OldEpochConnectionsTotal.WithLabelValues(connection).Inc()
}
