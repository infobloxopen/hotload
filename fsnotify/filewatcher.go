package fsnotify

import (
	"context"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	rfsnotify "github.com/fsnotify/fsnotify"
	"github.com/infobloxopen/hotload"
	"github.com/infobloxopen/hotload/logger"
	"github.com/pkg/errors"
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

func readConfigFile(path string) (v []byte, err error) {
	v, err = os.ReadFile(path)
	if err != nil {
		return nil, errors.Wrapf(err, "could not read %v", path)
	}
	v = []byte(strings.TrimSpace(string(v)))
	return
}

func resync(w watcher, pth string) (string, error) {
	log := logger.GetLogger()
	log("fsnotify: Path Name-Resync ", pth)
	err := w.Remove(pth)
	if err != nil && !errors.Is(err, rfsnotify.ErrNonExistentWatch) {
		return "", err
	}
	bs, err := readConfigFile(pth)
	if err != nil {
		return "", err
	}
	return string(bs), w.Add(pth)
}

func (s *Strategy) run() {
	log := logger.GetLogger()
	failedPaths := make(map[string]struct{})
	for {
		select {
		case e := <-s.watcher.GetEvents():
			log("fsnotify: Path Name-Run: got event: ", e.String())
			if !e.Has(rfsnotify.Write) && !e.Has(rfsnotify.Remove) {
				continue
			}

			val, err := resync(s.watcher, e.Name)
			if err != nil {
				failedPaths[e.Name] = struct{}{}
				break
			}

			s.setVal(e.Name, val)
		case e := <-s.watcher.GetErrors():
			log("got error: ", e)
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
	log := logger.GetLogger()
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.paths[pth]; !ok {
		log("fsnotify: Path not in map ", pth)
		return
	}
	s.paths[pth].value = val
	values := s.paths[pth].values
	go func() {
		values <- val
	}()
}

// Watch implements the hotload.Strategy interface.
func (s *Strategy) Watch(ctx context.Context, pth string, options url.Values) (value string, values <-chan string, err error) {
	log := logger.GetLogger()
	pth = path.Clean(pth)
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
		log("fsnotify: Path Name-Init ", pth)
		if err := s.watcher.Add(pth); err != nil {
			return "", nil, err
		}
		bs, err := readConfigFile(pth)
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
