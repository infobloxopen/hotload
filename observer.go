package hotload

import (
	"context"
	"sync"
	"time"
)

// ConfigChangeEvent is emitted when a strategy reports a new connection
// string for a watched hotload DSN.
type ConfigChangeEvent struct {
	// GroupName is the full hotload DSN (e.g. "fsnotify://postgres/etc/dsn").
	GroupName string
	// OldRedactedDSN and NewRedactedDSN are the previous and new underlying
	// connection strings with credentials redacted.
	OldRedactedDSN string
	NewRedactedDSN string
	// ForceKill reports whether the group closes old connections immediately.
	ForceKill bool
	At        time.Time
}

// ConnEvent is emitted when hotload opens or closes an underlying connection.
type ConnEvent struct {
	GroupName   string
	RedactedDSN string
	// Killed is true on close events caused by a config change (rather than
	// the pool retiring the connection).
	Killed bool
}

// TxEvent is emitted when a transaction completes. ExecStmts and QueryStmts
// are the number of exec and query statements observed on the connection
// since the previous transaction completed.
type TxEvent struct {
	// Ctx is the context the transaction was started with; adapters can
	// extract labels from it with GetExecLabelsFromContext.
	Ctx        context.Context
	ExecStmts  int64
	QueryStmts int64
	Committed  bool
}

// WatchEvent is emitted when a strategy watch is established or closed.
type WatchEvent struct {
	GroupName string
	Strategy  string
	Path      string
	Closed    bool
}

// ModTimeEvent is emitted by the modtime monitor when it samples the
// modification time of a watched path.
type ModTimeEvent struct {
	Strategy string
	Path     string
	// Latency is the time elapsed since the file was last modified.
	Latency time.Duration
}

// Hooks receives notifications about hotload activity. All fields are
// optional; nil fields are skipped. Hooks must be fast and must not call
// back into hotload. Adapters (e.g. the observability module) use Hooks to
// export metrics without hotload depending on any metrics library.
type Hooks struct {
	OnConfigChange func(ConfigChangeEvent)
	OnConnOpen     func(ConnEvent)
	OnConnClose    func(ConnEvent)
	OnTxComplete   func(TxEvent)
	OnWatch        func(WatchEvent)
	OnModTimeCheck func(ModTimeEvent)
}

var hooksMu sync.RWMutex
var hooks []Hooks

// RegisterHooks adds h to the set of registered hooks. Hooks cannot be
// unregistered; register them once during program initialization, before
// opening connections.
func RegisterHooks(h Hooks) {
	hooksMu.Lock()
	defer hooksMu.Unlock()
	hooks = append(hooks, h)
}

// resetHooks removes all registered hooks. For tests.
func resetHooks() {
	hooksMu.Lock()
	defer hooksMu.Unlock()
	hooks = nil
}

// hooksRegistered reports whether any hooks have been registered.
func hooksRegistered() bool {
	hooksMu.RLock()
	defer hooksMu.RUnlock()
	return len(hooks) != 0
}

func snapshotHooks() []Hooks {
	hooksMu.RLock()
	defer hooksMu.RUnlock()
	return hooks
}

func emitConfigChange(ev ConfigChangeEvent) {
	for _, h := range snapshotHooks() {
		if h.OnConfigChange != nil {
			h.OnConfigChange(ev)
		}
	}
}

func emitConnOpen(ev ConnEvent) {
	for _, h := range snapshotHooks() {
		if h.OnConnOpen != nil {
			h.OnConnOpen(ev)
		}
	}
}

func emitConnClose(ev ConnEvent) {
	for _, h := range snapshotHooks() {
		if h.OnConnClose != nil {
			h.OnConnClose(ev)
		}
	}
}

func emitTxComplete(ev TxEvent) {
	for _, h := range snapshotHooks() {
		if h.OnTxComplete != nil {
			h.OnTxComplete(ev)
		}
	}
}

// EmitWatchEvent notifies registered hooks that a strategy watch was
// established or closed. It is exported for strategy implementations
// (e.g. the fsnotify subpackage).
func EmitWatchEvent(ev WatchEvent) {
	for _, h := range snapshotHooks() {
		if h.OnWatch != nil {
			h.OnWatch(ev)
		}
	}
}

// EmitModTimeEvent notifies registered hooks of a modtime sample. It is
// exported for the modtime subpackage.
func EmitModTimeEvent(ev ModTimeEvent) {
	for _, h := range snapshotHooks() {
		if h.OnModTimeCheck != nil {
			h.OnModTimeCheck(ev)
		}
	}
}
