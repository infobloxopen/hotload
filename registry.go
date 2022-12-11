package hotload

import (
	"fmt"
	"sort"
	"sync"
)

// Registry holds the registration for different drivers.
type Registry[DriverType any, ConnType any] struct {
	name     string
	smu      sync.RWMutex
	ddrivers map[string]Driver[DriverType, ConnType]
}

// Driver is the interface that a data source driver must
// implement. The driver must return ConnType when calling Open().
type Driver[DriverType any, ConnType any] interface {
	Open(name string) (ConnType, error)
}

// NewRegistry will create a new DriverRegistry
func NewRegistry[DriverType any, ConnType any](name string) *Registry[DriverType, ConnType] {
	return &Registry[DriverType, ConnType]{
		name:     name,
		ddrivers: make(map[string]Driver[DriverType, ConnType]),
	}
}

// Drivers returns a list of sorted driver names that
// have been registered.
func (dr *Registry[DriverType, ConnType]) Drivers() []string {
	dr.smu.RLock()
	defer dr.smu.RUnlock()
	list := make([]string, 0, len(dr.ddrivers))
	for name := range dr.ddrivers {
		list = append(list, name)
	}
	sort.Strings(list)
	return list
}

// GetDriver returns the driver with the given name, if it exists.
func (dr *Registry[DriverType, ConnType]) GetDriver(name string) (driver Driver[DriverType, ConnType], exists bool) {
	dr.smu.RLock()
	defer dr.smu.RUnlock()
	driver, exists = dr.ddrivers[name]
	return driver, exists
}

// Register will add the given Driver under the name.
func (dr *Registry[DriverType, ConnType]) Register(name string, d Driver[DriverType, ConnType]) error {
	if d == nil {
		return fmt.Errorf("%s: Register driver is nil", dr.name)
	}
	dr.smu.Lock()
	defer dr.smu.Unlock()
	if _, dup := dr.ddrivers[name]; dup {
		return fmt.Errorf("%s: Register called twice for driver %s", dr.name, name)
	}
	dr.ddrivers[name] = d
	return nil
}
