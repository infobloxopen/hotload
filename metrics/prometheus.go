package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

const (
	GRPCMethodKey  = "grpc_method"
	GRPCServiceKey = "grpc_service"
	StatementKey   = "stmt" // either exec or query
	ExecStatement  = "exec"
	QueryStatement = "query"

	StrategyKey = "strategy"
	PathKey     = "path"
)

// SqlStmtsSummary is a prometheus metric to keep track of the number of times
// a sql statement is called in a transaction by statement type per grpc service
var SqlStmtsSummaryName = "transaction_sql_stmts"
var SqlStmtsSummary = prometheus.NewSummaryVec(prometheus.SummaryOpts{
	Name: SqlStmtsSummaryName,
	Help: "The number of sql stmts called in a transaction by statement type per grpc service and method",
}, []string{GRPCServiceKey, GRPCMethodKey, StatementKey})

// HotloadModtimeLatencyHistogram is modtime latency histogram (in seconds)
// ie: each sample datapoint is time.Now().Sub(Modtime)
var HotloadModtimeLatencyHistogramName = "hotload_modtime_latency_histogram"
var HotloadModtimeLatencyHistogram = prometheus.NewHistogramVec(prometheus.HistogramOpts{
	Name: HotloadModtimeLatencyHistogramName,
	Help: "Hotload modtime latency histogram (seconds)",
}, []string{PathKey, StrategyKey})

func ObserveHotloadModtimeLatencyHistogram(strategy, path string, val float64) {
	HotloadModtimeLatencyHistogram.WithLabelValues(strategy, path).Observe(val)
}

func GetCollectors() []prometheus.Collector {
	return []prometheus.Collector{
		SqlStmtsSummary,
		HotloadModtimeLatencyHistogram,
	}
}

// ResetCollectors is useful for testing
func ResetCollectors() {
	SqlStmtsSummary.Reset()
	HotloadModtimeLatencyHistogram.Reset()
}

func init() {
	prometheus.MustRegister(GetCollectors()...)
}
