package fsnotify

import (
	"context"
	"io/ioutil"
	"log"
	"net/url"
	"sync"
	"time"

	"github.com/infobloxopen/hotload"
)

func init() {
	hotload.RegisterStrategy("fsnotify", NewStrategy())
}


var resyncPeriod = time.Second * 2

// NewStrategy implements a hotload strategy that monitors config changes
// in a file using fsnotify.
func NewStrategy() *Strategy {
	return &Strategy{
		paths: make(map[string]*pathWatch),
	}
}

// Strategy implements the hotload Strategy inferface by using
// fsnotify under the covers.
type Strategy struct {
	mu      sync.RWMutex
	paths   map[string]*pathWatch
	watcher watcher
}

type pathWatch struct {
	path   string
	values chan string
	value  string
}

func resync(w watcher, pth string) (string, error) {
	w.Remove(pth)
	bs, err := ioutil.ReadFile(pth)
	if err != nil {
		return "", err
	}
	return string(bs), w.Add(pth)
}

func (s *Strategy) run() {
	failedPaths := make(map[string]struct{})
	for {
		select {
		case e := <-s.watcher.GetEvents():
			val, err := resync(s.watcher, e.Name)
			if err != nil {
				failedPaths[e.Name] = struct{}{}
				break
			}
			s.setVal(e.Name, val)
		case e := <-s.watcher.GetErrors():
			log.Printf("got error: %s", e)
			break
		case <-time.After(resyncPeriod):
			var fixedPaths []string
			for pth := range failedPaths {
				val, err := resync(s.watcher, pth)
				if err == nil {
					fixedPaths = append(fixedPaths, pth)
					s.setVal(pth, val)
				}
			}
			for _, pth := range fixedPaths {
				delete(failedPaths, pth)
			}
		}
	}
}

func (s *Strategy) setVal(pth string, val string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paths[pth].value = val
	go func() {
		s.paths[pth].values <- val
	}()
}

// Watch implements the hotload.Strategy interface.
func (s *Strategy) Watch(ctx context.Context, pth string, options url.Values) (value string, values <-chan string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// if this is the first time this strategy is called, initialize ourselves
	if s.watcher == nil {
		watcher, err := notifyConstructor()
		if err != nil {
			return "", nil, err
		}
		s.watcher = watcher
		go s.run()
	}
	notifier, found := s.paths[pth]
	if !found {
		if err := s.watcher.Add(pth); err != nil {
			return "", nil, err
		}
		bs, err := ioutil.ReadFile(pth)
		if err != nil {
			s.watcher.Remove(pth)
			return "", nil, err
		}
		notifier = &pathWatch{
			path:   pth,
			value:  string(bs),
			values: make(chan string),
		}
		s.paths[pth] = notifier
	}
	return notifier.value, notifier.values, nil
}
