//go:build unit_tests
// +build unit_tests

package fsnotify

// SetWatcher allows setting a custom watcher for testing purposes.
// This function is only available when building with the unit_tests build tag.
func (s *Strategy) SetWatcher(w watcher) {
	s.watcher = w
}
