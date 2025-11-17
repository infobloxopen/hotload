// Package testkit provides test fakes and utilities for unit testing hotload components.
//
// The testkit includes:
//   - FakeStrategy: A deterministic strategy implementation for testing without real file watching
//   - FakeDriver: A configurable driver that can simulate various optional interface combinations
//   - FakeClock: A controllable time source for testing time-dependent behavior without delays
//
// These fakes enable fast, hermetic unit tests of hotload's drain/kill logic, capability detection,
// and strategy interactions.
package testkit
