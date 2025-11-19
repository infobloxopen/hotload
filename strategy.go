package hotload

import (
	"context"
	"net/url"
)

// Strategy is the plugin interface for hotload.
type Strategy interface {
	// The updates channel is closed when the context is cancelled.
	Watch(ctx context.Context, path string, query url.Values) (initial string, updates <-chan string, err error)
}
