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
	UrlKey      = "url"
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
var HotloadModtimeLatencyHistogramHelp = "Hotload modtime latency histogram (seconds) by strategy and path"
var HotloadModtimeLatencyHistogramDefBuckets = []float64{900, 1800, 2700, 3600, 4500, 5400, 7200, 10800, 14400, 28800, 86400}
var HotloadModtimeLatencyHistogram = prometheus.NewHistogramVec(prometheus.HistogramOpts{
	Name:    HotloadModtimeLatencyHistogramName,
	Help:    HotloadModtimeLatencyHistogramHelp,
	Buckets: HotloadModtimeLatencyHistogramDefBuckets,
}, []string{StrategyKey, PathKey})

func ObserveHotloadModtimeLatencyHistogram(strategy, path string, val float64) {
	HotloadModtimeLatencyHistogram.WithLabelValues(strategy, path).Observe(val)
}

// HotloadChangeTotal is count of changes detected by hotload
var HotloadChangeTotalName = "hotload_change_total"
var HotloadChangeTotalHelp = "Hotload change total by url"
var HotloadChangeTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: HotloadChangeTotalName,
	Help: HotloadChangeTotalHelp,
}, []string{UrlKey})

func IncHotloadChangeTotal(url string) {
	HotloadChangeTotal.WithLabelValues(url).Inc()
}

// HotloadLastChangedTimestampSeconds is timestamp when hotload last detected change (unix timestamp)
var HotloadLastChangedTimestampSecondsName = "hotload_last_changed_timestamp_seconds"
var HotloadLastChangedTimestampSecondsHelp = "Hotload last changed (unix timestamp), by url"
var HotloadLastChangedTimestampSeconds = prometheus.NewGaugeVec(prometheus.GaugeOpts{
	Name: HotloadLastChangedTimestampSecondsName,
	Help: HotloadLastChangedTimestampSecondsHelp,
}, []string{UrlKey})

func SetHotloadLastChangedTimestampSeconds(url string, val float64) {
	HotloadLastChangedTimestampSeconds.WithLabelValues(url).Set(val)
}

func GetCollectors() []prometheus.Collector {
	return []prometheus.Collector{
		SqlStmtsSummary,
		HotloadModtimeLatencyHistogram,
		HotloadChangeTotal,
		HotloadLastChangedTimestampSeconds,
	}
}

// ResetCollectors is useful for testing
func ResetCollectors() {
	SqlStmtsSummary.Reset()
	HotloadModtimeLatencyHistogram.Reset()
	HotloadChangeTotal.Reset()
	HotloadLastChangedTimestampSeconds.Reset()
}

func init() {
	prometheus.MustRegister(GetCollectors()...)
}
