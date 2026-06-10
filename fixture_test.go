package hotload_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	hotload "github.com/infobloxopen/hotload/v3"
	"github.com/infobloxopen/hotload/v3/internal/dbfake"
)

// fixtureSeq makes registered driver/strategy names unique per fixture;
// hotload registries are global and panic on duplicates.
var fixtureSeq atomic.Int64

const fixturePath = "/cfg"

type fxCfg struct {
	caps    dbfake.Caps // dbfake.CapsModern if zero and rawCaps unset
	rawCaps bool        // use caps even if zero (a bare conn)
	params  string      // hotload DSN query params, e.g. "forceKill=true"
	initial string      // initial underlying DSN; default "dsn-1"
	execFn  func(c *dbfake.Conn, ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error)
	queryFn func(c *dbfake.Conn, ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error)
	noDB    bool // register only; do not sql.Open
}

type fixture struct {
	t       *testing.T
	db      *sql.DB
	drv     *dbfake.Driver
	strat   *dbfake.Strategy
	dsn     string // the hotload DSN
	changes chan hotload.ConfigChangeEvent
}

// newFixture registers a fake driver and strategy under unique names, opens
// a hotload sql.DB on them, and subscribes to config-change events. Hooks
// are reset so events from earlier tests cannot interfere; tests using
// fixtures must not run in parallel.
func newFixture(t *testing.T, cfg fxCfg) *fixture {
	t.Helper()

	caps := cfg.caps
	if caps == 0 && !cfg.rawCaps {
		caps = dbfake.CapsModern
	}
	initial := cfg.initial
	if initial == "" {
		initial = "dsn-1"
	}

	n := fixtureSeq.Add(1)
	stratName := fmt.Sprintf("fakestrat%d", n)
	drvName := fmt.Sprintf("fakedrv%d", n)

	fx := &fixture{
		t:       t,
		drv:     &dbfake.Driver{Caps: caps, ExecFn: cfg.execFn, QueryFn: cfg.queryFn},
		strat:   dbfake.NewStrategy(map[string]string{fixturePath: initial}),
		changes: make(chan hotload.ConfigChangeEvent, 100),
	}

	hotload.ResetHooks()
	t.Cleanup(hotload.ResetHooks)
	hotload.RegisterHooks(hotload.Hooks{
		OnConfigChange: func(ev hotload.ConfigChangeEvent) {
			select {
			case fx.changes <- ev:
			default:
			}
		},
	})

	hotload.RegisterSQLDriver(drvName, fx.drv)
	hotload.RegisterStrategy(stratName, fx.strat)
	t.Cleanup(func() { hotload.UnregisterStrategy(stratName) })

	fx.dsn = stratName + "://" + drvName + fixturePath
	if cfg.params != "" {
		fx.dsn += "?" + cfg.params
	}

	if !cfg.noDB {
		db, err := sql.Open("hotload", fx.dsn)
		if err != nil {
			t.Fatalf("sql.Open(%q): %v", fx.dsn, err)
		}
		t.Cleanup(func() { db.Close() })
		fx.db = db
	}
	return fx
}

// queryDSN reports which underlying DSN served the query — the fake's
// default result set is one row holding the conn's DSN.
func (fx *fixture) queryDSN() string {
	fx.t.Helper()
	var dsn string
	if err := fx.db.QueryRow("SELECT dsn").Scan(&dsn); err != nil {
		fx.t.Fatalf("queryDSN: %v", err)
	}
	return dsn
}

// push delivers a new value to the strategy watcher; it returns once the
// group run loop has received (but not necessarily processed) it.
func (fx *fixture) push(value string) {
	fx.t.Helper()
	fx.strat.Push(fixturePath, value)
}

// pushAndWait pushes a changed value and waits for the resulting
// config-change event. Connection retirement happens after the event fires,
// so tests must still poll for conn-state assertions.
func (fx *fixture) pushAndWait(value string) hotload.ConfigChangeEvent {
	fx.t.Helper()
	fx.push(value)
	select {
	case ev := <-fx.changes:
		return ev
	case <-time.After(5 * time.Second):
		fx.t.Fatalf("timed out waiting for config change event after pushing %q", value)
		return hotload.ConfigChangeEvent{}
	}
}

// noPendingChange asserts no config-change event is buffered.
func (fx *fixture) noPendingChange() {
	fx.t.Helper()
	select {
	case ev := <-fx.changes:
		fx.t.Fatalf("unexpected config change event: %+v", ev)
	default:
	}
}

// rawConn hands the wrapped driver.Conn of a pooled connection to fn.
func (fx *fixture) rawConn(fn func(dc driver.Conn)) {
	fx.t.Helper()
	ctx := context.Background()
	conn, err := fx.db.Conn(ctx)
	if err != nil {
		fx.t.Fatalf("db.Conn: %v", err)
	}
	defer conn.Close()
	err = conn.Raw(func(dc any) error {
		fn(dc.(driver.Conn))
		return nil
	})
	if err != nil {
		fx.t.Fatalf("conn.Raw: %v", err)
	}
}
