// Package hotload is a database/sql driver that dynamically loads connection strings for other
// database drivers. To use it, import it like any other database driver and register
// the real database driver you want to use with hotload.
//
//	import (
//	    "database/sql"
//	)
//
//	func init() {
//	    hotload.RegisterSQLDriver("postgres", &pq.Driver{})
//	}
//
//	func main() {
//	    db, err := sql.Open("hotload", "fsnotify://postgres/tmp/myconfig.txt")
//	    if err != nil {
//	        log.Fatalf("could not open db connection: %s", err)
//	    }
//	    db.Query("select 1")
//	}
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

var (
	ErrUnsupportedStrategy = fmt.Errorf("unsupported hotload strategy")

	ErrUnknownDriver = fmt.Errorf("target driver is not registered with hotload")

	ErrMalformedConnectionString = fmt.Errorf("malformed hotload connection string")
)

var (
	mu         sync.RWMutex
	sqlDrivers = make(map[string]driver.Driver)
	strategies = make(map[string]Strategy)
)

// RegisterSQLDriver makes a database driver available by the provided name.
// If RegisterSQLDriver is called twice with the same name or if driver is nil,
// it panics.
func RegisterSQLDriver(name string, drv driver.Driver) {
	mu.Lock()
	defer mu.Unlock()
	if drv == nil {
		panic("hotload: RegisterSQLDriver driver is nil")
	}
	if _, dup := sqlDrivers[name]; dup {
		panic("hotload: RegisterSQLDriver called twice for driver " + name)
	}
	sqlDrivers[name] = drv
}

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

// Strategies returns a sorted list of the names of the registered strategies.
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
	sql.Register("hotload", &hotloadDriver{
		connectors: make(map[string]*connector),
	})
}

type hotloadDriver struct {
	mu         sync.Mutex
	connectors map[string]*connector
}

func (d *hotloadDriver) Open(name string) (driver.Conn, error) {
	connector, err := d.OpenConnector(name)
	if err != nil {
		return nil, err
	}
	return connector.Connect(context.Background())
}

// OpenConnector implements driver.DriverContext.
func (d *hotloadDriver) OpenConnector(name string) (driver.Connector, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if conn, ok := d.connectors[name]; ok {
		return conn, nil
	}

	parsed, err := parseConnectionString(name)
	if err != nil {
		return nil, err
	}

	mu.RLock()
	strategy, ok := strategies[parsed.strategyName]
	if !ok {
		mu.RUnlock()
		return nil, ErrUnsupportedStrategy
	}

	targetDriver, ok := sqlDrivers[parsed.driverName]
	if !ok {
		mu.RUnlock()
		return nil, ErrUnknownDriver
	}
	mu.RUnlock()

	conn := &connector{
		name:         name,
		strategy:     strategy,
		targetDriver: targetDriver,
		parsed:       parsed,
	}

	d.connectors[name] = conn
	return conn, nil
}

type parsedDSN struct {
	strategyName string
	driverName   string
	path         string
	query        url.Values
}

func parseConnectionString(dsn string) (*parsedDSN, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedConnectionString, err)
	}

	if u.Scheme == "" {
		return nil, fmt.Errorf("%w: missing strategy (scheme)", ErrMalformedConnectionString)
	}

	if u.Host == "" {
		return nil, fmt.Errorf("%w: missing driver (host)", ErrMalformedConnectionString)
	}

	if u.Path == "" {
		return nil, fmt.Errorf("%w: missing path", ErrMalformedConnectionString)
	}

	return &parsedDSN{
		strategyName: u.Scheme,
		driverName:   u.Host,
		path:         u.Path,
		query:        u.Query(),
	}, nil
}

type connector struct {
	name         string
	strategy     Strategy
	targetDriver driver.Driver
	parsed       *parsedDSN

	mu      sync.Mutex
	started bool
	ctx     context.Context
	cancel  context.CancelFunc
	tracker *epochTracker

	logFunc func(format string, args ...interface{})
}

// Driver implements driver.Connector.
func (c *connector) Driver() driver.Driver {
	return c.targetDriver
}

// Connect implements driver.Connector.
func (c *connector) Connect(ctx context.Context) (driver.Conn, error) {
	if err := c.ensureStarted(ctx); err != nil {
		return nil, err
	}

	dsn, epoch := c.tracker.getCurrentDSN()
	if dsn == "" {
		return nil, fmt.Errorf("no DSN available")
	}

	conn, err := c.targetDriver.Open(dsn)
	if err != nil {
		return nil, err
	}

	return newWrappedConn(conn, epoch, c.tracker), nil
}

func (c *connector) ensureStarted(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.started {
		return nil
	}

	c.ctx, c.cancel = context.WithCancel(context.Background())

	// Try to use WatchWithOptions if available for backward compatibility
	var initialDSN string
	var updates <-chan string
	var err error
	if strategyWithOpts, ok := c.strategy.(StrategyWithOptions); ok {
		initialDSN, updates, err = strategyWithOpts.WatchWithOptions(c.ctx, c.parsed.path, c.parsed.query)
	} else {
		initialDSN, updates, err = c.strategy.Watch(c.ctx, c.parsed.path)
	}
	if err != nil {
		c.cancel()
		return fmt.Errorf("failed to start watching: %w", err)
	}

	c.tracker = newEpochTracker(initialDSN, c.parsed.query, c.logFunc)

	go c.watchUpdates(updates)

	c.started = true
	return nil
}

// watchUpdates processes DSN updates from the strategy.
func (c *connector) watchUpdates(updates <-chan string) {
	for {
		select {
		case <-c.ctx.Done():
			return
		case dsn, ok := <-updates:
			if !ok {
				return
			}
			c.tracker.updateDSN(dsn)
		}
	}
}

// Close closes the connector and stops watching.
func (c *connector) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cancel != nil {
		c.cancel()
	}

	return nil
}

func (c *connector) SetLogger(logFunc func(format string, args ...interface{})) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logFunc = logFunc
	if c.tracker != nil {
		c.tracker.logFunc = logFunc
	}
}
