// Package integration tests hotload against a real PostgreSQL server using
// the lib/pq driver and the fsnotify strategy.
//
// Start the database with `make postgres-docker-compose-up` (or point the
// HOTLOAD_INTEGRATION_TEST_POSTGRES_HOST/PORT environment variables at an
// existing server provisioned with docker/intgtest_init.sql). Tests skip
// themselves when no server is reachable.
package integration

import (
	"database/sql"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	hotload "github.com/infobloxopen/hotload/v3"
	_ "github.com/infobloxopen/hotload/v3/fsnotify"
	"github.com/lib/pq"
)

var (
	postgresHost = "localhost"
	postgresPort = "5432"

	adminUser  = "admin"
	adminPass  = "test"
	testDbUser = "uuser"

	// passSeq makes every password rotation unique across tests.
	passSeq atomic.Int64

	setupOnce sync.Once
	setupErr  error
	adminDB   *sql.DB
)

func TestMain(m *testing.M) {
	hotload.RegisterSQLDriver("postgres", &pq.Driver{})

	if h := strings.TrimSpace(os.Getenv("HOTLOAD_INTEGRATION_TEST_POSTGRES_HOST")); h != "" {
		postgresHost = h
	}
	if p := strings.TrimSpace(os.Getenv("HOTLOAD_INTEGRATION_TEST_POSTGRES_PORT")); p != "" {
		postgresPort = p
	}

	os.Exit(m.Run())
}

func adminDsn(database string) string {
	return fmt.Sprintf("postgresql://%s:%s@%s:%s/%s?sslmode=disable",
		adminUser, adminPass, postgresHost, postgresPort, database)
}

func userDsn(pass string) string {
	return fmt.Sprintf("postgresql://%s:%s@%s:%s/hldatabase?sslmode=disable",
		testDbUser, pass, postgresHost, postgresPort)
}

// enabled reports whether the integration tests were explicitly requested:
// either HOTLOAD_INTEGRATION_TESTS is truthy (set by `make
// local-integration-tests`) or a postgres host/port was pointed at via the
// environment. An implicit "is something listening on 5432" probe is not
// enough — an unrelated local postgres would fail authentication instead of
// skipping.
func enabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("HOTLOAD_INTEGRATION_TESTS"))) {
	case "1", "true", "yes":
		return true
	}
	return os.Getenv("HOTLOAD_INTEGRATION_TEST_POSTGRES_HOST") != "" ||
		os.Getenv("HOTLOAD_INTEGRATION_TEST_POSTGRES_PORT") != ""
}

// requirePostgres skips the test unless integration testing was requested
// and the server is reachable; it lazily provisions the test user and table.
func requirePostgres(t *testing.T) {
	t.Helper()
	if !enabled() {
		t.Skip("skipping: integration tests not requested (run via `make local-integration-tests`, or set HOTLOAD_INTEGRATION_TESTS=1)")
	}
	addr := net.JoinHostPort(postgresHost, postgresPort)
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Skipf("skipping: postgres not reachable at %s (start it with `make postgres-docker-compose-up`): %v", addr, err)
	}
	conn.Close()

	setupOnce.Do(func() {
		adminDB, setupErr = sql.Open("postgres", adminDsn("hldatabase"))
		if setupErr != nil {
			return
		}
		stmts := []string{
			"DROP TABLE IF EXISTS test",
			"DROP USER IF EXISTS " + testDbUser,
			fmt.Sprintf("CREATE USER %s WITH PASSWORD '%s'", testDbUser, nextPass(0)),
			"CREATE TABLE IF NOT EXISTS test (cnum INT, csource TEXT, created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP)",
			"GRANT ALL ON test TO PUBLIC",
		}
		for _, stmt := range stmts {
			if _, setupErr = adminDB.Exec(stmt); setupErr != nil {
				setupErr = fmt.Errorf("setup %q: %w", stmt, setupErr)
				return
			}
		}
	})
	if setupErr != nil {
		t.Fatalf("postgres setup failed: %v", setupErr)
	}
}

// nextPass returns the password for rotation n (or the next unique one when
// n is 0).
func nextPass(n int64) string {
	if n == 0 {
		n = passSeq.Add(1)
	}
	return fmt.Sprintf("ppass%d", n)
}

// rotatePassword sets a fresh password for the test user on the server and
// returns it. Existing sessions stay valid; only new dials use the new one.
func rotatePassword(t *testing.T) string {
	t.Helper()
	pass := nextPass(0)
	if _, err := adminDB.Exec(fmt.Sprintf("ALTER USER %s WITH PASSWORD '%s'", testDbUser, pass)); err != nil {
		t.Fatalf("rotatePassword: %v", err)
	}
	return pass
}

// dsnFile manages the config file watched by the fsnotify strategy. Each
// test gets its own path, hence its own hotload group.
type dsnFile struct {
	t    *testing.T
	path string
}

func newDsnFile(t *testing.T, dsn string) *dsnFile {
	t.Helper()
	f := &dsnFile{t: t, path: filepath.Join(t.TempDir(), "dsn.txt")}
	f.set(dsn)
	return f
}

func (f *dsnFile) set(dsn string) {
	f.t.Helper()
	if err := os.WriteFile(f.path, []byte(dsn), 0o644); err != nil {
		f.t.Fatalf("writing dsn file: %v", err)
	}
}

// openHotload opens a hotload sql.DB watching f.
func openHotload(t *testing.T, f *dsnFile, params string) *sql.DB {
	t.Helper()
	url := "fsnotify://postgres" + f.path
	if params != "" {
		url += "?" + params
	}
	db, err := sql.Open("hotload", url)
	if err != nil {
		t.Fatalf("sql.Open(%q): %v", url, err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Ping(); err != nil {
		t.Fatalf("ping through hotload: %v", err)
	}
	return db
}

// waitForQueryValue polls query until it returns want or the timeout
// elapses.
func waitForQueryValue(t *testing.T, db *sql.DB, query, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last string
	var lastErr error
	for time.Now().Before(deadline) {
		lastErr = db.QueryRow(query).Scan(&last)
		if lastErr == nil && last == want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q to return %q (last value %q, last err %v)", query, want, last, lastErr)
}
