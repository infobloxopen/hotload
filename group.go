package hotload

import (
	"context"
	"database/sql/driver"
	"fmt"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/infobloxopen/hotload/v3/internal"
	"github.com/infobloxopen/hotload/v3/logger"
)

// DefaultKillWindow is how long a killed generation waits for in-flight
// operations to observe cancellation before their connections are
// force-closed. Override per DSN with the killWindow query parameter.
const DefaultKillWindow = 100 * time.Millisecond

// generation owns everything whose lifetime matches one value of the
// connection string: the cancellation context, the set of live connections,
// and the drained flag consulted by ResetSession/IsValid. A config change
// swaps generation pointers on the group; nothing else has to be mutated.
type generation struct {
	dsn       string
	redactDsn string

	// ctx is derived from the group's parent context and canceled with
	// cause ErrHotSwap when the generation is killed.
	ctx    context.Context
	cancel context.CancelCauseFunc

	// drained marks the generation retired without canceling in-flight
	// work; conns answer driver.ErrBadConn on the next pool reuse.
	drained atomic.Bool

	// ops counts operations running under opCtx, so kill can wait
	// (bounded) for them to observe cancellation before force-closing.
	// A plain atomic (rather than a WaitGroup) because operations keep
	// starting while kill waits, which WaitGroup forbids.
	ops atomic.Int64

	// mu is a leaf lock guarding conns only. No driver call, hook, or
	// other lock acquisition ever happens while holding it.
	mu    sync.Mutex
	conns map[*baseConn]struct{}

	// connector lazily caches the underlying driver's connector when it
	// implements driver.DriverContext.
	connOnce  sync.Once
	connector driver.Connector
	connErr   error
}

func newGeneration(parent context.Context, dsn string) *generation {
	ctx, cancel := context.WithCancelCause(parent)
	return &generation{
		dsn:       dsn,
		redactDsn: internal.RedactUrl(dsn),
		ctx:       ctx,
		cancel:    cancel,
		conns:     make(map[*baseConn]struct{}),
	}
}

// retired reports whether conns of this generation should be discarded by
// the pool.
func (gen *generation) retired() bool {
	return gen.drained.Load() || gen.ctx.Err() != nil
}

func (gen *generation) add(c *baseConn) {
	gen.mu.Lock()
	defer gen.mu.Unlock()
	gen.conns[c] = struct{}{}
}

func (gen *generation) remove(c *baseConn) {
	gen.mu.Lock()
	defer gen.mu.Unlock()
	delete(gen.conns, c)
}

// drain retires the generation gracefully: in-flight work continues, and
// the pool discards each conn the next time it tries to reuse it.
func (gen *generation) drain() {
	gen.drained.Store(true)
}

// kill retires the generation immediately: the generation context is
// canceled (with cause ErrHotSwap), in-flight operations get up to window to
// observe the cancellation, and then every remaining conn is force-closed.
// The bounded wait guarantees the caller (the group run loop) cannot be
// wedged by a driver that ignores context cancellation.
func (gen *generation) kill(window time.Duration) {
	gen.drained.Store(true)
	gen.cancel(ErrHotSwap)
	gen.awaitIdle(window)

	gen.mu.Lock()
	snap := make([]*baseConn, 0, len(gen.conns))
	for c := range gen.conns {
		snap = append(snap, c)
	}
	gen.mu.Unlock()

	for _, c := range snap {
		c.closeConn(true)
	}
}

// awaitIdle polls until no operations are in flight or the window elapses.
// Well-behaved drivers observe the canceled context within microseconds;
// drivers that ignore cancellation are unblocked by the conn close that
// follows in kill.
func (gen *generation) awaitIdle(window time.Duration) {
	deadline := time.Now().Add(window)
	for gen.ops.Load() > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
}

// group represents one watched hotload DSN. It receives new connection
// string values from the strategy and swaps generations in response. Its
// mutex guards only the generation pointers and the closed flag; all driver
// calls, hook emissions and strategy calls happen outside it.
type group struct {
	name         string // the full hotload DSN
	strategyName string
	strategy     Strategy
	path         string
	pathQry      string
	sqlDriver    *driverInstance
	forceKill    bool
	killWindow   time.Duration
	parentCtx    context.Context
	parentCancel context.CancelFunc
	newValChan   <-chan string

	// refs and pinned are guarded by hdriver.mu. refs counts live
	// connectors; pinned marks groups created through the legacy
	// driver.Open path, which have no teardown signal and live forever.
	refs   int
	pinned bool

	mu     sync.Mutex
	cur    *generation
	prev   *generation
	closed bool
}

// runLoop receives new connection string values from the strategy until the
// group is shut down or the strategy closes the channel. It is the only
// goroutine that swaps generations.
func (g *group) runLoop() {
	for {
		select {
		case <-g.parentCtx.Done():
			g.logf("group.runLoop", "parent context done, terminating")
			return
		case newValue, ok := <-g.newValChan:
			if !ok {
				g.logf("group.runLoop", "strategy channel closed, terminating")
				EmitWatchEvent(WatchEvent{GroupName: g.name, Strategy: g.strategyName, Path: g.path, Closed: true})
				return
			}
			g.onNewValue(newValue)
		}
	}
}

func (g *group) onNewValue(v string) {
	g.mu.Lock()
	if g.closed || v == g.cur.dsn {
		g.mu.Unlock()
		g.logf("group.onNewValue", "conn dsn not changed")
		return
	}
	old, older := g.cur, g.prev
	next := newGeneration(g.parentCtx, v)
	g.cur = next
	if g.forceKill {
		g.prev = nil
	} else {
		g.prev = old
	}
	g.mu.Unlock()

	g.logf("group.onNewValue", "conn dsn changed: '%s' -> '%s'", old.redactDsn, next.redactDsn)
	emitConfigChange(ConfigChangeEvent{
		GroupName:      g.name,
		OldRedactedDSN: old.redactDsn,
		NewRedactedDSN: next.redactDsn,
		ForceKill:      g.forceKill,
		At:             time.Now(),
	})

	if g.forceKill {
		// Cancel in-flight work on the old DSN and close its conns.
		old.kill(g.killWindow)
		return
	}
	// Graceful: the old generation keeps serving in-flight work and drains
	// through the pool; the generation before it has had its grace period
	// and is killed now.
	old.drain()
	if older != nil {
		older.kill(g.killWindow)
	}
}

// conn dials a new connection on the current generation. The underlying
// dial happens outside all locks; if a config change lands mid-dial the new
// conn belongs to an already-retired generation and self-evicts on first
// pool reuse, which the pool handles by dialing again.
func (g *group) conn(ctx context.Context) (driver.Conn, error) {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return nil, fmt.Errorf("hotload: group %q is closed", g.name)
	}
	gen := g.cur
	g.mu.Unlock()

	dsn, err := mergeConnStringOptions(gen.dsn, g.sqlDriver.options)
	if err != nil {
		return nil, err
	}
	inner, err := g.dial(ctx, gen, dsn)
	if err != nil {
		return nil, err
	}

	c := &baseConn{
		inner:     inner,
		gen:       gen,
		group:     g,
		dsn:       dsn,
		redactDsn: internal.RedactUrl(dsn),
	}
	gen.add(c)
	emitConnOpen(ConnEvent{GroupName: g.name, RedactedDSN: c.redactDsn})
	g.logf("group.conn", "opened managed conn: '%s'", c.redactDsn)
	return wrapConn(c), nil
}

// dial opens an underlying connection, using the underlying driver's
// connector when it implements driver.DriverContext (so the dial honors
// ctx), and falling back to Open otherwise — the same split database/sql
// applies.
func (g *group) dial(ctx context.Context, gen *generation, dsn string) (driver.Conn, error) {
	dc, ok := g.sqlDriver.driver.(driver.DriverContext)
	if !ok {
		return g.sqlDriver.driver.Open(dsn)
	}
	gen.connOnce.Do(func() {
		gen.connector, gen.connErr = dc.OpenConnector(dsn)
	})
	if gen.connErr != nil {
		return nil, gen.connErr
	}
	return gen.connector.Connect(ctx)
}

// shutdown tears the group down: the strategy watch is closed and the
// parent context canceled, which terminates runLoop and cancels every
// generation context. Called once the last connector referencing the group
// closes.
func (g *group) shutdown() {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return
	}
	g.closed = true
	g.mu.Unlock()

	if err := g.strategy.CloseWatch(g.path, g.pathQry); err != nil {
		g.logf("group.shutdown", "CloseWatch error: %v", err)
	}
	g.parentCancel()
	EmitWatchEvent(WatchEvent{GroupName: g.name, Strategy: g.strategyName, Path: g.path, Closed: true})
	g.logf("group.shutdown", "group closed")
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

func (g *group) logf(prefix, format string, args ...any) {
	logger.Logf(fmt.Sprintf("%s[%s]:", prefix, g.name), format, args...)
}
