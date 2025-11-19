# Hotload V3 Integration Tests

This directory contains comprehensive integration tests for hotload v3 that test multiple PostgreSQL drivers (libpq, pgx/v4, pgx/v5) with real database connections.

## Test Coverage

### Driver Matrix Tests (`driver_matrix_test.go`)
Tests basic functionality across all supported drivers:
- **BasicConnection**: Verify basic connectivity
- **Ping**: Test Ping/PingContext methods
- **PreparedStatements**: Test prepared statement execution
- **Transactions**: Test transaction commit and rollback

### Reload Tests (`reload_test.go`)
Tests DSN reloading and epoch transitions:
- **BasicDSNChange**: Verify DSN changes are detected via fsnotify
- **LongRunningQuery**: Test queries during DSN changes
- **MultipleConsecutiveChanges**: Test handling of multiple DSN changes
- **ConnectionPoolBehavior**: Test connection pool during epoch transitions

### Stress Tests (`stress_test.go`)
Tests scale, concurrency, and race conditions:
- **ConcurrentQueriesWithDSNChanges**: 20 workers + 5 DSN changes
- **RapidDSNChanges**: 10 rapid DSN changes (50ms apart)
- **LongRunningTransactionDuringReload**: 3-second transaction during DSN change
- **HighConcurrencyConnectionPool**: 50 workers testing connection pool
- **RaceDetection**: Designed to trigger race conditions (run with `-race`)

## Running Tests

### Prerequisites
- Docker installed and running (for testcontainers)
- Go 1.23+ installed

### Run All Tests
```bash
cd test/integration
go test -v -race -timeout=10m ./...
```

### Run Specific Test Categories
```bash
# Driver matrix tests only
go test -v -race -run TestDriverMatrix

# Reload tests only
go test -v -race -run TestReload

# Stress tests only (skipped in -short mode)
go test -v -race -run TestStress
```

### Skip Stress Tests
```bash
go test -v -race -short ./...
```

### Use External PostgreSQL
```bash
export PG_DSN="host=localhost port=5432 user=postgres password=postgres dbname=testdb sslmode=disable"
go test -v -race ./...
```

## Test Architecture

### Separate Module
Integration tests use a separate `go.mod` with a `replace` directive to keep the root module dependency tree clean:
```go
replace github.com/infobloxopen/hotload => ../..
```

### Automatic Container Management
Tests use [testcontainers-go](https://github.com/testcontainers/testcontainers-go) to automatically start/stop PostgreSQL:
- Image: `postgres:16-alpine`
- Automatic port mapping to avoid conflicts
- Cleanup on test completion
- Graceful degradation if Docker unavailable

### Test Isolation
- Each test creates its own temporary DSN config file
- Unique driver names per test (timestamp-based)
- Tests can run in parallel with `-parallel` flag

## CI/CD Integration

### GitHub Actions
The workflow runs integration tests on the v3 branch:
```yaml
- name: Run Integration Tests
  working-directory: test/integration
  run: go test -v -race -timeout=10m ./...
```

### Docker Requirement
CI runners must have Docker available. The workflow uses the default GitHub Actions runner which includes Docker.

## Test Results

Example output from stress test:
```
📊 Stress Test Results:
   🔄 DSN changes: 5
   📝 Total queries attempted: 247
   ✓ Successful queries: 231 (93.5%)
   ✗ Failed queries: 16 (6.5%)
✓ Success rate: 93.5%
✅ Stress test passed
```

## Drivers Tested

### lib/pq (libpq)
- Pure Go PostgreSQL driver
- Stable, widely used
- Import: `github.com/lib/pq`

### pgx/v4
- High-performance PostgreSQL driver
- Version 4.x
- Import: `github.com/jackc/pgx/v4/stdlib`

### pgx/v5
- Latest pgx version
- Enhanced features and performance
- Import: `github.com/jackc/pgx/v5/stdlib`

## Key Features Tested

### Epoch-Based DSN Tracking
- Verify old epochs are cleaned up when connections close
- Verify new connections use new epoch after DSN change
- Verify epoch transitions don't cause panics or deadlocks

### Opportunistic Connection Closing
- Connections return `driver.ErrBadConn` for old epochs
- `database/sql` handles connection replacement
- In-flight operations can complete before closing

### Hash-Based Change Detection
- Only actual DSN changes trigger reloads
- File touch without content change is ignored
- Reduces unnecessary reconnections

### FSNotify Edge Cases
- File removal and re-addition
- Atomic file replacement (unix `mv`)
- Rapid consecutive changes

## Troubleshooting

### Docker Not Available
If Docker is not available, tests will skip with:
```
Skipping integration tests (Docker not available)
```

### Container Startup Timeout
If PostgreSQL container takes too long to start:
- Check Docker daemon is running
- Check available disk space
- Check network connectivity for image pull

### Test Failures
- Check PostgreSQL logs: `docker logs <container_id>`
- Run with verbose output: `go test -v`
- Run with race detector: `go test -race`
- Check for port conflicts: `docker ps`

## Performance Considerations

### Test Duration
- Basic tests: ~5-10 seconds
- Reload tests: ~10-20 seconds
- Stress tests: ~30-60 seconds
- Full suite: ~2-3 minutes

### Resource Usage
- Memory: ~500MB (PostgreSQL container + tests)
- CPU: Moderate (concurrent workers)
- Disk: ~100MB (PostgreSQL data + image)

## Future Enhancements

Potential additions:
- [ ] MySQL driver support
- [ ] Connection leak detection tests
- [ ] Performance benchmarks
- [ ] Chaos testing (network failures, container kills)
- [ ] Metrics validation tests
- [ ] Logging validation tests
