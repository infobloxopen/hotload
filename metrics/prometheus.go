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

	PathKey = "path"
)

// SqlStmtsSummary is a prometheus metric to keep track of the number of times
// a sql statement is called in a transaction by statement type per grpc service
var SqlStmtsSummary = prometheus.NewSummaryVec(prometheus.SummaryOpts{
	Name: "transaction_sql_stmts",
	Help: "The number of sql stmts called in a transaction by statement type per grpc service and method",
}, []string{GRPCServiceKey, GRPCMethodKey, StatementKey})

var HotloadFsnotifyModtimeCheckGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
	Name: "hotload_fsnotify_modtime_check_status",
	Help: "Boolean gauge of hotload fsnotify modtime check status: 0 is ok, 1 is failure",
}, []string{PathKey})

var HotloadFsnotifyModtimeCheckGaugeScrapeFmt = `
# HELP hotload_fsnotify_modtime_check_status Boolean gauge of hotload fsnotify modtime check status: 0 is ok, 1 is failure
# TYPE hotload_fsnotify_modtime_check_status gauge
hotload_fsnotify_modtime_check_status{path="%s"} %d
`

var HotloadFsnotifyModtimeFailureCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "hotload_fsnotify_modtime_failure_count",
	Help: "Total count of hotload fsnotify modtime failures",
}, []string{PathKey})

var HotloadFsnotifyModtimeFailureCounterScrapeFmt = `
# HELP hotload_fsnotify_modtime_failure_count Total count of hotload fsnotify modtime failures
# TYPE hotload_fsnotify_modtime_failure_count counter
hotload_fsnotify_modtime_failure_count{path="%s"} %d
`

func SetHotloadFsnotifyModtimeCheckGauge(path string, val float64) {
	HotloadFsnotifyModtimeCheckGauge.WithLabelValues(path).Set(val)
}

func IncHotloadFsnotifyModtimeFailureCounter(path string) {
	HotloadFsnotifyModtimeFailureCounter.WithLabelValues(path).Inc()
}

func GetHotloadCollectors() []prometheus.Collector {
	return []prometheus.Collector{
		SqlStmtsSummary,
		HotloadFsnotifyModtimeCheckGauge,
		HotloadFsnotifyModtimeFailureCounter,
	}
}

// ResetHotloadCollectors is useful for testing
func ResetHotloadCollectors() {
	SqlStmtsSummary.Reset()
	HotloadFsnotifyModtimeCheckGauge.Reset()
	HotloadFsnotifyModtimeFailureCounter.Reset()
}

func init() {
	prometheus.MustRegister(GetHotloadCollectors()...)
}
