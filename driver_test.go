package hotload_test

import (
	"database/sql"
	"database/sql/driver"
	"io"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/infobloxopen/hotload"
	"github.com/infobloxopen/hotload/fsnotify"
	"github.com/infobloxopen/hotload/logger"
)

var configFile string
var configFileDir string

func testLogger(args ...any) {
	log.Println(args...)
}

func TestMain(m *testing.M) {
	logger.WithLogger(testLogger)
	logger.WithErrLogger(testLogger)

	driver := &dummyDriver{}
	hotload.RegisterSQLDriver("dummydriver", driver)

	var err error
	configFile, err = os.Getwd()
	if err != nil {
		log.Fatalf("Failed to get working directory: %v", err)
	}
	configFileDir = configFile + "/testdata/"
	configFile += "/testdata/myconfig.txt"

	os.Exit(m.Run())
}

// dummyDriver is a minimal driver.Driver implementation for testing hotload's logic
type dummyDriver struct{}

func (d *dummyDriver) Open(name string) (driver.Conn, error) {
	return &dummyConn{dsn: name}, nil
}

type dummyConn struct {
	dsn string
}

func (c *dummyConn) Prepare(query string) (driver.Stmt, error) {
	return &dummyStmt{}, nil
}

func (c *dummyConn) Close() error {
	return nil
}

func (c *dummyConn) Begin() (driver.Tx, error) {
	return &dummyTx{}, nil
}

func (c *dummyConn) Ping(ctx interface{}) error {
	return nil
}

type dummyStmt struct{}

func (s *dummyStmt) Close() error {
	return nil
}

func (s *dummyStmt) NumInput() int {
	return 0
}

func (s *dummyStmt) Exec(args []driver.Value) (driver.Result, error) {
	return &dummyResult{}, nil
}

func (s *dummyStmt) Query(args []driver.Value) (driver.Rows, error) {
	return &dummyRows{}, nil
}

type dummyTx struct{}

func (t *dummyTx) Commit() error {
	return nil
}

func (t *dummyTx) Rollback() error {
	return nil
}

type dummyResult struct{}

func (r *dummyResult) LastInsertId() (int64, error) {
	return 0, nil
}

func (r *dummyResult) RowsAffected() (int64, error) {
	return 0, nil
}

type dummyRows struct{}

func (r *dummyRows) Columns() []string {
	return []string{}
}

func (r *dummyRows) Close() error {
	return nil
}

func (r *dummyRows) Next(dest []driver.Value) error {
	return io.EOF
}

func TestRegisterSQLDriver_PanicOnDuplicate(t *testing.T) {
	driver := &dummyDriver{}
	defer func() {
		if r := recover(); r != nil {
			msg := r.(string)
			if !strings.Contains(msg, "Register called twice for driver") {
				t.Fatalf("Expected panic message to contain 'Register called twice for driver' but got: %s", msg)
			}
		} else {
			t.Fatal("Expected RegisterSQLDriver to panic when registering duplicate driver")
		}
	}()
	hotload.RegisterSQLDriver("dummydriver", driver)
}

func TestRegisterSQLDriver_PanicOnNilDriver(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			msg := r.(string)
			if !strings.Contains(msg, "Register driver is nil") {
				t.Fatalf("Expected panic message to contain 'Register driver is nil' but got: %s", msg)
			}
		} else {
			t.Fatal("Expected RegisterSQLDriver to panic with nil driver")
		}
	}()
	hotload.RegisterSQLDriver("", nil)
}

func TestRegisterStrategy_PanicOnDuplicate(t *testing.T) {
	strat := fsnotify.NewStrategy()
	defer func() {
		if r := recover(); r != nil {
			msg := r.(string)
			if !strings.Contains(msg, "RegisterStrategy called twice for strategy") {
				t.Fatalf("Expected panic message to contain 'RegisterStrategy called twice for strategy' but got: %s", msg)
			}
		} else {
			t.Fatal("Expected RegisterStrategy to panic when registering duplicate strategy")
		}
	}()
	hotload.RegisterStrategy("fsnotify", strat)
}

func TestRegisterStrategy_PanicOnNilStrategy(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			msg := r.(string)
			if !strings.Contains(msg, "strategy is nil") {
				t.Fatalf("Expected panic message to contain 'strategy is nil' but got: %s", msg)
			}
		} else {
			t.Fatal("Expected RegisterStrategy to panic with nil strategy")
		}
	}()
	hotload.RegisterStrategy("", nil)
}

func TestOpen_ErrorWithUnknownDriver(t *testing.T) {
	db, err := sql.Open("hotload", "fsnotify://sqlmaybe?"+configFile)
	if err != nil {
		t.Fatalf("Expected no error opening db but got: %v", err)
	}
	err = db.Ping()
	if err == nil {
		t.Fatal("Expected error when pinging with unknown driver")
	}
	if err != hotload.ErrUnknownDriver {
		t.Fatalf("Expected ErrUnknownDriver but got: %v", err)
	}
}

func TestOpen_NoErrorWithRegisteredDriver(t *testing.T) {
	db, err := sql.Open("hotload", "fsnotify://dummydriver"+configFile)
	if err != nil {
		t.Fatalf("Expected no error opening db but got: %v", err)
	}

	if err := db.Ping(); err != nil {
		t.Fatalf("Expected no error pinging db but got: %v", err)
	}
}

func TestOpen_UnsupportedStrategy(t *testing.T) {
	db, err := sql.Open("hotload", "fstransmogrify://dummydriver/"+configFile)
	if err != nil {
		t.Fatalf("Expected no error opening db but got: %v", err)
	}
	err = db.Ping()
	if err == nil {
		t.Fatal("Expected error with unsupported strategy")
	}
	if err != hotload.ErrUnsupportedStrategy {
		t.Fatalf("Expected ErrUnsupportedStrategy but got: %v", err)
	}
}

func TestOpen_ErrorWhenConfigFileNotFound(t *testing.T) {
	db, err := sql.Open("hotload", "fsnotify://dummydriver/temple/run/2021-edition")
	if err != nil {
		t.Fatalf("Expected no error opening db but got: %v", err)
	}
	err = db.Ping()
	if err == nil {
		t.Fatal("Expected error when config file not found")
	}
}

func TestOpen_ErrorWithUnparseableURL(t *testing.T) {
	db, err := sql.Open("hotload", "://")
	if err != nil {
		t.Fatalf("Expected no error opening db but got: %v", err)
	}
	err = db.Ping()
	if err == nil {
		t.Fatal("Expected error with unparseable URL")
	}
	if !strings.Contains(err.Error(), "missing protocol scheme") {
		t.Fatalf("Expected error message to contain 'missing protocol scheme' but got: %v", err)
	}
}

func TestSQLDrivers_ContainsDummyDriver(t *testing.T) {
	drivers := hotload.SQLDrivers()
	found := false
	for _, d := range drivers {
		if d == "dummydriver" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("Expected SQLDrivers to contain 'dummydriver'")
	}
}
