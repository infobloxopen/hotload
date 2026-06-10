[![Go Reference](https://pkg.go.dev/badge/github.com/infobloxopen/hotload/v3.svg)](https://pkg.go.dev/github.com/infobloxopen/hotload/v3)
# hotload
Hotload is a Golang `database/sql` compatible package that supports dynamic reloading
of database configuration. In the typical use of `sql.Open()`, users must
close the returned DB object and recreate it to change the
connection string. Hotload works by registering a driver that proxies
the [`Driver` interface](https://pkg.go.dev/database/sql/driver#Driver)
to the real database driver. When config changes
are detected it retires connections in a manner that causes the `database/sql`
package to create new connections with the new connection parameters.

Upgrading from hotload v1? See [MIGRATION.md](MIGRATION.md).

```go
import (
    // import the std lib sql package
    "database/sql"

    log "github.com/sirupsen/logrus"

    // this import registers hotload with the sql package
    "github.com/infobloxopen/hotload/v3"

    // this import registers the fsnotify hotload strategy
    _ "github.com/infobloxopen/hotload/v3/fsnotify"

    // this import registers the postgres driver with the sql package
    "github.com/lib/pq"
)

func init() {
    // this function call registers the lib/pq postgres driver with hotload
    hotload.RegisterSQLDriver("postgres", &pq.Driver{})
}

func main() {
    db, err := sql.Open("hotload", "fsnotify://postgres/tmp/myconfig.txt")
    if err != nil {
        log.Fatalf("could not open db connection: %s", err)
    }
    db.Query("select 1")
}
```
The above code:
* registers the hotload driver with `database/sql`
* registers the `fsnotify` strategy with hotload
* registers the `lib/pq` postgres driver with `database/sql`
* registers the `lib/pq` postgres driver with hotload

In the `main()` function, the `sql.Open` call uses the hotload driver. The URL for the
connection string specifies `fsnotify` in the scheme. This is the hotload strategy. The
hostname in the URL specifies the real database driver (`postgres` in the example above).
Finally, the path and query parameters are left for the hotload strategy plugin to configure themselves.
Below is an example of a `lib/pq` Postgres connection string that would have been stored at `/tmp/myconfig.txt`
```
user=pqgotest dbname=pqgotest sslmode=verify-full
```

# Faithful driver capabilities

`database/sql` discovers what a driver can do through type assertions on the
optional interfaces of `database/sql/driver` (`ExecerContext`,
`QueryerContext`, `Pinger`, `NamedValueChecker`, `StmtExecContext`, …).
Hotload wraps connections and statements so that a wrapped object implements
an optional interface **if and only if** the underlying driver supports the
capability. If your driver has no `ExecerContext` (or legacy `Execer`),
`database/sql` correctly falls back to prepared statements instead of
bouncing off `driver.ErrSkip`.

The interfaces hotload needs for its own lifecycle (`ConnPrepareContext`,
`ConnBeginTx`, `SessionResetter`, `Validator`) are always implemented; when
the underlying driver lacks them, hotload replicates `database/sql`'s own
fallback behavior exactly, so the difference is unobservable.

# Strategies

Hotload has an interface for adding reload strategies. The interface looks like this:
```go
// Strategy is the plugin interface for hotload.
type Strategy interface {
	// Watch returns back the contents of the resource as well as a channel
	// for subsequent updates (if the value has changed). If there is an error
	// getting the initial value, an error is returned.
	Watch(ctx context.Context, pth string, pathQry string) (value string, newValChan <-chan string, err error)

	// CloseWatch closes the specified watch.
	CloseWatch(pth string, pathQry string) error

	// Close resets/closes strategy, in particular closes all the update channels.
	Close()
}
```

The strategies are loaded by calling the `RegisterStrategy` method in the hotload package.
This is the same pattern the `database/sql` package uses for loading drivers. The strategy
implements the `Watch` method. The context passed to the strategy should be used to shut
down any code watching the passed `pth`. `pathQry` carries the hotload connection
string's encoded query parameters. The strategy doesn't have to use a real file to load
the config. `pth` represents a unique string that makes sense to the strategy. For
example, pth could point to a path in etcd or a kind/id in k8s.

The hotload project ships with one hotload strategy: `fsnotify`.

Note: In your project, if you do not implement your own `Strategy`, and instead choose to use the out-of-the-box
`fsnotify` strategy, you must import the `fsnotify` package in your project to register at least one strategy with
hotload, otherwise an error will occur at runtime as the `database/sql` package will not be able to locate/load
your intended hotload strategy as a recognizable driver.

# Force Kill

By default, the hotload driver gracefully retires connections to the underlying driver:
in-flight work finishes on the old connection, and the pool replaces connections the
next time it reuses them. A retired generation gets one grace period — its connections
are force-closed when the *next* config change arrives.

Adding `forceKill=true` to your DSN will cause the hotload driver to cancel in-flight
operations and close the underlying connections as soon as a change to the connection
information is detected. Operations canceled this way fail with an error matching
`hotload.ErrHotSwap` (via `errors.Is`).

For example:
```
db, err := sql.Open("hotload", "fsnotify://postgres/tmp/myconfig.txt?forceKill=true")
```

With `forceKill=true`, hotload waits a bounded window (default 100ms) for in-flight
operations to observe the cancellation before force-closing their connections, so a
driver that ignores context cancellation can never wedge config-change processing.
Tune it with the `killWindow` DSN parameter, e.g. `?forceKill=true&killWindow=250ms`.

# Connection lifecycle

`sql.Open("hotload", dsn)` starts the strategy watch for that DSN; multiple `sql.Open`
calls with the same DSN share one watch. Closing the last `sql.DB` for a DSN stops the
watch and its background goroutine. Note that this means configuration errors (unknown
strategy or driver, unreadable config file) surface at `sql.Open` instead of at first
use.

# Metrics

Hotload's core has no metrics dependency. It emits events through a small hooks API
(`hotload.RegisterHooks`); the
[`observability`](https://pkg.go.dev/github.com/infobloxopen/hotload/observability)
module adapts those events to prometheus, preserving the metric names of hotload v1:

```go
import "github.com/infobloxopen/hotload/observability"

func main() {
    // nil registers with prometheus.DefaultRegisterer
    observability.MustEnablePrometheus(nil)
    ...
}
```

| Metric | Description |
|---|---|
| `transaction_sql_stmts` | sql statements per transaction, labeled by the exec labels carried via `hotload.ContextWithExecLabels` |
| `hotload_change_total` | config changes per hotload DSN |
| `hotload_last_changed_timestamp_seconds` | unix timestamp of the last change per DSN |
| `hotload_modtime_latency_histogram` | staleness of watched files (fed by the `modtime` package) |
| `hotload_path_chksum_timestamp_seconds` | when each watched file's checksum last changed (opt-in via `HOTLOAD_PATH_CHKSUM_METRICS_ENABLE`) |

# How To Run Tests Locally

Unit tests (no database needed):
```
$ make test
```

Integration tests (real postgres via docker compose):
```
$ make postgres-docker-compose-up
$ make local-integration-tests
$ make postgres-docker-compose-down
```
The integration tests honor `HOTLOAD_INTEGRATION_TEST_POSTGRES_HOST` and
`HOTLOAD_INTEGRATION_TEST_POSTGRES_PORT` if your postgres lives elsewhere, and skip
themselves when no server is reachable.

# Repository layout

This repository holds three Go modules tied together by the committed `go.work`:

| Module | Purpose | Dependencies |
|---|---|---|
| `github.com/infobloxopen/hotload/v3` | the driver, `fsnotify` strategy, `logger`, `modtime` | `fsnotify` only |
| `github.com/infobloxopen/hotload/observability` | prometheus adapter + test helpers | prometheus |
| `github.com/infobloxopen/hotload/test/integration` | postgres integration tests (never imported) | `lib/pq` |

The combination wrapper types are generated; after changing `internal/gen`,
`conn_pieces.go` or `stmt_pieces.go`, run `make generate` and commit the result
(CI fails on a diff).
