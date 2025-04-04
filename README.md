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
    _ "github.com/infobloxopen/hotload/fsnotify"

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

# How To Run Integration Tests Locally
```
$ make postgres-docker-compose-up
$ cd integrationstests
$ go test -v -race
$ vi ...
$ go test -v -race # this can be repeated  in your edit-run-test cycle
$ make postgres-docker-compose-down
```
