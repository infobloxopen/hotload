package core

import (
	"context"
	"database/sql/driver"
	"fmt"
	"sync"
	"time"

	"github.com/infobloxopen/hotload/internal/clock"
	"github.com/infobloxopen/hotload/internal/conntrack"
	"github.com/infobloxopen/hotload/internal/connwrap"
	"github.com/infobloxopen/hotload/internal/engine"
	"github.com/infobloxopen/hotload/internal/observability"
	"github.com/infobloxopen/hotload/logger"
	"github.com/infobloxopen/hotload/metrics"
)

// Strategy is the interface that DSN strategies must implement.
type Strategy interface {
	Watch(ctx context.Context, path string, query map[string][]string) (value string, updates <-chan string, err error)
	Close() error
}

// Connector implements driver.Connector for hotload.
type Connector struct {
	parsed   *ParsedDSN
	strategy Strategy
	realDrv  driver.Driver
	eng      engine.Engine
	tracker  *conntrack.Registry
	log      *observability.Logger
	metrics  *observability.Metrics

	mu           sync.RWMutex
	watchStarted bool
	watchCancel  context.CancelFunc
	watchCtx     context.Context
}

// NewConnector creates a new hotload connector.
func NewConnector(
	parsed *ParsedDSN,
	strategy Strategy,
	realDriver driver.Driver,
	log logger.Logger,
	metricsProvider metrics.Provider,
) (*Connector, error) {

	clk := clock.Real{}

	policy := engine.Policy{
		DrainTimeout:         parsed.DrainTimeout,
		ForceKill:            parsed.ForceKill,
		Debounce:             parsed.Debounce,
		Preconnect:           parsed.Preconnect,
		CredentialOnlyReload: parsed.CredentialOnlyReload,
	}

	tracker := conntrack.NewRegistry(clk)
	obsLog := observability.NewLogger("hotload", log)
	obsMetrics := observability.NewMetrics(metricsProvider)

	c := &Connector{
		parsed:   parsed,
		strategy: strategy,
		realDrv:  realDriver,
		tracker:  tracker,
		log:      obsLog,
		metrics:  obsMetrics,
	}

	// Create engine with hooks
	hooks := engine.Hooks{
		OnPromote: func(ev engine.Event) {
			obsLog.Logf("DSN promoted to epoch %d at %s", ev.Epoch, ev.At)
			obsMetrics.SetEpoch(parsed.TargetDriver, ev.Epoch)

			// Skip draining if this is a credential-only change
			if ev.SkipDrain {
				obsLog.Logf("Skipping connection drain for credential-only change (epoch %d)", ev.Epoch)
				return
			}

			// Start draining old epoch if there is one
			if ev.Epoch > 1 {
				go c.drainOldEpoch(ev.Epoch - 1)
			}
		},
		OnError: func(err error) {
			obsLog.ErrLogf("engine error: %v", err)
		},
	}

	c.eng = engine.New(policy, hooks, clk)

	return c, nil
}

// Driver implements driver.Connector.
func (c *Connector) Driver() driver.Driver {
	// Return a minimal driver implementation
	return &driverShim{}
}

// Connect implements driver.Connector.
func (c *Connector) Connect(ctx context.Context) (driver.Conn, error) {
	// Ensure watch is started
	if err := c.ensureWatch(ctx); err != nil {
		return nil, err
	}

	// Get current epoch and DSN
	current := c.eng.Current()
	if current.DSN == "" {
		return nil, fmt.Errorf("no DSN available yet")
	}

	// Open connection on real driver
	var conn driver.Conn
	var err error

	if drvCtx, ok := c.realDrv.(driver.DriverContext); ok {
		connector, cerr := drvCtx.OpenConnector(current.DSN)
		if cerr != nil {
			return nil, fmt.Errorf("failed to create connector: %w", cerr)
		}
		conn, err = connector.Connect(ctx)
	} else {
		conn, err = c.realDrv.Open(current.DSN)
	}

	if err != nil {
		return nil, err
	}

	// Wrap connection with tracking
	wrapped := connwrap.NewConn(conn, current.Epoch)
	c.tracker.Register(current.Epoch, wrapped.ConnWrapper)

	return wrapped, nil
}

// ensureWatch starts the strategy watcher if not already started.
func (c *Connector) ensureWatch(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.watchStarted {
		return nil
	}

	// Start watching - use background context for the watch lifecycle
	// (it lives beyond any single Connect call, but respect the incoming ctx for the initial setup)
	c.watchCtx, c.watchCancel = context.WithCancel(context.Background())

	initialDSN, updates, err := c.strategy.Watch(ctx, c.parsed.Path, c.parsed.Query)
	if err != nil {
		c.watchCancel() // Clean up the watch context
		return fmt.Errorf("failed to start watch: %w", err)
	}

	// Set initial DSN
	if initialDSN != "" {
		c.eng.Update(initialDSN)
	}

	// Watch for updates in background
	go c.watchUpdates(updates)

	c.watchStarted = true
	return nil
}

// watchUpdates processes DSN updates from the strategy.
func (c *Connector) watchUpdates(updates <-chan string) {
	for {
		select {
		case <-c.watchCtx.Done():
			return
		case dsn, ok := <-updates:
			if !ok {
				return
			}
			c.eng.Update(dsn)
		}
	}
}

// drainOldEpoch drains connections from an old epoch.
func (c *Connector) drainOldEpoch(epoch uint64) {
	policy := c.eng.Policy()

	ctx, cancel := context.WithTimeout(context.Background(), policy.DrainTimeout+time.Second)
	defer cancel()

	drained, killed := c.tracker.DrainEpoch(ctx, epoch, policy.DrainTimeout, policy.ForceKill)

	c.log.Logf("Epoch %d drained: %d graceful, %d killed", epoch, drained, killed)

	// Record metrics
	for i := 0; i < drained; i++ {
		c.metrics.RecordConnectionDrained(c.parsed.TargetDriver)
	}
	for i := 0; i < killed; i++ {
		c.metrics.RecordConnectionKilled(c.parsed.TargetDriver, "timeout")
	}
}

// Close closes the connector and stops watching.
func (c *Connector) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.watchCancel != nil {
		c.watchCancel()
	}

	if c.strategy != nil {
		return c.strategy.Close()
	}

	return nil
}

// driverShim is a minimal driver implementation returned by Connector.Driver().
type driverShim struct{}

func (d *driverShim) Open(name string) (driver.Conn, error) {
	return nil, fmt.Errorf("hotload: use sql.OpenDB with connector, not sql.Open")
}
