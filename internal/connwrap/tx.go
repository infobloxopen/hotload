package connwrap

import (
	"database/sql/driver"
)

// Tx wraps a driver.Tx with refcounting through its parent connection.
type Tx struct {
	tx   driver.Tx
	conn *Conn
}

// Commit implements driver.Tx.
func (t *Tx) Commit() error {
	t.conn.IncInFlight()
	defer t.conn.DecInFlight()
	return t.tx.Commit()
}

// Rollback implements driver.Tx.
func (t *Tx) Rollback() error {
	t.conn.IncInFlight()
	defer t.conn.DecInFlight()
	return t.tx.Rollback()
}
