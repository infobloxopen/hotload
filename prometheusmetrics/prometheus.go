package prometheusmetrics

import (
	"github.com/infobloxopen/hotload/metrics"
	"github.com/prometheus/client_golang/prometheus"
)

// PrometheusProvider implements the metrics.Provider interface using Prometheus metrics.
// Import this package and call Register() to enable Prometheus metrics for hotload.
type PrometheusProvider struct {
	SqlStmtsSummary                    *prometheus.SummaryVec
	HotloadModtimeLatencyHistogram     *prometheus.HistogramVec
	HotloadChangeTotal                 *prometheus.CounterVec
	HotloadLastChangedTimestampSeconds *prometheus.GaugeVec
}

// NewPrometheusProvider creates a new Prometheus metrics provider.
// After creating, call Register() to register metrics and SetProvider() to enable.
func NewPrometheusProvider() *PrometheusProvider {
	return &PrometheusProvider{
		SqlStmtsSummary: prometheus.NewSummaryVec(prometheus.SummaryOpts{
			Name: "transaction_sql_stmts",
			Help: "The number of sql stmts called in a transaction by statement type per grpc service and method",
		}, []string{"grpc_service", "grpc_method", "stmt"}),

		HotloadModtimeLatencyHistogram: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "hotload_modtime_latency_histogram",
			Help:    "Hotload modification time latency histogram (seconds) by strategy and path",
			Buckets: []float64{900, 1800, 2700, 3600, 4500, 5400, 7200, 10800, 14400, 28800, 86400},
		}, []string{"strategy", "path"}),

		HotloadChangeTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "hotload_change_total",
			Help: "Hotload change total by url",
		}, []string{"url"}),

		HotloadLastChangedTimestampSeconds: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "hotload_last_changed_timestamp_seconds",
			Help: "Hotload last changed (unix timestamp), by url",
		}, []string{"url"}),
	}
}

// Register registers all Prometheus collectors with the default registry
// and sets this provider as the active metrics provider for hotload.
func (p *PrometheusProvider) Register() error {
	collectors := []prometheus.Collector{
		p.SqlStmtsSummary,
		p.HotloadModtimeLatencyHistogram,
		p.HotloadChangeTotal,
		p.HotloadLastChangedTimestampSeconds,
	}
	for _, c := range collectors {
		if err := prometheus.Register(c); err != nil {
			return err
		}
	}

	// Set this as the active provider
	metrics.SetProvider(p)
	return nil
}

// ObserveHotloadModtimeLatencyHistogram implements metrics.Provider interface
func (p *PrometheusProvider) ObserveHotloadModtimeLatencyHistogram(strategy, path string, val float64) {
	p.HotloadModtimeLatencyHistogram.WithLabelValues(strategy, path).Observe(val)
}

// IncHotloadChangeTotal implements metrics.Provider interface
func (p *PrometheusProvider) IncHotloadChangeTotal(url string) {
	p.HotloadChangeTotal.WithLabelValues(url).Inc()
}

// SetHotloadLastChangedTimestampSeconds implements metrics.Provider interface
func (p *PrometheusProvider) SetHotloadLastChangedTimestampSeconds(url string, val float64) {
	p.HotloadLastChangedTimestampSeconds.WithLabelValues(url).Set(val)
}

// ObserveSQLStmtsSummary implements metrics.Provider interface
func (p *PrometheusProvider) ObserveSQLStmtsSummary(grpcService, grpcMethod, stmtType string, count float64) {
	p.SqlStmtsSummary.WithLabelValues(grpcService, grpcMethod, stmtType).Observe(count)
}

// GetCollectors returns all Prometheus collectors for manual registration
func (p *PrometheusProvider) GetCollectors() []prometheus.Collector {
	return []prometheus.Collector{
		p.SqlStmtsSummary,
		p.HotloadModtimeLatencyHistogram,
		p.HotloadChangeTotal,
		p.HotloadLastChangedTimestampSeconds,
	}
}

// ResetCollectors resets all metrics (useful for testing)
func (p *PrometheusProvider) ResetCollectors() {
	p.SqlStmtsSummary.Reset()
	p.HotloadModtimeLatencyHistogram.Reset()
	p.HotloadChangeTotal.Reset()
	p.HotloadLastChangedTimestampSeconds.Reset()
}
