package hotload

import (
	"context"
	"database/sql/driver"
	"fmt"
	"sync/atomic"
)

// connector ties a sql.DB to a group. database/sql obtains one per sql.Open
// call (through driver.DriverContext) and closes it when the sql.DB is
// closed, which releases the group reference and — once the last reference
// is gone — stops the strategy watch and the group's run loop.
type connector struct {
	h      *hdriver
	g      *group
	name   string
	closed atomic.Bool
}

var (
	_ driver.Connector = (*connector)(nil)
)

func (c *connector) Connect(ctx context.Context) (driver.Conn, error) {
	if c.closed.Load() {
		return nil, fmt.Errorf("hotload: connector for %q is closed", c.name)
	}
	return c.g.conn(ctx)
}

func (c *connector) Driver() driver.Driver {
	return c.h
}

// Close implements io.Closer; database/sql calls it from DB.Close.
func (c *connector) Close() error {
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}
	c.h.releaseGroup(c.name)
	return nil
}
