package fsnotify

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	rfsnotify "github.com/fsnotify/fsnotify"
	hotload "github.com/infobloxopen/hotload/v3"
	"github.com/infobloxopen/hotload/v3/internal"
	"github.com/infobloxopen/hotload/v3/logger"
)

func init() {
	hotload.RegisterStrategy("fsnotify", NewStrategy())
}

const defaultResyncPeriod = time.Second * 2

// NewStrategy implements a hotload strategy that monitors config changes
// in a file using fsnotify.
func NewStrategy() *Strategy {
	return &Strategy{
		paths:        make(map[string]*pathWatch),
		resyncPeriod: defaultResyncPeriod,
	}
}

// Strategy implements the hotload Strategy interface by using
// fsnotify under the covers.
type Strategy struct {
	mu           sync.RWMutex
	paths        map[string]*pathWatch
	watcher      watcher
	resyncPeriod time.Duration
}

// update is one value change queued for delivery to a watcher.
type update struct {
	dsn       string
	redactDsn string
}

// queryWatch fans one path's updates out to one subscriber (the hotload
// group watching pth+pathQry). Its opLoop goroutine is the only writer of
// updateChan: it forwards queued updates and closes the channel when
// operChan closes, so channel operations never race. done is closed (under
// the strategy lock) when the watch is closed, unblocking an opLoop stuck
// sending to a subscriber that stopped receiving.
type queryWatch struct {
	parentPathW *pathWatch
	pathQuery   string
	updateChan  chan string
	operChan    chan update
	done        chan struct{}
}

type pathWatch struct {
	parentStrat *Strategy
	watchPath   string
	value       string
	queries     map[string]*queryWatch
}

func (s *Strategy) readConfigFile(path string) (v []byte, err error) {
	v, err = os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("could not read %v: %w", path, err)
	}
	v = []byte(strings.TrimSpace(string(v)))
	return
}

func (s *Strategy) resync(w watcher, pth string) (string, error) {
	s.logf("fsnotify", "resync path: '%s'", pth)
	err := w.Remove(pth)
	if err != nil && !errors.Is(err, rfsnotify.ErrNonExistentWatch) {
		return "", err
	}
	bs, err := s.readConfigFile(pth)
	if err != nil {
		return "", err
	}
	return string(bs), w.Add(pth)
}

func (s *Strategy) runLoop(w watcher) {
	failedPaths := make(map[string]struct{})
	for {
		select {
		case ev, ok := <-w.GetEvents():
			if !ok {
				s.logf("fsnotify.runLoop", "Events chan closed, terminating")
				return
			}

			s.logf("fsnotify.runLoop", "got event: %s", ev.String())
			if !ev.Has(rfsnotify.Write) && !ev.Has(rfsnotify.Remove) {
				continue
			}

			for _, pth := range s.affectedPaths(ev.Name) {
				val, err := s.resync(w, pth)
				if err != nil {
					s.errlogf("fsnotify.runLoop", "resync(%s) err: %v", pth, err)
					failedPaths[pth] = struct{}{}
					continue
				}
				delete(failedPaths, pth)
				s.setVal(pth, val)
			}

		case err, ok := <-w.GetErrors():
			if !ok {
				s.logf("fsnotify.runLoop", "Errors chan closed, terminating")
				return
			}
			s.logf("fsnotify.runLoop", "got error: %s", err.Error())

		case <-time.After(s.resyncPeriod):
			var fixedPaths []string
			for pth := range failedPaths {
				val, err := s.resync(w, pth)
				if err != nil {
					s.errlogf("fsnotify.runLoop", "resync(%s) err: %v", pth, err)
				} else {
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

// affectedPaths maps a notification name to the watched paths that need a
// resync. The name usually is a watched path, but when the watched path is
// a symlink (the Kubernetes ConfigMap pattern) some platforms report events
// under the resolved target — kqueue even adds a /private prefix on macOS —
// so an unknown name conservatively resyncs every watched path. Events are
// rare and the watched files are small, so the cost is negligible.
func (s *Strategy) affectedPaths(name string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.paths[name]; ok {
		return []string{name}
	}
	out := make([]string, 0, len(s.paths))
	for pth := range s.paths {
		out = append(out, pth)
	}
	return out
}

func (s *Strategy) setVal(pth string, val string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pathW, ok := s.paths[pth]
	if !ok {
		s.logf("fsnotify.setVal", "ignoring path not in map: '%s'", pth)
		return
	}
	pathW.value = val
	redactDsn := internal.RedactUrl(val)
	for _, qryW := range pathW.queries {
		qryW.enqueue(update{dsn: val, redactDsn: redactDsn})
	}
}

// enqueue queues an update for delivery without ever blocking: only the
// latest value matters to a hotload group, so when a subscriber's queue is
// full (it stopped receiving, or is slow), the oldest queued value is
// dropped to make room. A blocking send here would wedge the whole strategy
// behind one slow subscriber, since enqueue runs under the strategy lock.
func (qw *queryWatch) enqueue(up update) {
	for {
		select {
		case qw.operChan <- up:
			return
		case <-qw.done:
			return
		default:
		}
		// Queue full: drop the oldest queued update and retry. opLoop may
		// have consumed one concurrently, so the drain is non-blocking too.
		select {
		case <-qw.operChan:
			qw.logf("fsnotify.enqueue", "subscriber slow; dropped oldest queued update")
		default:
		}
	}
}

// Watch implements the hotload.Strategy interface.
func (s *Strategy) Watch(ctx context.Context, pth string, pathQry string) (value string, values <-chan string, err error) {
	pth = path.Clean(pth)
	pathQry = strings.TrimSpace(pathQry)
	s.mu.Lock()
	defer s.mu.Unlock()
	// Re-initialize after Close (which nils the maps out).
	if s.paths == nil {
		s.paths = make(map[string]*pathWatch)
	}
	// if this is the first time this strategy is called, initialize ourselves
	if s.watcher == nil {
		watcher, err := notifyConstructor()
		if err != nil {
			return "", nil, err
		}
		s.watcher = watcher
		go s.runLoop(watcher)
	}
	pathW, found := s.paths[pth]
	if found {
		pathW.logf("fsnotify.Watch", "path already being watched")
	} else {
		s.logf("fsnotify.Watch", "new path to be watched: '%s'", pth)
		if err := s.watcher.Add(pth); err != nil {
			return "", nil, err
		}
		bs, err := s.readConfigFile(pth)
		if err != nil {
			s.watcher.Remove(pth)
			return "", nil, err
		}
		pathW = &pathWatch{
			parentStrat: s,
			watchPath:   pth,
			value:       string(bs),
			queries:     make(map[string]*queryWatch),
		}
		s.paths[pth] = pathW
	}

	qryW, found := pathW.queries[pathQry]
	if found {
		qryW.logf("fsnotify.Watch", "query already being watched")
	} else {
		pathW.logf("fsnotify.Watch", "new query to be watched: '%s'", pathQry)
		qryW = &queryWatch{
			parentPathW: pathW,
			pathQuery:   pathQry,
			updateChan:  make(chan string),
			operChan:    make(chan update, 30),
			done:        make(chan struct{}),
		}
		pathW.queries[pathQry] = qryW
		go qryW.opLoop()
	}

	return pathW.value, qryW.updateChan, nil
}

// CloseWatch implements the hotload.Strategy interface.
// Closes the specified watch; its update channel is closed (asynchronously,
// by the watch's delivery goroutine) and, if this was the last watch on the
// path, the path is removed from the file watcher.
func (s *Strategy) CloseWatch(pth string, pathQry string) error {
	pth = path.Clean(pth)
	pathQry = strings.TrimSpace(pathQry)
	s.mu.Lock()
	defer s.mu.Unlock()
	pathW, found := s.paths[pth]
	if !found {
		return nil
	}
	qryW, found := pathW.queries[pathQry]
	if !found {
		return nil
	}

	delete(pathW.queries, pathQry)
	qryW.shutdown()
	qryW.logf("fsnotify.CloseWatch", "closed watch")

	if len(pathW.queries) == 0 {
		delete(s.paths, pth)
		if err := s.watcher.Remove(pth); err != nil {
			s.errlogf("fsnotify.CloseWatch", "failed to remove '%s' from watcher, err=%v", pth, err)
			return err
		}
		s.logf("fsnotify.CloseWatch", "removed path from being watched '%s'", pth)
	}
	return nil
}

// Close implements the hotload.Strategy interface.
// Closes this strategy by closing the internal watcher
// and closing all the update channels.
func (s *Strategy) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.watcher != nil {
		s.watcher.Close()
		s.logf("fsnotify.Close", "closed internal watcher")
		s.watcher = nil
	}
	for _, pathW := range s.paths {
		for _, qryW := range pathW.queries {
			qryW.shutdown()
			qryW.logf("fsnotify.Close", "closed watch")
		}
		pathW.queries = nil
	}
	s.paths = nil
}

// shutdown stops the watch's delivery goroutine. Callers must hold the
// strategy lock (it is the lock that serializes shutdown with setVal's
// sends, making the channel close safe).
func (qw *queryWatch) shutdown() {
	close(qw.done)
	close(qw.operChan)
}

// opLoop forwards queued updates to the subscriber. It is the only
// goroutine that sends on or closes updateChan. The done channel unblocks
// the forwarding send if the subscriber stopped receiving (e.g. the hotload
// group was torn down before the watch was closed).
func (qw *queryWatch) opLoop() {
	for op := range qw.operChan {
		qw.logf("fsnotify.opLoop", "sending redactDsn='%s'", op.redactDsn)
		select {
		case qw.updateChan <- op.dsn:
			qw.logf("fsnotify.opLoop", "successfully sent redactDsn='%s'", op.redactDsn)
		case <-qw.done:
			qw.logf("fsnotify.opLoop", "watch closed while sending redactDsn='%s'", op.redactDsn)
		}
	}
	close(qw.updateChan)
	qw.logf("fsnotify.opLoop", "operChan closed, terminating")
}

func (s *Strategy) logf(prefix, format string, args ...any) {
	logPrefix := fmt.Sprintf("%s:", prefix)
	logger.Logf(logPrefix, format, args...)
}

func (pw *pathWatch) logf(prefix, format string, args ...any) {
	logPrefix := fmt.Sprintf("%s[%s]:", prefix, pw.watchPath)
	logger.Logf(logPrefix, format, args...)
}

func (qw *queryWatch) logf(prefix, format string, args ...any) {
	logPrefix := fmt.Sprintf("%s[%s?%s]:", prefix, qw.parentPathW.watchPath, qw.pathQuery)
	logger.Logf(logPrefix, format, args...)
}

func (s *Strategy) errlogf(prefix, format string, args ...any) {
	logPrefix := fmt.Sprintf("%s:", prefix)
	logger.ErrLogf(logPrefix, format, args...)
}
