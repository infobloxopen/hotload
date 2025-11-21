package hotload

import (
	"context"
	"net/url"
)

// Strategy is the plugin interface for hotload.
type Strategy interface {
	// The updates channel is closed when the context is cancelled.
	Watch(ctx context.Context, path string) (initial string, updates <-chan string, err error)
}

// StrategyWithOptions extends Strategy to support query parameters.
// Implementations should implement this interface to receive options from the connection string.
type StrategyWithOptions interface {
	Strategy
	// WatchWithOptions is like Watch but receives query parameters from the connection string.
	WatchWithOptions(ctx context.Context, path string, options url.Values) (initial string, updates <-chan string, err error)
}
