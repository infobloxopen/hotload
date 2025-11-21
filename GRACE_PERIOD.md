# Grace Period Configuration

The grace period feature helps reduce query failures during rapid DSN changes by allowing old connections to complete in-flight queries before being marked as "old" and discarded.

## How It Works

When a DSN changes:
1. **New connections are created immediately** using the new DSN
2. **Old connections enter a grace period** during which they can still complete queries
3. After the grace period expires, old connections are marked as "old" and gradually drained

## Configuration

Add the `grace_period` query parameter to your hotload connection string:

```go
// Default grace period (10s)
db, err := sql.Open("hotload", "fsnotify://postgres//tmp/myconfig.txt")

// Custom grace period (5s) - shorter for faster draining
db, err := sql.Open("hotload", "fsnotify://postgres//tmp/myconfig.txt?grace_period=5s")

// Custom grace period (30s) - longer for slower queries
db, err := sql.Open("hotload", "fsnotify://postgres//tmp/myconfig.txt?grace_period=30s")

// No grace period (0) - immediate connection draining
db, err := sql.Open("hotload", "fsnotify://postgres//tmp/myconfig.txt?grace_period=0")
```

## Performance Impact

### Before Grace Period (v3.0.0)
- ~14% query failures during rapid DSN changes (10 changes in 500ms)
- Queries completing during epoch transition would see "sql: no rows in result set"

### After Grace Period (v3.1.0+)
- ~2-3% query failures with default 10s grace period
- 85% reduction in errors during rapid changes

## Choosing the Right Value

- **Default (10s)**: Good for most production applications, allows long-running queries to complete
- **5s**: Faster connection draining while still protecting most queries
- **30s+**: For applications with very slow queries or batch operations
- **0 (disabled)**: Use when you want immediate connection draining and can tolerate higher error rates during transitions

## Tradeoffs

**Pros:**
- Significantly reduces query failures during DSN changes
- Allows in-flight queries to complete successfully
- Configurable per application needs

**Cons:**
- Slightly delays connection draining (by grace period amount)
- Old connections remain in pool longer during transitions
- May consume more resources during rapid changes

## Example Scenarios

### High-Frequency Password Rotation
```go
// Default 10s grace period is usually sufficient
db, err := sql.Open("hotload", 
    "fsnotify://postgres//var/run/secrets/db-credentials")
```

### Development/Testing
```go
// Use shorter grace period for faster iteration
db, err := sql.Open("hotload", 
    "fsnotify://postgres//tmp/dev-config.txt?grace_period=1s")
```

### Production with Long-Running Queries
```go
// Use longer grace period for batch operations
db, err := sql.Open("hotload", 
    "fsnotify://postgres//etc/db-config.txt?grace_period=30s")
```
