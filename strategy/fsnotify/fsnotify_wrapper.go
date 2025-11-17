package fsnotify

import (
	"github.com/fsnotify/fsnotify"
	rfsnotify "github.com/fsnotify/fsnotify"
)

type notifyConstructorType func() (watcher, error)

var (
	defaultNotifyContructor notifyConstructorType = func() (watcher, error) {
		impl, err := rfsnotify.NewWatcher()
		if err != nil {
			return nil, err
		}
		return &myfsnotify{impl: impl}, nil
	}
	notifyConstructor = defaultNotifyContructor
)

// mynotify is a wrapper around fsnotify's Watcher
// so that it can be mocked appropriately in unit tests.
type myfsnotify struct {
	impl *rfsnotify.Watcher
}

type watcher interface {
	Add(string) error
	Remove(string) error
	Close() error
	GetEvents() <-chan rfsnotify.Event
	GetErrors() <-chan error
}

func (n *myfsnotify) Add(name string) error {
	return n.impl.Add(name)
}

func (n *myfsnotify) Remove(name string) error {
	return n.impl.Remove(name)
}

func (n *myfsnotify) GetEvents() <-chan fsnotify.Event {
	return n.impl.Events
}
func (n *myfsnotify) GetErrors() <-chan error {
	return n.impl.Errors
}

func (n *myfsnotify) Close() error {
	return n.impl.Close()
}
