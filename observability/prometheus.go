// Package observability exports hotload activity as prometheus metrics.
//
// Hotload v3's core has no metrics dependency; it emits events through
// hotload.Hooks. This module adapts those events to prometheus collectors
// that preserve the metric names of hotload v1, so existing dashboards keep
// working. Unlike v1, registration is explicit:
//
//	import "github.com/infobloxopen/hotload/observability"
//
//	func main() {
//	    observability.MustEnablePrometheus(nil) // nil = prometheus.DefaultRegisterer
//	    ...
//	}
package observability

import (
	"github.com/prometheus/client_golang/prometheus"

	hotload "github.com/infobloxopen/hotload/v3"
)

// Label keys and values, identical to hotload v1's metrics package.
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

// Metric names, identical to hotload v1.
const (
	SqlStmtsSummaryName                    = "transaction_sql_stmts"
	HotloadChangeTotalName                 = "hotload_change_total"
	HotloadLastChangedTimestampSecondsName = "hotload_last_changed_timestamp_seconds"
	HotloadModtimeLatencyHistogramName     = "hotload_modtime_latency_histogram"
)

// HotloadModtimeLatencyHistogramDefBuckets are the default buckets (seconds)
// of the modtime latency histogram, identical to hotload v1.
var HotloadModtimeLatencyHistogramDefBuckets = []float64{900, 1800, 2700, 3600, 4500, 5400, 7200, 10800, 14400, 28800, 86400}

// Collectors bundles the prometheus collectors fed by hotload hooks.
type Collectors struct {
	// SqlStmtsSummary tracks the number of sql statements per transaction
	// by statement type and the grpc service/method labels carried by the
	// transaction context (see hotload.ContextWithExecLabels).
	SqlStmtsSummary *prometheus.SummaryVec
	// HotloadChangeTotal counts config changes per hotload DSN.
	HotloadChangeTotal *prometheus.CounterVec
	// HotloadLastChangedTimestampSeconds is the unix timestamp of the last
	// config change per hotload DSN.
	HotloadLastChangedTimestampSeconds *prometheus.GaugeVec
	// HotloadModtimeLatencyHistogram tracks how stale watched files are,
	// fed by the modtime monitor.
	HotloadModtimeLatencyHistogram *prometheus.HistogramVec
	// HotloadPathChksumTimestampSeconds reports when each watched file's
	// content checksum last changed, computed at scrape time. Gated by the
	// HOTLOAD_PATH_CHKSUM_METRICS_ENABLE environment variable.
	HotloadPathChksumTimestampSeconds *PathChksumCollector
}

// NewCollectors creates unregistered collectors with hotload v1's metric
// names.
func NewCollectors() *Collectors {
	return &Collectors{
		SqlStmtsSummary: prometheus.NewSummaryVec(prometheus.SummaryOpts{
			Name: SqlStmtsSummaryName,
			Help: "The number of sql stmts called in a transaction by statement type per grpc service and method",
		}, []string{GRPCServiceKey, GRPCMethodKey, StatementKey}),
		HotloadChangeTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: HotloadChangeTotalName,
			Help: "Hotload change total by url",
		}, []string{UrlKey}),
		HotloadLastChangedTimestampSeconds: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: HotloadLastChangedTimestampSecondsName,
			Help: "Hotload last changed (unix timestamp), by url",
		}, []string{UrlKey}),
		HotloadModtimeLatencyHistogram: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    HotloadModtimeLatencyHistogramName,
			Help:    "Hotload modtime latency histogram (seconds) by strategy and path",
			Buckets: HotloadModtimeLatencyHistogramDefBuckets,
		}, []string{StrategyKey, PathKey}),
		HotloadPathChksumTimestampSeconds: NewPathChksumCollector(DefaultFileHasher),
	}
}

// All returns every collector, for manual registration.
func (c *Collectors) All() []prometheus.Collector {
	return []prometheus.Collector{
		c.SqlStmtsSummary,
		c.HotloadChangeTotal,
		c.HotloadLastChangedTimestampSeconds,
		c.HotloadModtimeLatencyHistogram,
		c.HotloadPathChksumTimestampSeconds,
	}
}

// Hooks returns the hotload hooks that feed the collectors. Register them
// with hotload.RegisterHooks (EnablePrometheus does this for you).
func (c *Collectors) Hooks() hotload.Hooks {
	return hotload.Hooks{
		OnConfigChange: func(ev hotload.ConfigChangeEvent) {
			c.HotloadChangeTotal.WithLabelValues(ev.GroupName).Inc()
			c.HotloadLastChangedTimestampSeconds.WithLabelValues(ev.GroupName).Set(float64(ev.At.Unix()))
		},
		OnTxComplete: func(ev hotload.TxEvent) {
			labels := hotload.GetExecLabelsFromContext(ev.Ctx)
			service := labels[GRPCServiceKey]
			method := labels[GRPCMethodKey]
			c.SqlStmtsSummary.WithLabelValues(service, method, ExecStatement).Observe(float64(ev.ExecStmts))
			c.SqlStmtsSummary.WithLabelValues(service, method, QueryStatement).Observe(float64(ev.QueryStmts))
		},
		OnModTimeCheck: func(ev hotload.ModTimeEvent) {
			c.HotloadModtimeLatencyHistogram.WithLabelValues(ev.Strategy, ev.Path).Observe(ev.Latency.Seconds())
		},
		OnWatch: func(ev hotload.WatchEvent) {
			if !ev.Closed {
				c.HotloadPathChksumTimestampSeconds.AddPath(ev.Path)
			}
		},
	}
}

// EnablePrometheus creates the collectors, registers them with reg (the
// default prometheus registerer when reg is nil), and registers the hooks
// that feed them with hotload. Call it once during program initialization,
// before opening hotload connections.
func EnablePrometheus(reg prometheus.Registerer) (*Collectors, error) {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}
	c := NewCollectors()
	for _, collector := range c.All() {
		if err := reg.Register(collector); err != nil {
			return nil, err
		}
	}
	hotload.RegisterHooks(c.Hooks())
	return c, nil
}

// MustEnablePrometheus is EnablePrometheus, panicking on registration
// errors.
func MustEnablePrometheus(reg prometheus.Registerer) *Collectors {
	c, err := EnablePrometheus(reg)
	if err != nil {
		panic(err)
	}
	return c
}
