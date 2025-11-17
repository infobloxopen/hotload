package hotload

import (
	"database/sql/driver"
	"sort"
	"sync"
)

var (
	mu         sync.RWMutex
	sqlDrivers = make(map[string]*driverInstance)
	strategies = make(map[string]Strategy)
)

// RegisterSQLDriver makes a database driver available by the provided name.
// If RegisterSQLDriver is called twice with the same name or if driver is nil,
// it panics.
func RegisterSQLDriver(name string, driver driver.Driver, options ...driverOption) {
	mu.Lock()
	defer mu.Unlock()
	if driver == nil {
		panic("hotload: Register driver is nil")
	}
	if _, dup := sqlDrivers[name]; dup {
		panic("hotload: Register called twice for driver " + name)
	}
	di := &driverInstance{driver: driver}
	for _, opt := range options {
		opt(di)
	}

	sqlDrivers[name] = di
}

// unregisterAll clears all registered drivers and strategies.
// For tests only.
func unregisterAll() {
	mu.Lock()
	defer mu.Unlock()
	sqlDrivers = make(map[string]*driverInstance)
	strategies = make(map[string]Strategy)
}

// SQLDrivers returns a sorted list of the names of the registered drivers.
func SQLDrivers() []string {
	mu.RLock()
	defer mu.RUnlock()
	list := make([]string, 0, len(sqlDrivers))
	for name := range sqlDrivers {
		list = append(list, name)
	}
	sort.Strings(list)
	return list
}

// RegisterStrategy makes a database driver available by the provided name.
// If RegisterStrategy is called twice with the same name or if strategy is nil,
// it panics.
func RegisterStrategy(name string, strategy Strategy) {
	mu.Lock()
	defer mu.Unlock()
	if strategy == nil {
		panic("hotload: RegisterStrategy strategy is nil")
	}
	if _, dup := strategies[name]; dup {
		panic("hotload: RegisterStrategy called twice for strategy " + name)
	}
	strategies[name] = strategy
}

// UnregisterStrategy unregisters the named driver strategy.
// Does nothing if strategy does not exist.
// Intended for internal unit-testing.
func UnregisterStrategy(name string) {
	mu.Lock()
	defer mu.Unlock()
	strategy, ok := strategies[name]
	if ok {
		if strategy != nil {
			strategy.Close()
		}
		delete(strategies, name)
	}
}

// Strategies returns a sorted list of the names of the registered drivers.
func Strategies() []string {
	mu.RLock()
	defer mu.RUnlock()
	list := make([]string, 0, len(strategies))
	for name := range strategies {
		list = append(list, name)
	}
	sort.Strings(list)
	return list
}
