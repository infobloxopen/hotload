// Package hotload is a database/sql driver that dynamically loads connection strings for other
// database drivers. To use it, import it like any other database driver and register
// the real database driver you want to use with hotload.
//
//     import (
//         // import the std lib sql package
//         "database/sql"
//
//        log "github.com/sirupsen/logrus"
//
//        // this import registers hotload with the sql package
//        "github.com/infobloxopen/hotload"
//
//        // this import registers the fsnotify hotload strategy
//        _ "github.com/infobloxopen/hotload/fsnotify"
//
//        // this import registers the postgres driver with the sql package
//        "github.com/lib/pq"
//     )
//
//     func init() {
//         // this function call registers the lib/pq postgres driver with hotload
//         hotload.Register("postgres", pq.Driver{})
//     }
//
//     func main() {
//         db, err := sql.Open("hotload", "fsnotify://postgres/tmp/myconfig.txt")
//         if err != nil {
//             log.Fatalf("could not open db connection: %s", err)
//         }
//         db.Query("select 1")
//     }
//
// The above code:
// * registers the hotload driver with database/sql
// * registers the fsnotify strategy with hotload
// * registers the lib/pq postgres driver with database/sql
// * registers the lib/pq postgres driver with hotload
//
// Then in the main() function the sql.Open call uses the hotload driver. The URL for the
// connection string specifies fsnotify in the scheme. This is the hotload strategy. The
// hostname in the URL specifies the real database driver. Finally the path and query parameters
// are left for the hotload strategy plugin to configure themselves. Below is an example
// of a lib/pq postgres connection string that would have been stored at /tmp/myconfig.txt
//     user=pqgotest dbname=pqgotest sslmode=verify-full
package hotload

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"net/url"
	"sort"
	"sync"
)

// Strategy is the plugin interface for hotload.
type Strategy interface {
	// Watch returns back the contents of the resource as well as a channel
	// for subsequent updates (if the value has changed). If there is an error
	// getting the initial value, an error is returned.
	Watch(ctx context.Context, pth string, options url.Values) (value string, values <-chan string, err error)
}

var (
	ErrUnsupportedStrategy       = fmt.Errorf("unsupported hotload strategy")
	ErrMalformedConnectionString = fmt.Errorf("malformed hotload connection string")
	ErrUnknownDriver             = fmt.Errorf("target driver is not registered with hotload")

	mu         sync.RWMutex
	sqlDrivers = make(map[string]driver.Driver)
	strategies = make(map[string]Strategy)
)

// RegisterSQLDriver makes a database driver available by the provided name.
// If RegisterSQLDriver is called twice with the same name or if driver is nil,
// it panics.
func RegisterSQLDriver(name string, driver driver.Driver) {
	mu.Lock()
	defer mu.Unlock()
	if driver == nil {
		panic("hotload: Register driver is nil")
	}
	if _, dup := sqlDrivers[name]; dup {
		panic("hotload: Register called twice for driver " + name)
	}
	sqlDrivers[name] = driver
}

func unregisterAll() {
	mu.Lock()
	defer mu.Unlock()
	// For tests.
	sqlDrivers = make(map[string]driver.Driver)
	strategies = make(map[string]Strategy)
}

// SQLDrivers returns a sorted list of the names of the registered drivers.
func SQLDrivers() []string {
	mu.RLock()
	defer mu.RUnlock()
	list := make([]string, 0, len(sqlDrivers))
	for name := range sqlDrivers {
		list = append(list, name)
	}
	sort.Strings(list)
	return list
}

// RegisterStrategy makes a database driver available by the provided name.
// If RegisterStrategy is called twice with the same name or if strategy is nil,
// it panics.
func RegisterStrategy(name string, strategy Strategy) {
	mu.Lock()
	defer mu.Unlock()
	if strategy == nil {
		panic("hotload: RegisterStrategy strategy is nil")
	}
	if _, dup := strategies[name]; dup {
		panic("hotload: RegisterStrategy called twice for strategy " + name)
	}
	strategies[name] = strategy
}

// Strategies returns a sorted list of the names of the registered drivers.
func Strategies() []string {
	mu.RLock()
	defer mu.RUnlock()
	list := make([]string, 0, len(strategies))
	for name := range strategies {
		list = append(list, name)
	}
	sort.Strings(list)
	return list
}

func init() {
	sql.Register("hotload", &hdriver{ctx: context.Background()})
}

// hdriver is the hotload driver.
type hdriver struct {
	ctx    context.Context
	cgroup map[string]*chanGroup
	mu     sync.Mutex
}

// chanGroup represents a hotload location that is being monitored
type chanGroup struct {
	value     string
	values    <-chan string
	parentCtx context.Context
	ctx       context.Context
	cancel    context.CancelFunc
	sqlDriver driver.Driver
	mu        sync.RWMutex
}

func (cg *chanGroup) run() {
	for {
		select {
		case <-cg.parentCtx.Done():
			cg.cancel()
			return
		case v := <-cg.values:
			if v == cg.value {
				// next update is the same, just ignore it
				continue
			}
			cg.mu.Lock()
			cg.cancel()
			cg.ctx, cg.cancel = context.WithCancel(cg.parentCtx)
			cg.value = v
			cg.mu.Unlock()
		}
	}
}

func (cg *chanGroup) Open() (driver.Conn, error) {
	cg.mu.RLock()
	defer cg.mu.RUnlock()
	conn, err := cg.sqlDriver.Open(cg.value)
	if err != nil {
		return conn, err
	}
	return newManagedConn(cg.ctx, conn), nil
}

func (h *hdriver) Open(name string) (driver.Conn, error) {

	uri, err := url.Parse(name)
	if err != nil {
		return nil, err
	}
	mu.RLock()
	defer mu.RUnlock()

	// look up in the chan group
	cgroup, ok := h.cgroup[name]
	if !ok {
		strategy, ok := strategies[uri.Scheme]
		if !ok {
			return nil, ErrUnsupportedStrategy
		}

		sqlDriver, ok := sqlDrivers[uri.Host]
		if !ok {
			return nil, ErrUnknownDriver
		}

		value, values, err := strategy.Watch(h.ctx, uri.Path, uri.Query())
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithCancel(h.ctx)
		cgroup = &chanGroup{
			value:     value,
			values:    values,
			parentCtx: h.ctx,
			ctx:       ctx,
			cancel:    cancel,
			sqlDriver: sqlDriver,
		}
		h.cgroup[name] = cgroup
		go cgroup.run()
	}
	return cgroup.Open()
}
