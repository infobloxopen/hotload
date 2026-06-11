package k8ssecret

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"sync"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hotload "github.com/infobloxopen/hotload/v3"
)

// TestEndToEndThroughSQLOpen drives the strategy through the real hotload
// core and database/sql: registration via this package's init, DSN parsing
// (including the leading slash the core puts on the path component), watch
// establishment at sql.Open, and update propagation into the pool.
func TestEndToEndThroughSQLOpen(t *testing.T) {
	cs := fakeClientset(t, makeSecret("prod", "mydb", map[string]string{"dsn": "dsn-1"}))

	// Swap the package-registered strategy instance for one bound to the
	// fake clientset; restore a pristine instance afterwards.
	hotload.UnregisterStrategy("k8ssecret")
	s := NewStrategyWithClientset(cs)
	s.backoff = 20 * time.Millisecond
	hotload.RegisterStrategy("k8ssecret", s)
	t.Cleanup(func() {
		hotload.UnregisterStrategy("k8ssecret")
		hotload.RegisterStrategy("k8ssecret", NewStrategy())
	})

	registerFakeDriverOnce()

	db, err := sql.Open("hotload", "k8ssecret://k8sfake/mydb?namespace=prod&dsn=dsn")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	queryDSN := func() (string, error) {
		var dsn string
		err := db.QueryRow("SELECT dsn").Scan(&dsn)
		return dsn, err
	}

	got, err := queryDSN()
	if err != nil {
		t.Fatal(err)
	}
	if got != "dsn-1" {
		t.Fatalf("initial query routed to %q, want dsn-1", got)
	}

	// Rotate the secret; the pool must converge on the new DSN.
	if _, err := cs.CoreV1().Secrets("prod").Update(context.Background(),
		makeSecret("prod", "mydb", map[string]string{"dsn": "dsn-2"}), metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := queryDSN()
		if err == nil && got == "dsn-2" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pool did not converge on dsn-2 (last: %q, err: %v)", got, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestSQLOpenErrorsOnMissingSecret: configuration problems surface at
// sql.Open (the hotload core starts the watch there).
func TestSQLOpenErrorsOnMissingSecret(t *testing.T) {
	cs := fakeClientset(t)

	hotload.UnregisterStrategy("k8ssecret")
	hotload.RegisterStrategy("k8ssecret", NewStrategyWithClientset(cs))
	t.Cleanup(func() {
		hotload.UnregisterStrategy("k8ssecret")
		hotload.RegisterStrategy("k8ssecret", NewStrategy())
	})

	registerFakeDriverOnce()

	if _, err := sql.Open("hotload", "k8ssecret://k8sfake/nosuchsecret?namespace=prod"); err == nil {
		t.Fatal("expected sql.Open to fail for a missing secret")
	}
}

// A minimal driver whose conns answer "SELECT dsn" with the DSN they were
// opened with, so tests can observe which generation served a query. (The
// hotload core's richer fake lives in its internal packages, which this
// module cannot import.)
var registerFakeDriver sync.Once

func registerFakeDriverOnce() {
	registerFakeDriver.Do(func() {
		hotload.RegisterSQLDriver("k8sfake", fakeDriver{})
	})
}

type fakeDriver struct{}

func (fakeDriver) Open(dsn string) (driver.Conn, error) { return &fakeConn{dsn: dsn}, nil }

type fakeConn struct{ dsn string }

func (c *fakeConn) Prepare(query string) (driver.Stmt, error) { return &fakeStmt{conn: c}, nil }
func (c *fakeConn) Close() error                              { return nil }
func (c *fakeConn) Begin() (driver.Tx, error)                 { return fakeTx{}, nil }

func (c *fakeConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	return &fakeRows{dsn: c.dsn}, nil
}

type fakeStmt struct{ conn *fakeConn }

func (s *fakeStmt) Close() error  { return nil }
func (s *fakeStmt) NumInput() int { return -1 }
func (s *fakeStmt) Exec(args []driver.Value) (driver.Result, error) {
	return driver.RowsAffected(1), nil
}
func (s *fakeStmt) Query(args []driver.Value) (driver.Rows, error) {
	return &fakeRows{dsn: s.conn.dsn}, nil
}

type fakeTx struct{}

func (fakeTx) Commit() error   { return nil }
func (fakeTx) Rollback() error { return nil }

type fakeRows struct {
	dsn  string
	done bool
}

func (r *fakeRows) Columns() []string { return []string{"dsn"} }
func (r *fakeRows) Close() error      { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	dest[0] = r.dsn
	r.done = true
	return nil
}
