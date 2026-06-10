package hotload_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	hotload "github.com/infobloxopen/hotload/v3"
	"github.com/infobloxopen/hotload/v3/internal/dbfake"
	"github.com/infobloxopen/hotload/v3/internal/testutil"
)

// TestGracefulSwap: after a config change without forceKill, the pool
// discards old-generation conns on reuse and dials the new DSN; the old
// underlying conn is closed exactly once.
func TestGracefulSwap(t *testing.T) {
	testutil.NoLeaks(t)
	fx := newFixture(t, fxCfg{})

	if got := fx.queryDSN(); got != "dsn-1" {
		t.Fatalf("queryDSN = %q, want dsn-1", got)
	}

	fx.pushAndWait("dsn-2")

	if got := fx.queryDSN(); got != "dsn-2" {
		t.Fatalf("queryDSN after change = %q, want dsn-2", got)
	}

	conns := fx.drv.Conns()
	if len(conns) < 2 {
		t.Fatalf("expected a second conn to be dialed, got %d conns", len(conns))
	}
	old := conns[0]
	testutil.WaitFor(t, 2*time.Second, "old conn to close", old.Closed)
	if n := old.CloseCount(); n != 1 {
		t.Errorf("old conn closed %d times, want exactly 1", n)
	}
}

// TestGracefulGracePeriod: in graceful mode the previous generation's idle
// conns survive one change (the grace period) and are killed on the next
// change, without the pool ever touching them.
func TestGracefulGracePeriod(t *testing.T) {
	testutil.NoLeaks(t)
	fx := newFixture(t, fxCfg{})

	fx.queryDSN() // dial conn 1 on dsn-1
	c1 := fx.drv.Conns()[0]

	fx.pushAndWait("dsn-2")
	// Grace period: conn 1 must not be force-closed by this change.
	time.Sleep(20 * time.Millisecond)
	if c1.Closed() {
		t.Fatal("conn from previous generation was closed during its grace period")
	}

	fx.pushAndWait("dsn-3")
	// Now conn 1 belonged to the generation before the previous one — killed.
	testutil.WaitFor(t, 2*time.Second, "prev-prev conn to close", c1.Closed)
	if n := c1.CloseCount(); n != 1 {
		t.Errorf("conn closed %d times, want exactly 1", n)
	}

	if got := fx.queryDSN(); got != "dsn-3" {
		t.Fatalf("queryDSN = %q, want dsn-3", got)
	}
}

// TestSwapBackToOriginal: a -> b -> a is two real changes and ends with
// fresh conns on the original DSN.
func TestSwapBackToOriginal(t *testing.T) {
	testutil.NoLeaks(t)
	fx := newFixture(t, fxCfg{})

	fx.queryDSN()
	fx.pushAndWait("dsn-2")
	ev := fx.pushAndWait("dsn-1")
	if ev.GroupName != fx.dsn {
		t.Errorf("event group = %q, want %q", ev.GroupName, fx.dsn)
	}

	if got := fx.queryDSN(); got != "dsn-1" {
		t.Fatalf("queryDSN = %q, want dsn-1", got)
	}
	if len(fx.drv.Conns()) < 2 {
		t.Errorf("expected a fresh conn after swapping back, got %d conns", len(fx.drv.Conns()))
	}
}

// TestUnchangedValueIgnored: pushing the same value is not a change.
func TestUnchangedValueIgnored(t *testing.T) {
	testutil.NoLeaks(t)
	fx := newFixture(t, fxCfg{})

	fx.queryDSN()
	fx.push("dsn-1") // identical; processed but ignored
	fx.pushAndWait("dsn-2")
	fx.noPendingChange()

	c1 := fx.drv.Conns()[0]
	if got := fx.queryDSN(); got != "dsn-2" {
		t.Fatalf("queryDSN = %q, want dsn-2", got)
	}
	_ = c1
}

// TestForceKillCancelsInflightExec: with forceKill, an exec blocked in the
// driver is canceled by the config change, the error surfaces as ErrHotSwap,
// and the conn is closed exactly once.
func TestForceKillCancelsInflightExec(t *testing.T) {
	testutil.NoLeaks(t)

	started := make(chan struct{}, 10)
	fx := newFixture(t, fxCfg{
		params: "forceKill=true",
		execFn: func(c *dbfake.Conn, ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
			started <- struct{}{}
			<-ctx.Done()
			return nil, context.Cause(ctx)
		},
	})

	errCh := make(chan error, 1)
	go func() {
		_, err := fx.db.Exec("UPDATE x")
		errCh <- err
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("exec never reached the driver")
	}

	fx.pushAndWait("dsn-2")

	select {
	case err := <-errCh:
		if !errors.Is(err, hotload.ErrHotSwap) {
			t.Fatalf("exec error = %v, want ErrHotSwap", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("exec did not return after forceKill change")
	}

	old := fx.drv.Conns()[0]
	testutil.WaitFor(t, 2*time.Second, "old conn to close", old.Closed)
	if n := old.CloseCount(); n != 1 {
		t.Errorf("old conn closed %d times, want exactly 1", n)
	}

	fx.drv.ExecFn = nil // restore default behavior for the new generation
	if got := fx.queryDSN(); got != "dsn-2" {
		t.Fatalf("queryDSN = %q, want dsn-2", got)
	}
}

// TestForceKillBoundedByKillWindow: a driver that ignores context
// cancellation cannot wedge the run loop — after killWindow elapses the conn
// is force-closed and new work proceeds on the new DSN while the old
// operation is still parked.
func TestForceKillBoundedByKillWindow(t *testing.T) {
	testutil.NoLeaks(t)

	started := make(chan struct{}, 10)
	release := make(chan struct{})
	fx := newFixture(t, fxCfg{
		params: "forceKill=true&killWindow=30ms",
		execFn: func(c *dbfake.Conn, ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
			started <- struct{}{}
			<-release // deliberately ignores ctx
			return driver.RowsAffected(1), nil
		},
	})
	defer close(release)

	errCh := make(chan error, 1)
	go func() {
		_, err := fx.db.Exec("UPDATE x")
		errCh <- err
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("exec never reached the driver")
	}

	fx.pushAndWait("dsn-2")

	old := fx.drv.Conns()[0]
	testutil.WaitFor(t, 2*time.Second, "old conn to be force-closed", old.Closed)

	// The group must remain responsive with the old exec still parked.
	fx.drv.ExecFn = nil
	if got := fx.queryDSN(); got != "dsn-2" {
		t.Fatalf("queryDSN = %q, want dsn-2", got)
	}
	select {
	case err := <-errCh:
		t.Logf("parked exec returned early: %v", err)
	default:
		// Still parked, as expected; released by the deferred close.
	}
}

// TestRunLoopNotBlockedBySlowDial: dials happen outside group locks, so a
// slow dial must not delay config-change processing (hotload v1 serialized
// these on one mutex).
func TestRunLoopNotBlockedBySlowDial(t *testing.T) {
	testutil.NoLeaks(t)
	fx := newFixture(t, fxCfg{})
	fx.drv.OpenDelay = 300 * time.Millisecond

	dialDone := make(chan struct{})
	go func() {
		defer close(dialDone)
		fx.queryDSN() // triggers the slow dial
	}()

	time.Sleep(20 * time.Millisecond) // let the dial start
	t0 := time.Now()
	fx.pushAndWait("dsn-2")
	if elapsed := time.Since(t0); elapsed > 200*time.Millisecond {
		t.Errorf("config change took %v while a dial was in flight; the run loop appears blocked by dialing", elapsed)
	}
	<-dialDone
}

// TestOpenErrors: bad DSNs and missing registrations fail at sql.Open (the
// connector is created there), with the sentinel errors preserved from v1.
func TestOpenErrors(t *testing.T) {
	t.Run("unsupported strategy", func(t *testing.T) {
		_, err := sql.Open("hotload", "nosuchstrategy://nosuchdriver/cfg")
		if !errors.Is(err, hotload.ErrUnsupportedStrategy) {
			t.Fatalf("err = %v, want ErrUnsupportedStrategy", err)
		}
	})

	t.Run("unknown driver", func(t *testing.T) {
		fx := newFixture(t, fxCfg{noDB: true})
		// Reuse the registered strategy but point at an unregistered driver.
		dsnURL := fmt.Sprintf("%s://%s%s", schemeOf(t, fx.dsn), "nosuchdriver", fixturePath)
		_, err := sql.Open("hotload", dsnURL)
		if !errors.Is(err, hotload.ErrUnknownDriver) {
			t.Fatalf("err = %v, want ErrUnknownDriver", err)
		}
	})

	t.Run("malformed killWindow", func(t *testing.T) {
		fx := newFixture(t, fxCfg{noDB: true})
		_, err := sql.Open("hotload", fx.dsn+"?killWindow=bogus")
		if !errors.Is(err, hotload.ErrMalformedConnectionString) {
			t.Fatalf("err = %v, want ErrMalformedConnectionString", err)
		}
	})

	t.Run("watch error", func(t *testing.T) {
		fx := newFixture(t, fxCfg{noDB: true})
		_, err := sql.Open("hotload", fx.dsn+"nosuchpath")
		if err == nil {
			t.Fatal("expected watch error for unknown path")
		}
	})
}

// TestGroupTeardownAndSharing: two sql.DB handles on the same DSN share one
// watch; the watch survives the first Close and stops after the last one,
// leaving no goroutines behind.
func TestGroupTeardownAndSharing(t *testing.T) {
	testutil.NoLeaks(t)
	fx := newFixture(t, fxCfg{})

	db2, err := sql.Open("hotload", fx.dsn)
	if err != nil {
		t.Fatal(err)
	}

	if n := fx.strat.Watches(); n != 1 {
		t.Fatalf("watches = %d, want 1 (groups must be shared per DSN)", n)
	}

	if err := db2.Close(); err != nil {
		t.Fatal(err)
	}
	if n := fx.strat.Watches(); n != 1 {
		t.Fatalf("watches after first close = %d, want 1", n)
	}

	if err := fx.db.Close(); err != nil {
		t.Fatal(err)
	}
	testutil.WaitFor(t, 2*time.Second, "watch teardown", func() bool {
		return fx.strat.Watches() == 0
	})
}

// TestReopenWhileClosing races the close of a DSN's last sql.DB against a
// fresh sql.Open of the same DSN. Whatever the interleaving, the surviving
// handle must keep receiving config changes: the dying group's watch
// teardown is serialized with new watch creation, so a new group can never
// be handed a doomed update channel.
func TestReopenWhileClosing(t *testing.T) {
	testutil.NoLeaks(t)
	fx := newFixture(t, fxCfg{noDB: true})

	db, err := sql.Open("hotload", fx.dsn)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 20; i++ {
		var (
			wg  sync.WaitGroup
			db2 *sql.DB
		)
		wg.Add(2)
		go func() {
			defer wg.Done()
			db.Close()
		}()
		go func() {
			defer wg.Done()
			var err error
			db2, err = sql.Open("hotload", fx.dsn)
			if err != nil {
				t.Errorf("reopen: %v", err)
			}
		}()
		wg.Wait()
		if t.Failed() {
			t.FailNow()
		}

		// The surviving handle must observe a config change.
		want := fmt.Sprintf("dsn-rw-%d", i)
		fx.push(want)
		testutil.WaitFor(t, 5*time.Second, "change to propagate to the reopened db", func() bool {
			var got string
			return db2.QueryRow("SELECT dsn").Scan(&got) == nil && got == want
		})
		db = db2
	}
	db.Close()
}

// TestStrategyChannelClose: when the strategy closes its update channel the
// run loop exits, existing connections keep serving the last value, and a
// later db.Close still tears down cleanly.
func TestStrategyChannelClose(t *testing.T) {
	testutil.NoLeaks(t)
	fx := newFixture(t, fxCfg{})

	fx.queryDSN()
	fx.strat.CloseChan(fixturePath)

	// Existing conns keep working on the last known value.
	testutil.WaitFor(t, 2*time.Second, "queries to keep working", func() bool {
		return fx.queryDSN() == "dsn-1"
	})
}

// TestValidatorEviction: an underlying conn reporting IsValid()==false is
// discarded by the pool and replaced.
func TestValidatorEviction(t *testing.T) {
	testutil.NoLeaks(t)
	fx := newFixture(t, fxCfg{})

	fx.queryDSN()
	c1 := fx.drv.Conns()[0]
	c1.SetInvalid()

	// The pool validates on put-back/reuse; the next queries must succeed
	// on a fresh conn.
	if got := fx.queryDSN(); got != "dsn-1" {
		t.Fatalf("queryDSN = %q, want dsn-1", got)
	}
	testutil.WaitFor(t, 2*time.Second, "invalid conn to be discarded", c1.Closed)
}

func schemeOf(t *testing.T, dsn string) string {
	t.Helper()
	for i := range dsn {
		if dsn[i] == ':' {
			return dsn[:i]
		}
	}
	t.Fatalf("no scheme in %q", dsn)
	return ""
}
