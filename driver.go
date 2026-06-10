// Package hotload is a database/sql driver that dynamically loads connection
// strings for other database drivers. To use it, import it like any other
// database driver and register the real database driver you want to use with
// hotload.
//
//	import (
//	    // import the std lib sql package
//	    "database/sql"
//
//	   log "github.com/sirupsen/logrus"
//
//	   // this import registers hotload with the sql package
//	   "github.com/infobloxopen/hotload/v3"
//
//	   // this import registers the fsnotify hotload strategy
//	   _ "github.com/infobloxopen/hotload/v3/fsnotify"
//
//	   // this import registers the postgres driver with the sql package
//	   "github.com/lib/pq"
//	)
//
//	func init() {
//	    // this function call registers the lib/pq postgres driver with hotload
//	    hotload.RegisterSQLDriver("postgres", pq.Driver{})
//	}
//
//	func main() {
//	    db, err := sql.Open("hotload", "fsnotify://postgres/tmp/myconfig.txt")
//	    if err != nil {
//	        log.Fatalf("could not open db connection: %s", err)
//	    }
//	    db.Query("select 1")
//	}
//
// The above code:
// * registers the hotload driver with database/sql
// * registers the fsnotify strategy with hotload
// * registers the lib/pq postgres driver with database/sql
// * registers the lib/pq postgres driver with hotload
//
// Then in the main() function the sql.Open call uses the hotload driver. The
// URL for the connection string specifies fsnotify in the scheme. This is
// the hotload strategy. The hostname in the URL specifies the real database
// driver. Finally the path and query parameters are left for the hotload
// strategy plugin to configure themselves. Below is an example of a lib/pq
// postgres connection string that would have been stored at
// /tmp/myconfig.txt
//
//	user=pqgotest dbname=pqgotest sslmode=verify-full
package hotload

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"net/url"
	"sort"
	"sync"
	"time"

	"github.com/infobloxopen/hotload/v3/logger"
)

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

const forceKillParam = "forceKill"
const killWindowParam = "killWindow"

var (
	mu         sync.RWMutex
	sqlDrivers = make(map[string]*driverInstance)
	strategies = make(map[string]Strategy)
)

type driverInstance struct {
	driver  driver.Driver
	options map[string]string
}

type driverOption func(*driverInstance)

// WithDriverOptions allows you to specify query parameters to the underlying
// driver. The underlying driver must support URL style connection strings.
// The given options are appended to the connection string when a connection
// is opened.
func WithDriverOptions(options map[string]string) driverOption {
	return func(d *driverInstance) {
		if d.options == nil {
			d.options = make(map[string]string)
		}
		for k, v := range options {
			d.options[k] = v
		}
	}
}

// RegisterSQLDriver makes a database driver available by the provided name.
// If RegisterSQLDriver is called twice with the same name or if driver is
// nil, it panics.
func RegisterSQLDriver(name string, driver driver.Driver, options ...driverOption) {
	mu.Lock()
	defer mu.Unlock()
	if driver == nil {
		panic("hotload: Register driver is nil")
	}
	if _, dup := sqlDrivers[name]; dup {
		panic("hotload: Register called twice for driver " + name)
	}
	di := &driverInstance{driver: driver}
	for _, opt := range options {
		opt(di)
	}
	sqlDrivers[name] = di
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

// RegisterStrategy makes a hotload strategy available by the provided name.
// If RegisterStrategy is called twice with the same name or if strategy is
// nil, it panics.
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

// UnregisterStrategy unregisters the named driver strategy.
// Does nothing if strategy does not exist.
// Intended for internal unit-testing.
func UnregisterStrategy(name string) {
	mu.Lock()
	defer mu.Unlock()
	strategy, ok := strategies[name]
	if ok {
		if strategy != nil {
			strategy.Close()
		}
		delete(strategies, name)
	}
}

// Strategies returns a sorted list of the names of the registered
// strategies.
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
	sql.Register("hotload", newHdriver(context.Background()))
}

func newHdriver(ctx context.Context) *hdriver {
	return &hdriver{
		ctx:    ctx,
		groups: make(map[string]*group),
	}
}

// hdriver is the hotload driver.
type hdriver struct {
	ctx    context.Context
	mu     sync.Mutex
	groups map[string]*group
}

var (
	_ driver.Driver        = (*hdriver)(nil)
	_ driver.DriverContext = (*hdriver)(nil)
)

// Open implements the legacy driver.Driver dial path. Groups created here
// are pinned: they have no teardown signal, so their strategy watch and run
// loop live for the life of the process (as in hotload v1). Prefer the
// connector path (database/sql uses it automatically), which tears the
// group down when the last sql.DB using it is closed.
func (h *hdriver) Open(name string) (driver.Conn, error) {
	g, err := h.getGroup(name, true)
	if err != nil {
		return nil, err
	}
	return g.conn(context.Background())
}

// OpenConnector implements driver.DriverContext. The returned connector
// holds a reference on the group; database/sql closes the connector when
// the sql.DB is closed, and the group shuts down when its last reference is
// released.
func (h *hdriver) OpenConnector(name string) (driver.Connector, error) {
	g, err := h.getGroup(name, false)
	if err != nil {
		return nil, err
	}
	return &connector{h: h, g: g, name: name}, nil
}

// getGroup returns the group watching name, creating it (and its strategy
// watch) on first use. Groups are shared across sql.DB handles opened with
// the same DSN.
func (h *hdriver) getGroup(name string, pin bool) (*group, error) {
	uri, err := url.Parse(name)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedConnectionString, err)
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	g, ok := h.groups[name]
	if !ok {
		mu.RLock()
		strategy, okStrategy := strategies[uri.Scheme]
		sqlDriver, okDriver := sqlDrivers[uri.Host]
		mu.RUnlock()
		if !okStrategy {
			return nil, ErrUnsupportedStrategy
		}
		if !okDriver {
			return nil, ErrUnknownDriver
		}

		queryParams := uri.Query()
		forceKill, killWindow, err := parseGroupParams(queryParams)
		if err != nil {
			return nil, err
		}

		pathQry := queryParams.Encode()
		value, newValChan, err := strategy.Watch(h.ctx, uri.Path, pathQry)
		if err != nil {
			return nil, err
		}

		parentCtx, parentCancel := context.WithCancel(h.ctx)
		g = &group{
			name:         name,
			strategyName: uri.Scheme,
			strategy:     strategy,
			path:         uri.Path,
			pathQry:      pathQry,
			sqlDriver:    sqlDriver,
			forceKill:    forceKill,
			killWindow:   killWindow,
			parentCtx:    parentCtx,
			parentCancel: parentCancel,
			newValChan:   newValChan,
		}
		g.cur = newGeneration(parentCtx, value)
		h.groups[name] = g
		h.logf("hotload", "new group: '%s'", name)
		EmitWatchEvent(WatchEvent{GroupName: name, Strategy: uri.Scheme, Path: uri.Path})
		go g.runLoop()
	}

	if pin {
		g.pinned = true
	} else {
		g.refs++
	}
	return g, nil
}

// releaseGroup drops one connector reference and shuts the group down when
// no references remain (unless a legacy Open pinned it).
func (h *hdriver) releaseGroup(name string) {
	h.mu.Lock()
	g, ok := h.groups[name]
	if ok {
		g.refs--
		if g.refs > 0 || g.pinned {
			g = nil
		} else {
			delete(h.groups, name)
		}
	}
	h.mu.Unlock()

	if g != nil {
		g.shutdown()
	}
}

func parseGroupParams(vs url.Values) (forceKill bool, killWindow time.Duration, err error) {
	killWindow = DefaultKillWindow
	if v, ok := vs[forceKillParam]; ok && len(v) > 0 {
		forceKill = v[0] == "true"
	}
	if v, ok := vs[killWindowParam]; ok && len(v) > 0 {
		killWindow, err = time.ParseDuration(v[0])
		if err != nil {
			return false, 0, fmt.Errorf("%w: invalid %s: %v", ErrMalformedConnectionString, killWindowParam, err)
		}
	}
	return forceKill, killWindow, nil
}

func (h *hdriver) logf(prefix, format string, args ...any) {
	logger.Logf(fmt.Sprintf("%s:", prefix), format, args...)
}

// Deprecated: Use logger.WithLogger() instead, retained for
// backwards-compatibility only.
func WithLogger(l logger.Logger) {
	logger.WithLogger(l)
}

// Deprecated: Use logger.GetLogger() instead, retained for
// backwards-compatibility only.
func GetLogger() logger.Logger {
	return logger.GetLogger()
}
