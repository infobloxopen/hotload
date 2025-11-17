package hotload

import "database/sql/driver"

// driverInstance holds a SQL driver and its associated options.
type driverInstance struct {
	driver  driver.Driver
	options map[string]string
}

// driverOption is a function that configures a driverInstance.
type driverOption func(*driverInstance)

// WithDriverOptions allows you to specify query parameters to the underlying driver.
// The underlying driver must support URL style connection strings. The given options
// are appended to the connection string when a connection is opened.
func WithDriverOptions(options map[string]string) driverOption {
	return func(d *driverInstance) {
		if d.options == nil {
			d.options = make(map[string]string)
		}
		for k, v := range options {
			d.options[k] = v
		}
	}
}
