// Package hotload is a database/sql driver that dynamically loads connection strings for other
// database drivers. To use it, import it like any other database driver and register
// the real database driver you want to use with hotload.
//
//	import (
//	    // import the std lib sql package
//	    "database/sql"
//
//	   log "github.com/sirupsen/logrus"
//
//	   // this import registers hotload with the sql package
//	   "github.com/infobloxopen/hotload"
//
//	   // this import registers the fsnotify hotload strategy
//	   _ "github.com/infobloxopen/hotload/fsnotify"
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
// Then in the main() function the sql.Open call uses the hotload driver. The URL for the
// connection string specifies fsnotify in the scheme. This is the hotload strategy. The
// hostname in the URL specifies the real database driver. Finally the path and query parameters
// are left for the hotload strategy plugin to configure themselves. Below is an example
// of a lib/pq postgres connection string that would have been stored at /tmp/myconfig.txt
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

	"github.com/infobloxopen/hotload/internal"
	"github.com/infobloxopen/hotload/logger"
	"github.com/infobloxopen/hotload/metrics"
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

const forceKill = "forceKill"
const driverOptions = "driverOptions"

var (
	ErrUnsupportedStrategy       = fmt.Errorf("unsupported hotload strategy")
	ErrMalformedConnectionString = fmt.Errorf("malformed hotload connection string")
	ErrUnknownDriver             = fmt.Errorf("target driver is not registered with hotload")

	mu         sync.RWMutex
	sqlDrivers = make(map[string]*driverInstance)
	strategies = make(map[string]Strategy)
)

type driverInstance struct {
	driver  driver.Driver
	options map[string]string
}

type driverOption func(*driverInstance)

// WithDriverOptions allows you to specify query parameters to the underlying driver.
// The underlying driver must support URL style connection strings. The given options
// are appended to the connection string when a connection is opened.
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
// If RegisterSQLDriver is called twice with the same name or if driver is nil,
// it panics.
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

func unregisterAll() {
	mu.Lock()
	defer mu.Unlock()
	// For tests.
	sqlDrivers = make(map[string]*driverInstance)
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
	ctx := context.Background()
	sql.Register("hotload", &hdriver{
		ctx:    ctx,
		cgroup: make(map[string]*chanGroup),
	})
}

// hdriver is the hotload driver.
type hdriver struct {
	ctx    context.Context
	cgroup map[string]*chanGroup
	mu     sync.Mutex
}

// chanGroup represents a hotload location that is being monitored
type chanGroup struct {
	name          string
	value         string
	redactVal     string
	newValChan    <-chan string
	parentCtx     context.Context
	ctx           context.Context
	cancel        context.CancelFunc
	sqlDriver     *driverInstance
	mu            sync.RWMutex
	forceKill     bool
	conns         []*managedConn
	prevCancel    context.CancelFunc
	prevRedactVal string
	prevConns     []*managedConn
}

// monitor the location for changes
func (cg *chanGroup) runLoop() {
	for {
		cg.logf("chanGroup.runLoop", "select waiting...")
		select {
		case <-cg.parentCtx.Done():
			cg.cancel()
			cg.logf("chanGroup.runLoop", "parent context done, canceled chanGroup context, terminating")
			return

		case newValue, ok := <-cg.newValChan:
			if !ok {
				cg.logf("chanGroup.runLoop", "newValChan closed, terminating")
				return
			}
			cg.processNewValue(newValue)
		}
	}
}

func (cg *chanGroup) processNewValue(newValue string) {
	type oldInfo struct {
		changedFlag       bool
		prevPrevCancel    context.CancelFunc
		prevPrevRedactVal string
		prevPrevConns     []*managedConn
		prevCancel        context.CancelFunc
		prevRedactVal     string
		prevConns         []*managedConn
	}

	criticalSection := func() oldInfo {
		cg.mu.Lock()
		defer cg.mu.Unlock()

		prevValue := cg.value
		prevRedactVal := cg.redactVal

		newRedactVal := internal.RedactUrl(newValue)
		cg.logf("chanGroup.processNewValue", "old conn dsn: '%s'", prevRedactVal)
		cg.logf("chanGroup.processNewValue", "new conn dsn: '%s'", newRedactVal)

		if newValue == prevValue {
			// next update is the same, just ignore it
			cg.logf("chanGroup.processNewValue", "conn dsn not changed")
			return oldInfo{}
		}
		cg.logf("chanGroup.processNewValue", "conn dsn changed")

		result := oldInfo{
			changedFlag:       true,
			prevPrevConns:     cg.prevConns,
			prevPrevCancel:    cg.prevCancel,
			prevPrevRedactVal: cg.prevRedactVal,
		}

		// Prepare shallow copy of existing connections,
		// and reset new connections to zero
		cg.prevConns = cg.conns
		cg.conns = make([]*managedConn, 0)

		// Prepare copy of existing cancel ctx fn,
		// and reset to new cancelable ctx
		cg.prevCancel = cg.cancel
		cg.ctx, cg.cancel = context.WithCancel(cg.parentCtx)

		// Prepare copy of existing value,
		// and reset to new value
		cg.prevRedactVal = cg.redactVal
		cg.value = newValue
		cg.redactVal = newRedactVal

		result.prevConns = cg.prevConns
		result.prevCancel = cg.prevCancel
		result.prevRedactVal = cg.prevRedactVal

		return result
	}

	prev := criticalSection()
	if !prev.changedFlag {
		return
	}

	// Mutex MUST be unlocked at this point before continuing

	// Update metrics
	metrics.IncHotloadChangeTotal(cg.name)
	metrics.SetHotloadLastChangedTimestampSeconds(cg.name, float64(time.Now().Unix()))

	// Canceling previous ctx can potentially cause other threads
	// to call managedConn.Close(), which calls managedConn.afterClose(),
	// which calls chanGroup.removeMgdConn(), which tries to lock mutex.
	if cg.forceKill {
		// Immediately cancel the previous dsn
		if prev.prevCancel != nil {
			prev.prevCancel()
			cg.logf("chanGroup.processNewValue", "canceled context for previous dsn: '%s'", prev.prevRedactVal)
		}
	} else {
		// Immediately cancel the previous-previous dsn.
		// We let the previous dsn to gracefully continue until the next dsn-change.
		if prev.prevPrevCancel != nil {
			prev.prevPrevCancel()
			cg.logf("chanGroup.processNewValue", "canceled context for previous-previous dsn: '%s'", prev.prevPrevRedactVal)
		}
	}

	// Yield to let other threads process cancel signal.
	// Otherwise, there's a race and what happens (esp if forceKill=true)
	// is that sometimes a db.Exec completes successfully (before cancel is processed),
	// but db.Exec is later killed (closed) below because dsn changed, resulting in
	// db.Exec returning error.  This is inconsistent.
	time.Sleep(1 * time.Millisecond)

	// Reset previous connections
	// Mutex MUST NOT be held by this point, because in the same thread,
	// we will call managedConn.Close() if forceKill is true,
	// which calls managedConn.afterClose(), which calls chanGroup.removeMgdConn(),
	// which tries to lock mutex.
	if cg.forceKill {
		// Immediately reset/close previous conns
		cg.logf("chanGroup.processNewValue", "reset/close conns for previous dsn: '%s'", prev.prevRedactVal)
		for _, c := range prev.prevConns {
			c.Reset(true)
			// ignore errors from close
			c.Close()
		}
	} else {
		// Immediately close previous-previous conns.
		// We let the previous conns to gracefully continue until the next dsn-change.
		cg.logf("chanGroup.processNewValue", "close conns for previous-previous dsn: '%s'", prev.prevPrevRedactVal)
		for _, c := range prev.prevPrevConns {
			// ignore errors from close
			c.Close()
		}

		// Immediately reset (but do not close) previous conns.
		// We let the previous conns to gracefully continue until the next dsn-change.
		cg.logf("chanGroup.processNewValue", "reset conns for previous dsn: '%s'", prev.prevPrevRedactVal)
		for _, c := range prev.prevConns {
			c.Reset(true)
		}
	}
}

func mergeConnStringOptions(dsn string, options map[string]string) (string, error) {
	if len(options) == 0 {
		return dsn, nil
	}
	u, err := url.ParseRequestURI(dsn)
	if err != nil {
		return "", fmt.Errorf("unable to parse connection string when specifying extra driver options: %v", err)
	}
	values, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return "", fmt.Errorf("unable to parse query options in connection string when specifying extra driver options: %v", err)
	}
	for k, v := range options {
		values.Set(k, v)
	}
	u.RawQuery = values.Encode()
	return u.String(), nil
}

func (cg *chanGroup) Open() (driver.Conn, error) {
	cg.mu.Lock()
	defer cg.mu.Unlock()
	dsn, err := mergeConnStringOptions(cg.value, cg.sqlDriver.options)
	if err != nil {
		return nil, err
	}
	redactDsn := internal.RedactUrl(dsn)
	conn, err := cg.sqlDriver.driver.Open(dsn)
	if err != nil {
		return conn, err
	}

	manConn := newManagedConn(cg.ctx, dsn, redactDsn, conn, cg.removeMgdConn)
	cg.conns = append(cg.conns, manConn)
	cg.logf("chanGroup.Open", "opened managed conn: '%s'", manConn.redactDsn)

	return manConn, nil
}

func (cg *chanGroup) removeMgdConn(conn *managedConn) {
	cg.mu.Lock()
	defer cg.mu.Unlock()
	for i, c := range cg.conns {
		if c == conn {
			cg.conns = append(cg.conns[:i], cg.conns[i+1:]...)
			cg.logf("chanGroup.removeMgdConn", "%d: removed: '%s'", i, conn.redactDsn)
			return
		}
	}
}

func (cg *chanGroup) parseUrlValues(vs url.Values) {
	cg.logf("chanGroup.parseUrlValues", "values: %s", vs)
	v, ok := vs[forceKill]
	if ok && len(v) > 0 {
		firstValue := v[0]
		cg.forceKill = firstValue == "true"
		cg.logf("chanGroup.parseUrlValues", "forceKill set to true")
	}
}

func (h *hdriver) Open(name string) (driver.Conn, error) {
	uri, err := url.Parse(name)
	if err != nil {
		return nil, err
	}
	mu.Lock()
	defer mu.Unlock()

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
		queryParams := uri.Query()
		value, newValChan, err := strategy.Watch(h.ctx, uri.Path, queryParams.Encode())
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithCancel(h.ctx)
		cgroup = &chanGroup{
			name:       name,
			value:      value,
			redactVal:  internal.RedactUrl(value),
			newValChan: newValChan,
			parentCtx:  h.ctx,
			ctx:        ctx,
			cancel:     cancel,
			sqlDriver:  sqlDriver,
			conns:      make([]*managedConn, 0),
		}
		cgroup.parseUrlValues(queryParams)
		h.cgroup[name] = cgroup
		h.logf("hotload", "new chanGroup: '%s'", name)
		go cgroup.runLoop()
	}
	return cgroup.Open()
}

func (h *hdriver) logf(prefix, format string, args ...any) {
	logPrefix := fmt.Sprintf("%s:", prefix)
	logger.Logf(logPrefix, format, args...)
}

func (cg *chanGroup) logf(prefix, format string, args ...any) {
	logPrefix := fmt.Sprintf("%s[%s]:", prefix, cg.name)
	logger.Logf(logPrefix, format, args...)
}

// Deprecated: Use logger.WithLogger() instead, retained for backwards-compatibility only
func WithLogger(l logger.Logger) {
	logger.WithLogger(l)
}

// Deprecated: Use logger.GetLogger() instead, retained for backwards-compatibility only
func GetLogger() logger.Logger {
	return logger.GetLogger()
}
