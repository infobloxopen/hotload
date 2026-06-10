package hotload_test

import (
	"context"
	"testing"
	"time"

	hotload "github.com/infobloxopen/hotload/v3"
	"github.com/infobloxopen/hotload/v3/internal/testutil"
)

// collectTxEvents subscribes a hook capturing TxEvents into a channel.
func collectTxEvents(t *testing.T) chan hotload.TxEvent {
	t.Helper()
	events := make(chan hotload.TxEvent, 100)
	hotload.RegisterHooks(hotload.Hooks{
		OnTxComplete: func(ev hotload.TxEvent) {
			select {
			case events <- ev:
			default:
			}
		},
	})
	return events
}

func awaitTxEvent(t *testing.T, events chan hotload.TxEvent) hotload.TxEvent {
	t.Helper()
	select {
	case ev := <-events:
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for TxEvent")
		return hotload.TxEvent{}
	}
}

// TestTxStatementCounters: exec and query statements run on a conn are
// reported when its transaction completes, along with the labels carried by
// the transaction's context.
func TestTxStatementCounters(t *testing.T) {
	testutil.NoLeaks(t)
	fx := newFixture(t, fxCfg{})
	events := collectTxEvents(t)

	labels := map[string]string{"grpc_service": "svc", "grpc_method": "m"}
	ctx := hotload.ContextWithExecLabels(context.Background(), labels)

	tx, err := fx.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("UPDATE x"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("UPDATE y"); err != nil {
		t.Fatal(err)
	}
	var dsn string
	if err := tx.QueryRow("SELECT dsn").Scan(&dsn); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	ev := awaitTxEvent(t, events)
	if ev.ExecStmts != 2 || ev.QueryStmts != 1 {
		t.Errorf("counters = exec %d query %d, want exec 2 query 1", ev.ExecStmts, ev.QueryStmts)
	}
	if !ev.Committed {
		t.Error("Committed = false, want true")
	}
	got := hotload.GetExecLabelsFromContext(ev.Ctx)
	if got["grpc_service"] != "svc" || got["grpc_method"] != "m" {
		t.Errorf("labels from event ctx = %v, want %v", got, labels)
	}
}

// TestTxRollbackEvent: rollbacks report Committed=false and reset counters
// for the next transaction.
func TestTxRollbackEvent(t *testing.T) {
	testutil.NoLeaks(t)
	fx := newFixture(t, fxCfg{})
	events := collectTxEvents(t)

	ctx := context.Background()
	tx, err := fx.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("UPDATE x"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	ev := awaitTxEvent(t, events)
	if ev.Committed {
		t.Error("Committed = true, want false")
	}
	if ev.ExecStmts != 1 {
		t.Errorf("ExecStmts = %d, want 1", ev.ExecStmts)
	}

	// Counters must reset between transactions on the same conn.
	tx2, err := fx.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatal(err)
	}
	ev2 := awaitTxEvent(t, events)
	if ev2.ExecStmts != 0 || ev2.QueryStmts != 0 {
		t.Errorf("second tx counters = exec %d query %d, want 0/0 (reset)", ev2.ExecStmts, ev2.QueryStmts)
	}
}

// TestTxCountsPreparedStatements: statements executed through prepared
// statements count toward the transaction counters — a blind spot in v1,
// which did not wrap driver.Stmt.
func TestTxCountsPreparedStatements(t *testing.T) {
	testutil.NoLeaks(t)
	fx := newFixture(t, fxCfg{})
	events := collectTxEvents(t)

	tx, err := fx.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	stmt, err := tx.Prepare("UPDATE x")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stmt.Exec(); err != nil {
		t.Fatal(err)
	}
	if _, err := stmt.Exec(); err != nil {
		t.Fatal(err)
	}
	if err := stmt.Close(); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	ev := awaitTxEvent(t, events)
	if ev.ExecStmts != 2 {
		t.Errorf("ExecStmts = %d, want 2 (prepared statement execs must count)", ev.ExecStmts)
	}
}

// TestConnAndWatchEvents: conn open/close and watch hooks fire with the
// group name and redacted DSNs.
func TestConnAndWatchEvents(t *testing.T) {
	testutil.NoLeaks(t)

	fx := newFixture(t, fxCfg{})
	opens := make(chan hotload.ConnEvent, 10)
	closes := make(chan hotload.ConnEvent, 10)
	hotload.RegisterHooks(hotload.Hooks{
		OnConnOpen:  func(ev hotload.ConnEvent) { opens <- ev },
		OnConnClose: func(ev hotload.ConnEvent) { closes <- ev },
	})

	fx.queryDSN()
	select {
	case ev := <-opens:
		if ev.GroupName != fx.dsn {
			t.Errorf("open event group = %q, want %q", ev.GroupName, fx.dsn)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no conn open event")
	}

	fx.pushAndWait("dsn-2")
	fx.queryDSN()
	select {
	case ev := <-closes:
		if ev.GroupName != fx.dsn {
			t.Errorf("close event group = %q, want %q", ev.GroupName, fx.dsn)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no conn close event after swap")
	}
}
