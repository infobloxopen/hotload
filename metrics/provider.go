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

	// IncHotloadConnectionsDrained increments the drained connections counter
	IncHotloadConnectionsDrained(driverName string)

	// IncHotloadConnectionsKilled increments the killed connections counter with reason
	IncHotloadConnectionsKilled(driverName, reason string)

	// SetHotloadEpoch sets the current epoch gauge
	SetHotloadEpoch(driverName string, epoch uint64)

	// ObserveHotloadReloadSeconds records the duration of a DSN reload
	ObserveHotloadReloadSeconds(driverName string, duration float64)
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

// IncHotloadConnectionsDrained calls the current provider
func IncHotloadConnectionsDrained(driverName string) {
	currentProvider.IncHotloadConnectionsDrained(driverName)
}

// IncHotloadConnectionsKilled calls the current provider
func IncHotloadConnectionsKilled(driverName, reason string) {
	currentProvider.IncHotloadConnectionsKilled(driverName, reason)
}

// SetHotloadEpoch calls the current provider
func SetHotloadEpoch(driverName string, epoch uint64) {
	currentProvider.SetHotloadEpoch(driverName, epoch)
}

// ObserveHotloadReloadSeconds calls the current provider
func ObserveHotloadReloadSeconds(driverName string, duration float64) {
	currentProvider.ObserveHotloadReloadSeconds(driverName, duration)
}

// NoOpProvider is a no-op implementation of Provider
type NoOpProvider struct{}

func (n *NoOpProvider) ObserveHotloadModtimeLatencyHistogram(strategy, path string, val float64) {
	// No-op implementation
}
func (n *NoOpProvider) IncHotloadChangeTotal(url string) {
	// No-op implementation
}
func (n *NoOpProvider) SetHotloadLastChangedTimestampSeconds(url string, val float64) {
	// No-op implementation
}
func (n *NoOpProvider) ObserveSQLStmtsSummary(grpcService, grpcMethod, stmtType string, count float64) {
	// No-op implementation
}
func (n *NoOpProvider) IncHotloadConnectionsDrained(driverName string) {
	// No-op implementation
}
func (n *NoOpProvider) IncHotloadConnectionsKilled(driverName, reason string) {
	// No-op implementation
}
func (n *NoOpProvider) SetHotloadEpoch(driverName string, epoch uint64) {
	// No-op implementation
}
func (n *NoOpProvider) ObserveHotloadReloadSeconds(driverName string, duration float64) {
	// No-op implementation
}
