# Hotload Integration Tests

This directory contains integration tests for the hotload database driver using real PostgreSQL instances via testcontainers.

## Test Coverage

### Basic Functionality Tests

#### `TestLibPQ_BasicConnection` / `TestPGX_BasicConnection`
- Verifies basic connectivity through hotload with lib/pq and pgx drivers
- Tests that simple queries work correctly
- Validates driver registration and connection opening

#### `TestLibPQ_Ping` / `TestPGX_Ping`
- Tests that `Ping()` and `PingContext()` reach the underlying driver
- Verifies connection health checks work through hotload
- Validates that the database/sql Pinger interface is properly delegated
- Ensures both lib/pq and pgx drivers receive ping requests

#### `TestLibPQ_DSNReload`
- Tests DSN hot-reloading by modifying the watched file
- Verifies new connections pick up the changed DSN
- Demonstrates zero-downtime configuration changes

#### `TestLibPQ_ConnectionPooling`
- Validates connection pool behavior with hotload
- Tests multiple concurrent connections
- Ensures pool management works correctly

#### `TestPGX_PreparedStatements`
- Tests prepared statement support through hotload
- Verifies parameter binding and execution
- Ensures pgx-specific features work

#### `TestPGX_Transactions`
- Tests transaction support (BEGIN, COMMIT, ROLLBACK)
- Verifies ACID properties are maintained
- Ensures transaction isolation works through hotload

#### `TestPGX_NativeTypes`
- Tests PostgreSQL native types (arrays, JSON, etc.)
- Verifies type conversion through database/sql
- Demonstrates handling of complex data types

### Advanced Graceful Reload Tests

#### `TestGracefulReload_LongRunningQueries` ⭐
**The core hotload feature test**

This test demonstrates hotload's key value proposition: **zero-downtime DSN changes with graceful connection draining**.

**Scenario:**
1. Establishes connections using `testuser` credentials
2. Starts 3 concurrent long-running queries (5 second `pg_sleep()`)
3. **While queries are running**, changes DSN to use `testuser2` credentials
4. Verifies all 3 queries complete successfully without interruption
5. Confirms new queries automatically use the new credentials

**Key Validations:**
- ✅ Old queries complete without errors (graceful drain)
- ✅ No queries are killed mid-flight
- ✅ New connections immediately use updated DSN
- ✅ Seamless transition with zero downtime
- ✅ Connection pool handles mixed-epoch connections

**Configuration:**
- `drainTimeout=10s` - generous timeout for graceful completion
- `forceKill=false` - never kill in-flight queries

**Use Case:** Production password rotation, database migration, credential updates

---

#### `TestGracefulReload_ForceKill`
**Tests the safety valve for stuck connections**

This test demonstrates the `forceKill` behavior when queries don't complete within the drain timeout.

**Scenario:**
1. Starts a very long query (10 seconds)
2. Changes DSN with a **short** `drainTimeout=2s`
3. Query is forcibly terminated after the timeout expires
4. Verifies new connections work after forced cleanup

**Key Validations:**
- ✅ Queries are killed after `drainTimeout` expires
- ✅ Force kill prevents indefinite waiting
- ✅ New connections work immediately after cleanup
- ✅ System recovers correctly from forced termination

**Configuration:**
- `drainTimeout=2s` - short timeout to trigger force kill
- `forceKill=true` - kill connections that don't drain in time

**Use Case:** Emergency failover, stuck queries, aggressive cutover scenarios

### Stress and Edge Case Tests

#### `TestStress_ConnectionExhaustion`
**Tests behavior under connection pool pressure**

This test validates hotload's behavior when the connection pool is exhausted.

**Scenario:**
1. Configure very small connection pool (3 max connections)
2. Start 15 workers competing for connections
3. Execute queries with random sleeps (200-600ms)
4. Measure successful vs timeout vs failed queries

**Key Validations:**
- ✅ Graceful handling of pool exhaustion (no panics)
- ✅ Queries either succeed or timeout (no hard failures)
- ✅ System remains stable under pressure
- ✅ Connection pool limits are respected

**Configuration:**
- `MaxOpenConns=3` - very small pool to stress system
- `MaxIdleConns=1` - minimal idle connections
- 15 workers with 200-600ms query duration

**Use Case:** High-concurrency scenarios, resource-constrained environments

---

#### `TestStress_ConcurrentDSNChanges`
**Tests hotload under query load with multiple DSN changes**

This test demonstrates stability when DSN changes frequently while queries are running.

**Scenario:**
1. Start 20 workers executing queries with random sleeps (50-300ms)
2. Change DSN 5 times (every 500ms) while queries run
3. Track success/failure rates across all workers
4. Verify high success rate (≥80%)

**Key Validations:**
- ✅ Queries continue during DSN transitions
- ✅ High success rate maintained (>95% typical)
- ✅ No panics or deadlocks during rapid changes
- ✅ Connection tracking handles mixed epochs correctly

**Configuration:**
- 20 concurrent workers
- 5 DSN changes during 3-second window
- `drainTimeout=5s`, `forceKill=false`

**Use Case:** Dynamic configuration management, frequent updates, blue/green deployments

---

#### `TestStress_RapidDSNChanges`
**Tests debouncing with very rapid DSN changes**

This test validates that debouncing prevents excessive reconnections.

**Scenario:**
1. Execute background queries
2. Change DSN 10 times in rapid succession (every 50ms)
3. Verify debouncing coalesces changes (250ms debounce period)
4. Confirm high success rate despite rapid changes

**Key Validations:**
- ✅ Debouncing prevents connection thrashing
- ✅ Multiple rapid changes coalesced into fewer updates
- ✅ Queries remain successful during rapid changes
- ✅ System stability maintained

**Configuration:**
- 10 DSN changes in 500ms (50ms apart)
- `debounce=250ms` - coalesces changes
- 5 background query workers

**Use Case:** Configuration management tools (Consul, etcd watches), noisy file systems

---

#### `TestStress_LongRunningTransactionDuringReload`
**Tests transaction behavior during DSN changes**

This test validates how long-running transactions handle DSN changes.

**Scenario:**
1. Start 3-second transaction (3× pg_sleep(1))
2. Change DSN after 500ms (mid-transaction)
3. Verify graceful handling (may succeed or fail gracefully)
4. Confirm new queries work after transaction completes

**Key Validations:**
- ✅ No panics during transaction DSN change
- ✅ System remains stable even if transaction fails
- ✅ New queries work immediately after change
- ✅ Graceful error handling for disrupted transactions

**Configuration:**
- `drainTimeout=30s` - generous timeout
- `forceKill=false` - attempt graceful completion

**Note:** In-flight transactions may experience "bad connection" errors when DSN changes, as the underlying connection is being replaced. This is expected behavior.

**Use Case:** Understanding transaction limits during hot reload, planning maintenance windows

## Running the Tests

### Run all integration tests:
```bash
cd test/integration
go test -v -timeout 10m
```

### Run specific test:
```bash
go test -v -run TestGracefulReload_LongRunningQueries -timeout 10m
```

### Run with coverage:
```bash
go test -v -timeout 10m -coverprofile=coverage.out \
  -coverpkg=github.com/infobloxopen/hotload,github.com/infobloxopen/hotload/internal/...
go tool cover -html=coverage.out
```

### Skip in short mode:
```bash
go test -short  # Skips integration tests
```

## Requirements

- Docker or compatible container runtime (Podman, Rancher Desktop)
- Go 1.20+
- testcontainers-go handles automatic PostgreSQL setup

## Test Container

Tests automatically:
1. Pull `postgres:16-alpine` image
2. Start PostgreSQL container with test credentials
3. Wait for database to be ready
4. Run tests
5. Clean up container (unless test fails)

**Container Lifecycle:** ~3-4 seconds startup, automatic cleanup

## Hotload URL Format

```
hotload DSN: fsnotify://<driver>/<path-to-file>?<options>

Examples:
  fsnotify://pgx//tmp/db.dsn?drainTimeout=5s&forceKill=false
  fsnotify://postgres//etc/app/db.conf?drainTimeout=30s&forceKill=true
```

**URL Parameters:**
- `drainTimeout` - How long to wait for in-flight queries before force kill (default: 30s)
- `forceKill` - Whether to kill connections after drainTimeout expires (default: true)
- `debounce` - Delay before processing DSN changes to batch rapid updates (default: 250ms)
- `preconnect` - Test new DSN before switching (default: false)

## Real-World Scenarios Tested

1. **Password Rotation** (`TestGracefulReload_LongRunningQueries`)
   - Rotate database credentials without downtime
   - Long-running reports/analytics continue uninterrupted
   - New queries automatically use new credentials

2. **Database Migration** (`TestLibPQ_DSNReload`)
   - Switch from old to new database host
   - Connection pool transitions gracefully
   - Zero query failures during cutover

3. **Emergency Failover** (`TestGracefulReload_ForceKill`)
   - Force connection cleanup when needed
   - Aggressive timeout for rapid failover
   - System recovers immediately after force kill

4. **Connection Pool Management** (`TestLibPQ_ConnectionPooling`)
   - Multiple concurrent connections through hotload
   - Pool respects database/sql limits
   - Connections properly tracked and cleaned up

## Key Learnings

1. **Coverage requires `internal/...`**: The modern implementation lives in `internal/core`, `internal/connwrap`, etc. Include these packages in `-coverpkg` to see actual coverage (42.9% vs 5.9%).

2. **Go 1.20+ uses OpenConnector**: The `database/sql` package prefers `OpenConnector()` over `Open()` when available. All tests use this modern path.

3. **fsnotify URL format**: Must include `/` between driver and path: `fsnotify://pgx/` + `/path/to/file` (not `fsnotify://pgx` + `/path`).

4. **Array types through database/sql**: PostgreSQL arrays are returned as strings in format `{val1,val2}` when using `database/sql`, not as Go slices.

5. **Graceful drain is the default**: Set `forceKill=false` and generous `drainTimeout` for production password rotation scenarios.

## Architecture Verified

These tests validate the complete hotload architecture:

```
sql.Open("hotload", "fsnotify://pgx/file?opts")
    ↓
driver.OpenConnector() 
    ↓
internal/core.Connector
    ↓
internal/engine.Engine (epoch management, deduplication)
    ↓
internal/connwrap.Conn (capability-preserving wrapper)
    ↓
Real Driver (pgx/lib/pq)
    ↓
PostgreSQL
```

File changes trigger:
```
fsnotify detects change
    ↓
Strategy.Watch() emits update
    ↓
Engine.Update() creates new epoch
    ↓
ConnTrack drains old epoch connections
    ↓
New connections use new DSN
```
