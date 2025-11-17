package hotload

import (
	"database/sql/driver"
	"reflect"
	"testing"

	"github.com/infobloxopen/hotload/internal/testkit"
)

// TestRegisterSQLDriver_Success verifies successful driver registration.
//
// Scenario:
//   - Register a new SQL driver with a unique name
//
// Expected Outcome:
//   - Driver is registered successfully
//   - Driver appears in SQLDrivers() list
//   - Driver can be retrieved and used
func TestRegisterSQLDriver_Success(t *testing.T) {
	defer unregisterAll() // Clean up after test

	driverName := "test-driver-success"
	fakeDriver := testkit.NewFakeDriver()

	// Register the driver
	RegisterSQLDriver(driverName, fakeDriver)

	// Verify it appears in the list
	drivers := SQLDrivers()
	found := false
	for _, name := range drivers {
		if name == driverName {
			found = true
			break
		}
	}

	if !found {
		t.Errorf("Driver %q not found in SQLDrivers() list: %v", driverName, drivers)
	}

	// Verify we can retrieve it
	mu.RLock()
	di, exists := sqlDrivers[driverName]
	mu.RUnlock()

	if !exists {
		t.Fatal("Driver was not registered")
	}

	if di.driver != fakeDriver {
		t.Error("Retrieved driver does not match registered driver")
	}
}

// TestRegisterSQLDriver_WithOptions verifies driver registration with options.
//
// Scenario:
//   - Register a driver with custom options
//
// Expected Outcome:
//   - Driver is registered with options preserved
//   - Options can be retrieved
func TestRegisterSQLDriver_WithOptions(t *testing.T) {
	defer unregisterAll()

	driverName := "test-driver-with-options"
	fakeDriver := testkit.NewFakeDriver()
	options := map[string]string{
		"sslmode":         "require",
		"connect_timeout": "10",
	}

	RegisterSQLDriver(driverName, fakeDriver, WithDriverOptions(options))

	mu.RLock()
	di, exists := sqlDrivers[driverName]
	mu.RUnlock()

	if !exists {
		t.Fatal("Driver was not registered")
	}

	if !reflect.DeepEqual(di.options, options) {
		t.Errorf("Options mismatch: got %v, want %v", di.options, options)
	}
}

// TestRegisterSQLDriver_PanicOnNil verifies panic when registering nil driver.
//
// Scenario:
//   - Attempt to register a nil driver
//
// Expected Outcome:
//   - Function panics with appropriate message
func TestRegisterSQLDriver_PanicOnNil(t *testing.T) {
	defer unregisterAll()
	defer func() {
		if r := recover(); r == nil {
			t.Error("RegisterSQLDriver did not panic with nil driver")
		} else {
			msg := r.(string)
			if msg != "hotload: Register driver is nil" {
				t.Errorf("Wrong panic message: %v", msg)
			}
		}
	}()

	RegisterSQLDriver("test-nil", nil)
}

// TestRegisterSQLDriver_PanicOnDuplicate verifies panic on duplicate registration.
//
// Scenario:
//   - Register a driver twice with the same name
//
// Expected Outcome:
//   - Second registration panics with appropriate message
func TestRegisterSQLDriver_PanicOnDuplicate(t *testing.T) {
	defer unregisterAll()

	driverName := "test-duplicate"
	fakeDriver := testkit.NewFakeDriver()

	// First registration should succeed
	RegisterSQLDriver(driverName, fakeDriver)

	// Second registration should panic
	defer func() {
		if r := recover(); r == nil {
			t.Error("RegisterSQLDriver did not panic on duplicate registration")
		}
	}()

	RegisterSQLDriver(driverName, fakeDriver)
}

// TestRegisterStrategy_Success verifies successful strategy registration.
//
// Scenario:
//   - Register a new strategy with a unique name
//
// Expected Outcome:
//   - Strategy is registered successfully
//   - Strategy appears in Strategies() list
func TestRegisterStrategy_Success(t *testing.T) {
	defer unregisterAll()

	strategyName := "test-strategy-success"
	fakeStrategy := testkit.NewFakeStrategy()

	RegisterStrategy(strategyName, fakeStrategy)

	// Verify it appears in the list
	strats := Strategies()
	found := false
	for _, name := range strats {
		if name == strategyName {
			found = true
			break
		}
	}

	if !found {
		t.Errorf("Strategy %q not found in Strategies() list: %v", strategyName, strats)
	}

	// Verify we can retrieve it
	mu.RLock()
	strategy, exists := strategies[strategyName]
	mu.RUnlock()

	if !exists {
		t.Fatal("Strategy was not registered")
	}

	if strategy != fakeStrategy {
		t.Error("Retrieved strategy does not match registered strategy")
	}
}

// TestRegisterStrategy_PanicOnNil verifies panic when registering nil strategy.
//
// Scenario:
//   - Attempt to register a nil strategy
//
// Expected Outcome:
//   - Function panics with appropriate message
func TestRegisterStrategy_PanicOnNil(t *testing.T) {
	defer unregisterAll()
	defer func() {
		if r := recover(); r == nil {
			t.Error("RegisterStrategy did not panic with nil strategy")
		} else {
			msg := r.(string)
			if msg != "hotload: RegisterStrategy strategy is nil" {
				t.Errorf("Wrong panic message: %v", msg)
			}
		}
	}()

	RegisterStrategy("test-nil", nil)
}

// TestRegisterStrategy_PanicOnDuplicate verifies panic on duplicate registration.
//
// Scenario:
//   - Register a strategy twice with the same name
//
// Expected Outcome:
//   - Second registration panics with appropriate message
func TestRegisterStrategy_PanicOnDuplicate(t *testing.T) {
	defer unregisterAll()

	strategyName := "test-duplicate-strategy"
	fakeStrategy := testkit.NewFakeStrategy()

	RegisterStrategy(strategyName, fakeStrategy)

	defer func() {
		if r := recover(); r == nil {
			t.Error("RegisterStrategy did not panic on duplicate registration")
		}
	}()

	RegisterStrategy(strategyName, testkit.NewFakeStrategy())
}

// TestUnregisterStrategy verifies strategy unregistration.
//
// Scenario:
//   - Register a strategy, then unregister it
//
// Expected Outcome:
//   - Strategy is removed from registry
//   - Strategy.Close() is called during unregistration
func TestUnregisterStrategy(t *testing.T) {
	defer unregisterAll()

	strategyName := "test-unregister"
	fakeStrategy := testkit.NewFakeStrategy()

	RegisterStrategy(strategyName, fakeStrategy)

	// Verify it's registered
	mu.RLock()
	_, exists := strategies[strategyName]
	mu.RUnlock()

	if !exists {
		t.Fatal("Strategy was not registered")
	}

	// Unregister it
	UnregisterStrategy(strategyName)

	// Verify it's gone
	mu.RLock()
	_, exists = strategies[strategyName]
	mu.RUnlock()

	if exists {
		t.Error("Strategy was not unregistered")
	}

	// Verify Close was called
	if !fakeStrategy.IsClosed() {
		t.Error("Strategy.Close() was not called during unregistration")
	}
}

// TestUnregisterStrategy_NonExistent verifies safe handling of unregistering non-existent strategy.
//
// Scenario:
//   - Attempt to unregister a strategy that doesn't exist
//
// Expected Outcome:
//   - No panic or error
//   - Function returns gracefully
func TestUnregisterStrategy_NonExistent(t *testing.T) {
	defer unregisterAll()

	// Should not panic
	UnregisterStrategy("does-not-exist")
}

// TestSQLDrivers_Sorted verifies that driver list is sorted.
//
// Scenario:
//   - Register multiple drivers in random order
//
// Expected Outcome:
//   - SQLDrivers() returns sorted list
func TestSQLDrivers_Sorted(t *testing.T) {
	defer unregisterAll()

	// Register in non-alphabetical order
	RegisterSQLDriver("zebra", testkit.NewFakeDriver())
	RegisterSQLDriver("alpha", testkit.NewFakeDriver())
	RegisterSQLDriver("mike", testkit.NewFakeDriver())

	drivers := SQLDrivers()

	expected := []string{"alpha", "mike", "zebra"}
	if !reflect.DeepEqual(drivers, expected) {
		t.Errorf("SQLDrivers() not sorted: got %v, want %v", drivers, expected)
	}
}

// TestStrategies_Sorted verifies that strategy list is sorted.
//
// Scenario:
//   - Register multiple strategies in random order
//
// Expected Outcome:
//   - Strategies() returns sorted list
func TestStrategies_Sorted(t *testing.T) {
	defer unregisterAll()

	// Register in non-alphabetical order
	RegisterStrategy("zebra", testkit.NewFakeStrategy())
	RegisterStrategy("alpha", testkit.NewFakeStrategy())
	RegisterStrategy("mike", testkit.NewFakeStrategy())

	strats := Strategies()

	expected := []string{"alpha", "mike", "zebra"}
	if !reflect.DeepEqual(strats, expected) {
		t.Errorf("Strategies() not sorted: got %v, want %v", strats, expected)
	}
}

// mockDriver is a minimal driver implementation for testing
type mockDriver struct{}

func (mockDriver) Open(name string) (driver.Conn, error) {
	return nil, driver.ErrSkip
}
