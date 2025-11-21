package observability

import (
	"github.com/infobloxopen/hotload"
	"github.com/infobloxopen/hotload/internal/metrics"
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

	// KPI: Old-Epoch Drain Time
	EpochDrainTimeHistogram = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "hotload_epoch_drain_seconds",
			Help:    "Time taken to fully drain an epoch (all connections closed)",
			Buckets: prometheus.ExponentialBuckets(0.1, 2, 12), // 100ms to ~409s
		},
		[]string{"connection"},
	)

	// KPI: Old-Epoch Connection Backlog
	OldEpochConnectionsGauge = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "hotload_old_epoch_connections",
			Help: "Current number of connections in old (non-current) epochs",
		},
		[]string{"connection", "epoch"},
	)

	// KPI: Prepared Statement Recovery
	PreparedStatementRepreparesHistogram = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "hotload_prepared_stmt_reprepare_attempts",
			Help:    "Number of attempts needed to successfully re-prepare a statement after epoch change",
			Buckets: prometheus.LinearBuckets(1, 1, 10), // 1-10 attempts
		},
		[]string{"connection"},
	)

	// KPI: Transaction Continuity
	TransactionResultsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "hotload_transaction_results_total",
			Help: "Total transaction results (success/failure) with epoch context",
		},
		[]string{"connection", "result", "old_epoch"},
	)

	// KPI: DSN Update Propagation Latency
	DSNUpdatePropagationHistogram = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "hotload_dsn_update_propagation_seconds",
			Help:    "Latency from file change detection to epoch update",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 14), // 1ms to ~8s
		},
		[]string{"connection"},
	)

	// KPI: Grace Period Effectiveness
	QueryResultsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "hotload_query_results_total",
			Help: "Total query results (success/failure) with reason",
		},
		[]string{"connection", "result", "reason"},
	)
)

func init() {
	// Register the Prometheus-based metrics recorder when this package is imported
	hotload.SetMetricsRecorder(metrics.NewPrometheusRecorder())
}
