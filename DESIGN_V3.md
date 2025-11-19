# Hotload V3 Design

## Goals

1. **Robust fsnotify strategy** with edge case handling:
   - File removal and re-addition
   - Atomic file replacement (unix mv)
   - Hash-based change detection (only send changes)

2. **Epoch-based DSN tracking**:
   - New connections use new epoch
   - Old epochs gracefully wound down
   - Clear visibility into epoch transitions

3. **Opportunistic connection closing**:
   - Wrapped connections hint to sql library via driver.ErrBadConn
   - Cannot directly close (must be done by goroutine using connection)
   - Wrapped result objects also participate

4. **Minimal root package dependencies**:
   - Core Strategy interface in root
   - Test/validation dependencies in separate package with own go.mod
   - Logging and telemetry isolated

5. **Goroutine safety and resource management**:
   - No goroutine leaks
   - No resource leaks
   - No deadlocks
   - Proper synchronization

## Architecture

### Core Types (root package)

```go
// Strategy interface - minimal, no external dependencies
type Strategy interface {
    // Watch returns initial DSN and channel for updates
    // Context cancellation stops watching
    Watch(ctx context.Context, path string, query url.Values) (initial string, updates <-chan string, err error)
}

// Epoch represents a DSN version
type Epoch uint64

// Connection tracking
type epochTracker struct {
    mu sync.RWMutex
    current Epoch
    dsn map[Epoch]string
    conns map[Epoch][]*wrappedConn
}
```

### FSNotify Strategy

```go
// Handles edge cases:
// 1. File removal: Keep retrying with exponential backoff
// 2. Atomic mv: Re-add watch after REMOVE/CREATE events
// 3. Hash-based: Only send update if content hash changed

type fsnotifyStrategy struct {
    mu sync.RWMutex
    watches map[string]*fileWatch
    watcher *fsnotify.Watcher
}

type fileWatch struct {
    path string
    hash [32]byte  // SHA-256 of content
    subscribers []chan<- string
    ctx context.Context
    cancel context.CancelFunc
}
```

### Connection Wrapping

```go
// wrappedConn tracks epoch and provides opportunistic closing
type wrappedConn struct {
    conn driver.Conn
    epoch Epoch
    closed atomic.Bool
    tracker *epochTracker
}

// ResetSession returns driver.ErrBadConn when epoch is old
func (w *wrappedConn) ResetSession(ctx context.Context) error {
    if w.tracker.isOldEpoch(w.epoch) {
        return driver.ErrBadConn  // Hint to sql library to discard
    }
    // ... delegate to underlying conn
}
```

### Epoch Lifecycle

1. **DSN Change Detected**:
   - Increment epoch counter
   - Store new DSN for new epoch
   - Mark old epoch as "draining"

2. **New Connection Request**:
   - Use current epoch's DSN
   - Register connection with epoch tracker

3. **Old Epoch Winding Down**:
   - Connections return driver.ErrBadConn on ResetSession
   - sql library naturally discards and creates new connections
   - Track when all connections for epoch are closed

4. **Visibility**:
   - Log epoch transitions
   - Track connection count per epoch
   - Expose metrics (if telemetry package available)

## Package Structure

```
hotload/
├── driver.go           # Core driver, minimal deps
├── strategy.go         # Strategy interface
├── epoch.go            # Epoch tracking
├── conn.go             # Connection wrapping
├── fsnotify/
│   ├── strategy.go     # FSNotify implementation
│   └── strategy_test.go
├── observability/      # Separate go.mod
│   ├── go.mod
│   ├── logger.go
│   ├── metrics.go
│   └── integration_test.go
```

## Goroutine Safety

1. **No shared mutable state without locks**
2. **Lock ordering**: Always acquire locks in same order
3. **No locks held during channel operations**
4. **Context cancellation for cleanup**
5. **WaitGroups for goroutine lifecycle tracking**

## Resource Leak Prevention

1. **All goroutines have clear termination conditions**
2. **Context cancellation propagates to all watchers**
3. **Channels closed when no longer needed**
4. **File watchers properly removed**
5. **Connections tracked and cleaned up**
