# Hotload V3 - Major Rewrite

## Overview

V3 is a complete rewrite of the hotload driver with focus on:
- **Robust edge case handling** in fsnotify strategy
- **Epoch-based DSN tracking** for graceful connection transitions
- **Opportunistic connection closing** via driver.ErrBadConn
- **Minimal dependencies** in root package
- **Strong goroutine safety** guarantees

## Key Changes from V1/V2

### 1. Simplified Strategy Interface

```go
type Strategy interface {
    // Watch returns initial DSN and channel for updates
    // Context cancellation stops watching
    Watch(ctx context.Context, path string, query url.Values) (initial string, updates <-chan string, err error)
}
```

**Changes:**
- Removed `CloseWatch()` - use context cancellation instead
- Removed `Close()` - cleanup happens automatically
- Simpler contract: one method, clear lifecycle

### 2. Epoch-Based DSN Tracking

```go
type Epoch uint64

type epochTracker struct {
    current Epoch
    dsn     map[Epoch]string
    conns   map[Epoch][]*wrappedConn
}
```

**Benefits:**
- Clear visibility into which connections belong to which DSN version
- Automatic cleanup when old epochs are fully drained
- Metrics and logging for epoch transitions

### 3. Opportunistic Connection Closing

```go
func (wc *wrappedConn) ResetSession(ctx context.Context) error {
    if wc.tracker.isOldEpoch(wc.epoch) {
        return driver.ErrBadConn  // Hint to sql library to discard
    }
    // ... delegate to underlying conn
}
```

**How it works:**
1. DSN changes → new epoch created
2. New connections use new epoch's DSN
3. Old connections return `driver.ErrBadConn` on `ResetSession()`
4. sql library naturally discards old connections
5. Old epoch cleaned up when all connections closed

**No forced closing:** Connections are never forcibly closed from another goroutine, avoiding race conditions and ensuring operations complete naturally.

### 4. FSNotify Edge Case Handling

**File Removal and Re-addition:**
```go
// Periodic recheck handles missed events
ticker := time.NewTicker(2 * time.Second)
for {
    case <-ticker.C:
        s.recheckAllFiles()
}
```

**Atomic File Replacement (unix mv):**
```go
if event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
    time.Sleep(10 * time.Millisecond)
    s.watcher.Remove(path)
    s.watcher.Add(path)  // Re-add watch
}
```

**Hash-Based Change Detection:**
```go
newHash := sha256.Sum256([]byte(content))
if oldHash == newHash {
    return  // No change, don't send update
}
```

### 5. Minimal Dependencies

**Root package (`go.mod`):**
```go
require (
    github.com/fsnotify/fsnotify v1.6.0
)
```

**Observability package (`observability/go.mod`):**
```go
require (
    github.com/infobloxopen/hotload v0.0.0
    github.com/prometheus/client_golang v1.20.0
)
```

**Benefits:**
- Core hotload has minimal transitive dependencies
- Logging/metrics isolated in separate module
- Easier to vendor and audit

## Usage

### Basic Usage

```go
import (
    "database/sql"
    "github.com/infobloxopen/hotload"
    _ "github.com/infobloxopen/hotload/fsnotify"
    "github.com/lib/pq"
)

func init() {
    hotload.RegisterSQLDriver("postgres", &pq.Driver{})
}

func main() {
    db, err := sql.Open("hotload", "fsnotify://postgres/tmp/config.txt")
    if err != nil {
        log.Fatal(err)
    }
    defer db.Close()
    
    // Use db normally - DSN changes are handled automatically
    rows, err := db.Query("SELECT * FROM users")
    // ...
}
```

### With Observability

```go
import (
    "github.com/infobloxopen/hotload/observability"
)

func init() {
    // Set logger for visibility
    observability.SetLogger(observability.DefaultLogger())
    
    // Metrics are automatically exported via Prometheus
}
```

### Custom Strategy

```go
type MyStrategy struct{}

func (s *MyStrategy) Watch(ctx context.Context, path string, query url.Values) (string, <-chan string, error) {
    updates := make(chan string)
    
    // Read initial value
    initial := readFromSource(path)
    
    // Start watching in background
    go func() {
        defer close(updates)
        for {
            select {
            case <-ctx.Done():
                return
            case newValue := <-myWatcher:
                updates <- newValue
            }
        }
    }()
    
    return initial, updates, nil
}

func init() {
    hotload.RegisterStrategy("mysource", &MyStrategy{})
}
```

## Migration from V1/V2

### Strategy Interface Changes

**V1/V2:**
```go
type Strategy interface {
    Watch(ctx context.Context, pth string, pathQry string) (value string, values <-chan string, err error)
    CloseWatch(pth string, pathQry string) error
    Close()
}
```

**V3:**
```go
type Strategy interface {
    Watch(ctx context.Context, path string, query url.Values) (initial string, updates <-chan string, err error)
}
```

**Migration:**
- Remove `CloseWatch()` and `Close()` implementations
- Use context cancellation for cleanup
- Change `pathQry string` to `query url.Values`

### No More forceKill Option

V3 uses opportunistic closing exclusively. The `forceKill` query parameter is no longer needed or supported.

**Why:** Opportunistic closing is safer and avoids race conditions while still ensuring old connections are eventually discarded.

## Testing

### Run Tests

```bash
# Unit tests
go test ./...

# With race detector
go test -race ./...

# Specific package
go test -v ./fsnotify
```

### Test Coverage

- ✅ FSNotify edge cases (file removal, atomic mv, hash-based)
- ✅ Epoch transitions and cleanup
- ✅ Concurrent access to epoch tracker
- ✅ Connection lifecycle
- ✅ Context cancellation

## Goroutine Safety

See [GOROUTINE_SAFETY_REVIEW.md](GOROUTINE_SAFETY_REVIEW.md) for detailed analysis.

**Summary:**
- ✅ No data races
- ✅ No deadlock risks
- ✅ No resource leaks
- ✅ Proper cleanup on shutdown
- ✅ All edge cases handled

## Architecture

```
hotload/
├── strategy.go         # Strategy interface (minimal deps)
├── epoch.go           # Epoch tracking
├── conn.go            # Connection wrapping
├── driver.go          # Core driver
├── fsnotify/
│   ├── strategy.go    # FSNotify implementation
│   └── strategy_test.go
└── observability/     # Separate go.mod
    ├── go.mod
    ├── logger.go
    └── metrics.go
```

## Metrics (Optional)

When using the observability package:

- `hotload_epoch{connection}` - Current epoch
- `hotload_epoch_transitions_total{connection}` - Total DSN changes
- `hotload_connections_per_epoch{connection,epoch}` - Active connections per epoch
- `hotload_old_epoch_connections_total{connection}` - Discarded connections

## Logging (Optional)

When using the observability package:

```go
observability.SetLogger(func(format string, args ...interface{}) {
    log.Printf(format, args...)
})
```

Example output:
```
epoch 1 created with initial DSN
epoch 1 -> 2: DSN updated (old epoch has 5 connections)
epoch 1: unregistered connection (remaining: 4)
epoch 1: unregistered connection (remaining: 3)
...
epoch 1: fully drained and cleaned up
```

## Performance

- **Lock-free reads** for current epoch (atomic operations)
- **Minimal lock contention** (fine-grained locking)
- **No blocking operations** while holding locks
- **Efficient hash-based change detection** (SHA-256)

## Future Enhancements

Potential improvements for future versions:
- Configurable retry strategy for file operations
- Circuit breaker for persistent file read failures
- Metrics for goroutine count per connector
- Configurable timeout for file watch initialization
- Support for credential-only reload optimization

## Contributing

When contributing to v3:
1. Run tests with `-race` flag
2. Review [GOROUTINE_SAFETY_REVIEW.md](GOROUTINE_SAFETY_REVIEW.md)
3. Ensure no new dependencies in root package
4. Add tests for new edge cases
5. Update documentation

## License

Same as hotload project (Apache 2.0)
