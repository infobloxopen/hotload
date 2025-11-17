package hotload

import "context"

// Strategy is the plugin interface for hotload.
type Strategy interface {
	// Watch returns back the contents of the resource as well as a channel
	// for subsequent updates (if the value has changed). If there is an error
	// getting the initial value, an error is returned.
	// The updates channel is closed when the context is cancelled.
	Watch(ctx context.Context, path string, query map[string][]string) (value string, updates <-chan string, err error)

	// Close resets/closes strategy, in particular closes all the update channels.
	Close() error
}
