[![Go Reference](https://pkg.go.dev/badge/github.com/infobloxopen/hotload.svg)](https://pkg.go.dev/github.com/infobloxopen/hotload)
# hotload
Hotload is a Golang `database/sql` compatible package that supports dynamic reloading
of database configuration. In the typical use of `sql.Open()`, users must
close the returned DB object and recreate it to change the
connection string. Hotload works by registering a driver that proxies
the [`Driver` interface](https://pkg.go.dev/database/sql/driver#Driver) 
to the real database driver. When config changes
are detected it closes connections in a manner that causes the `database/sql`
package to create new connections with the new connection parameters.

```go
import (
    // import the std lib sql package
    "database/sql"

    log "github.com/sirupsen/logrus"

    // this import registers hotload with the sql package
    "github.com/infobloxopen/hotload"

    // this import registers the fsnotify hotload strategy
    _ "github.com/infobloxopen/hotload/strategy/fsnotify"

    // this import registers the postgres driver with the sql package
    "github.com/lib/pq"
)

func init() {
    // this function call registers the lib/pq postgres driver with hotload
    hotload.Register("postgres", pq.Driver{})
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

# Strategies

Hotload has an interface for adding reload strategies. The interface looks like this:
```go
// Strategy is the plugin interface for hotload.
type Strategy interface {
	// Watch returns back the contents of the resource as well as a channel
	// for subsequent updates (if the value has changed). If there is an error
	// getting the initial value, an error is returned.
	Watch(ctx context.Context, pth string, options url.Values) (value string, values <-chan string, err error)
}
```

The strategies are loaded by calling the `RegisterStrategy` method in the hotload package.
This is the same pattern the `database/sql` package uses for loading drivers. The strategy
implements the `Watch` method. The context passed to the strategy should be used to shut
down any code watching the passed `pth`. Options are taken from the hotload connection
string query parameters. The strategy doesn't have to use a real file to load the config.
`pth` represents a unique string that makes sense to the strategy. For example, pth could
point to a path in etcd or a kind/id in k8s.

The hotload project ships with one hotload strategy: `fsnotify`.

Note: In your project, if you do not implement your own `Strategy`, and instead choose to use the out-of-the-box 
`fsnotify` strategy, you must import the `fsnotify` package in your project to register at least one strategy with 
hotload, otherwise an error will occur at runtime as the `database/sql` package will not be able to locate/load
your intended hotload strategy as a recognizable driver.

# Force Kill

By default, the hotload driver gracefully closes connections to the underlying driver. If your application holds connections open with long-running operations, this will prevent graceful switchover to new data sources.

Adding `forceKill=true` to your DSN will cause the hotload driver to close the underlying connection manually when a 
change to the connection information is detected.

For example:
```
db, err := sql.Open("hotload", "fsnotify://postgres/tmp/myconfig.txt?forceKill=true")
```

# Connection Draining

Hotload supports graceful connection draining when the DSN changes. This allows in-flight database operations to complete before switching to the new DSN.

## Configuration Options

- **`drainTimeout`**: Duration to wait for connections to become idle before force-closing them (default: `30s`)
- **`forceKill`**: Whether to force-close connections after drainTimeout expires (default: `true`)
- **`debounce`**: Duration to suppress rapid DSN changes (default: `250ms`)
- **`preconnect`**: Whether to test the new DSN before promoting it (default: `false`)
- **`credentialOnlyReload`**: Skip connection draining when only username/password changes (default: `false`)

Example with custom drain settings:
```go
db, err := sql.Open("hotload", "fsnotify://postgres/tmp/myconfig.txt?drainTimeout=2s&forceKill=true&debounce=500ms")
```

### Smart DSN Comparison (`credentialOnlyReload`)

When enabled, hotload intelligently detects whether a DSN change is "structural" (host, port, database, options) or "credential-only" (username, password). Credential-only changes do NOT trigger connection draining, allowing in-flight queries to complete without interruption.

**Use case:** Password rotation without query disruption.

```go
// Enable credential-only reload optimization
db, err := sql.Open("hotload", 
    "fsnotify://postgres/tmp/myconfig.txt?credentialOnlyReload=true&drainTimeout=10s")
```

**Behavior:**
- **Credential-only change** (username/password): Connections stay alive, no draining
- **Structural change** (host/port/database/options): Connections drained normally

Example scenario:
1. Current DSN: `postgresql://user1:pass1@localhost:5432/mydb`
2. Update to: `postgresql://user1:pass2@localhost:5432/mydb` ← **Credential only**
3. With `credentialOnlyReload=true`: In-flight queries continue uninterrupted
4. New connections immediately use `pass2`

If you then change:
1. Update to: `postgresql://user1:pass2@localhost:5433/mydb` ← **Port changed (structural)**
2. Connections are drained normally (port change requires new connections)

**Default behavior (`credentialOnlyReload=false`):** All DSN changes trigger connection draining for maximum safety.

## Connection Lifecycle

When a DSN change is detected:
1. New connections use the new DSN (new epoch)
2. Old connections are monitored for in-flight operations
3. If `credentialOnlyReload=true` and only credentials changed: Skip draining (connections continue)
4. Otherwise: Once operations complete (or `drainTimeout` expires), connections are closed
5. Metrics track gracefully drained vs force-killed connections

## Metrics

Hotload exposes the following Prometheus metrics for connection lifecycle:

- `hotload_connections_drained_total{driver}`: Connections gracefully closed
- `hotload_connections_killed_total{driver,reason}`: Connections force-closed (reason: timeout, error)
- `hotload_epoch{driver}`: Current DSN version/epoch
- `hotload_reload_seconds{driver}`: Duration of DSN switchover

# How To Run Integration Tests Locally
```
$ make postgres-docker-compose-up
$ cd integrationstests
$ go test -v -race -timeout=3m
$ vi ...
$ go test -v -race # this can be repeated  in your edit-run-test cycle
$ make postgres-docker-compose-down
```
