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
//	   _ "github.com/infobloxopen/hotload/strategy/fsnotify"
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

	"github.com/infobloxopen/hotload/internal/core"
	"github.com/infobloxopen/hotload/logger"
	"github.com/infobloxopen/hotload/metrics"
)

var (
	ErrUnsupportedStrategy       = fmt.Errorf("unsupported hotload strategy")
	ErrMalformedConnectionString = fmt.Errorf("malformed hotload connection string")
	ErrUnknownDriver             = fmt.Errorf("target driver is not registered with hotload")
)

func init() {
	sql.Register("hotload", &hdriver{})
}

// hdriver is the hotload driver that implements both driver.Driver and driver.DriverContext.
// Go 1.20+ prefers OpenConnector (driver.DriverContext), so Open() simply delegates to it.
type hdriver struct{}

// Open implements driver.Driver by delegating to OpenConnector().Connect().
// This ensures both paths use the same modern implementation in internal/core.
func (h *hdriver) Open(name string) (driver.Conn, error) {
	connector, err := h.OpenConnector(name)
	if err != nil {
		return nil, err
	}
	return connector.Connect(context.Background())
}

// OpenConnector implements driver.DriverContext for Go 1.20+ connector-based opening.
func (h *hdriver) OpenConnector(name string) (driver.Connector, error) {
	parsed, err := core.Parse(name)
	if err != nil {
		return nil, err
	}

	mu.RLock()
	strategy, ok := strategies[parsed.Strategy]
	if !ok {
		mu.RUnlock()
		return nil, ErrUnsupportedStrategy
	}

	sqlDriver, ok := sqlDrivers[parsed.TargetDriver]
	if !ok {
		mu.RUnlock()
		return nil, ErrUnknownDriver
	}
	mu.RUnlock()

	// Strategy interface now matches core.Strategy interface - no adapter needed
	return core.NewConnector(
		parsed,
		strategy,
		sqlDriver.driver,
		logger.GetLogger(),
		metrics.GetProvider(),
	)
}
