package metrics

// Metric label keys
const (
	GRPCMethodKey  = "grpc_method"
	GRPCServiceKey = "grpc_service"
	StatementKey   = "stmt"
	ExecStatement  = "exec"
	QueryStatement = "query"
	StrategyKey    = "strategy"
	PathKey        = "path"
	UrlKey         = "url"
)

// Provider is an interface for metrics collection.
// This allows the hotload library to work without prometheus dependencies
// by using the default NoOpProvider.
type Provider interface {
	// ObserveHotloadModtimeLatencyHistogram records modtime latency
	ObserveHotloadModtimeLatencyHistogram(strategy, path string, val float64)

	// IncHotloadChangeTotal increments the change counter
	IncHotloadChangeTotal(url string)

	// SetHotloadLastChangedTimestampSeconds sets the last changed timestamp
	SetHotloadLastChangedTimestampSeconds(url string, val float64)

	// ObserveSQLStmtsSummary records SQL statement counts per transaction
	ObserveSQLStmtsSummary(grpcService, grpcMethod, stmtType string, count float64)
}

var currentProvider Provider = &NoOpProvider{}

// SetProvider allows users to optionally set a metrics provider.
// If not set, a no-op provider is used by default.
func SetProvider(p Provider) {
	if p != nil {
		currentProvider = p
	}
}

// GetProvider returns the current metrics provider.
func GetProvider() Provider {
	return currentProvider
}

// ObserveHotloadModtimeLatencyHistogram calls the current provider
func ObserveHotloadModtimeLatencyHistogram(strategy, path string, val float64) {
	currentProvider.ObserveHotloadModtimeLatencyHistogram(strategy, path, val)
}

// IncHotloadChangeTotal calls the current provider
func IncHotloadChangeTotal(url string) {
	currentProvider.IncHotloadChangeTotal(url)
}

// SetHotloadLastChangedTimestampSeconds calls the current provider
func SetHotloadLastChangedTimestampSeconds(url string, val float64) {
	currentProvider.SetHotloadLastChangedTimestampSeconds(url, val)
}

// ObserveSQLStmtsSummary calls the current provider
func ObserveSQLStmtsSummary(grpcService, grpcMethod, stmtType string, count float64) {
	currentProvider.ObserveSQLStmtsSummary(grpcService, grpcMethod, stmtType, count)
}

// NoOpProvider is a no-op implementation of Provider
type NoOpProvider struct{}

func (n *NoOpProvider) ObserveHotloadModtimeLatencyHistogram(strategy, path string, val float64) {}
func (n *NoOpProvider) IncHotloadChangeTotal(url string)                                         {}
func (n *NoOpProvider) SetHotloadLastChangedTimestampSeconds(url string, val float64)            {}
func (n *NoOpProvider) ObserveSQLStmtsSummary(grpcService, grpcMethod, stmtType string, count float64) {
}
