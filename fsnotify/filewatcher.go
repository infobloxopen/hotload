package fsnotify

import (
	"context"
	"fmt"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	rfsnotify "github.com/fsnotify/fsnotify"
	"github.com/infobloxopen/hotload"
	"github.com/infobloxopen/hotload/internal"
	"github.com/infobloxopen/hotload/logger"
	"github.com/infobloxopen/hotload/metrics"
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

type pendingOperation struct {
	operation string
	watchPath string
	pathQuery string
	dsn       string
	redactDsn string
}

type queryWatch struct {
	parentPathW *pathWatch
	pathQuery   string
	updateChan  chan string
	operChan    chan pendingOperation
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
		return nil, errors.Wrapf(err, "could not read %v", path)
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

func (s *Strategy) runLoop() {
	failedPaths := make(map[string]struct{})
	for {
		select {
		case ev, ok := <-s.watcher.GetEvents():
			if !ok {
				s.logf("fsnotify.runLoop", "Events chan closed, terminating")
				return
			}

			s.logf("fsnotify.runLoop", "got event: %s", ev.String())
			if !ev.Has(rfsnotify.Write) && !ev.Has(rfsnotify.Remove) {
				continue
			}

			val, err := s.resync(s.watcher, ev.Name)
			if err != nil {
				s.errlogf("fsnotify.runLoop", "resync(%s) err: %v", ev.Name, err)
				failedPaths[ev.Name] = struct{}{}
				break
			}

			s.setVal(ev.Name, val)

		case err, ok := <-s.watcher.GetErrors():
			if !ok {
				s.logf("fsnotify.runLoop", "Errors chan closed, terminating")
				return
			}
			s.logf("fsnotify.runLoop", "got error: %s", err.Error())

		case <-time.After(resyncPeriod):
			s.logf("fsnotify.runLoop", "resyncPeriod %s timedout", resyncPeriod.String())
			var fixedPaths []string
			for pth := range failedPaths {
				val, err := s.resync(s.watcher, pth)
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

func (s *Strategy) setVal(pth string, val string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.paths[pth]; !ok {
		s.logf("fsnotify.setVal", "ignoring path not in map: '%s'", pth)
		return
	}
	s.paths[pth].value = val
	redactDsn := internal.RedactUrl(val)
	for _, qryW := range s.paths[pth].queries {
		pendOp := pendingOperation{
			operation: "send",
			dsn:       val,
			redactDsn: redactDsn,
		}
		qryW.operChan <- pendOp
	}
}

// Watch implements the hotload.Strategy interface.
func (s *Strategy) Watch(ctx context.Context, pth string, pathQry string) (value string, values <-chan string, err error) {
	pth = path.Clean(pth)
	pathQry = strings.TrimSpace(pathQry)
	s.mu.Lock()
	defer s.mu.Unlock()
	// if this is the first time this strategy is called, initialize ourselves
	if s.watcher == nil {
		watcher, err := notifyConstructor()
		if err != nil {
			return "", nil, err
		}
		s.watcher = watcher
		go s.runLoop()
	}
	pathW, found := s.paths[pth]
	if found {
		pathW.logf("fsnotify.Watch", "path already being watched")
	} else {
		s.logf("fsnotify.Watch", "new path to be watched: '%s'", pth)
		if err := s.watcher.Add(pth); err != nil {
			return "", nil, err
		}
		if err := metrics.AddToDefaultPathChksum(pth); err != nil {
			if err != metrics.ErrDuplicatePath {
				s.errlogf("fsnotify.Watch", "AddToDefaultPathChksum(%s) failed, err=%v", pth, err)
				return "", nil, err
			}
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
			operChan:    make(chan pendingOperation, 30),
		}
		pathW.queries[pathQry] = qryW
		go qryW.opLoop()
	}

	return pathW.value, qryW.updateChan, nil
}

// CloseWatch implements the hotload.Strategy interface.
// Closes the specified watch by removing the path
// from the watcher and closing the path's update channel.
func (s *Strategy) CloseWatch(pth string, pathQry string) error {
	pth = path.Clean(pth)
	pathQry = strings.TrimSpace(pathQry)
	s.mu.Lock()
	defer s.mu.Unlock()
	pathW, found := s.paths[pth]
	if found {
		qryW, ok := pathW.queries[pathQry]
		if ok {
			pendOp := pendingOperation{
				operation: "close",
				watchPath: pth,
				pathQuery: pathQry,
			}
			qryW.operChan <- pendOp
			qryW.logf("fsnotify.CloseWatch", "sent pending close operation")
		}
	}
	return nil
}

func (s *Strategy) processWatchClosure(pendOp pendingOperation) error {
	var err error
	s.mu.Lock()
	defer s.mu.Unlock()
	pathW, found := s.paths[pendOp.watchPath]
	if found {
		qryW, ok := pathW.queries[pendOp.pathQuery]
		if ok {
			delete(pathW.queries, pendOp.pathQuery)
			qryW.closeUpdateChan()
			qryW.logf("fsnotify.processWatchClosure", "closed update channel")
		}
		if len(pathW.queries) <= 0 {
			err = s.watcher.Remove(pendOp.watchPath)
			if err == nil {
				s.logf("fsnotify.processWatchClosure", "removed path from being watched '%s'", pendOp.watchPath)
			} else {
				s.errlogf("fsnotify.processWatchClosure", "failed to remove '%s' from watcher, err=%v", pendOp.watchPath, err)
			}
			delete(s.paths, pendOp.watchPath)
			s.logf("fsnotify.processWatchClosure", "strategy removed path '%s'", pendOp.watchPath)
		}
	}
	return err
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
			qryW.closeUpdateChan()
			qryW.logf("fsnotify.Close", "closed update channel")
		}
		pathW.queries = nil
	}
	s.paths = nil
}

func (qw *queryWatch) sendUpdate(val, redactDsn string) {
	if qw.updateChan == nil {
		return
	}

	defer func() {
		// Recover/ignore from "panic: send on closed channel"
		r := recover()
		if r != nil {
			qw.logf("fsnotify.sendUpdate", "panic recovery '%s'", r)
		}
	}()

	qw.logf("fsnotify.sendUpdate", "block-sending redactDsn='%s'", redactDsn)
	qw.updateChan <- val
	qw.logf("fsnotify.sendUpdate", "successfully sent redactDsn='%s'", redactDsn)
}

func (qw *queryWatch) closeUpdateChan() {
	close(qw.updateChan)
	qw.updateChan = nil
}

func (qw *queryWatch) opLoop() {
	for {
		select {
		case pendOp, ok := <-qw.operChan:
			if !ok {
				qw.logf("fsnotify.opLoop", "operChan closed, terminating")
				return
			}
			switch pendOp.operation {
			case "close":
				qw.logf("fsnotify.opLoop", "pendingOperation '%s', pendingPath=%s, pendingQuery='%s'",
					pendOp.operation, pendOp.watchPath, pendOp.pathQuery)
				qw.parentPathW.parentStrat.processWatchClosure(pendOp)
			case "send":
				qw.logf("fsnotify.opLoop", "pendingOperation '%s', redactDsn='%s'",
					pendOp.operation, pendOp.redactDsn)
				qw.sendUpdate(pendOp.dsn, pendOp.redactDsn)
			default:
				qw.logf("fsnotify.opLoop", "ignore invalid pendingOperation '%s'",
					pendOp.operation)
			}
		}
	}
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

func (pw *pathWatch) errlogf(prefix, format string, args ...any) {
	logPrefix := fmt.Sprintf("%s[%s]:", prefix, pw.watchPath)
	logger.ErrLogf(logPrefix, format, args...)
}

func (qw *queryWatch) errlogf(prefix, format string, args ...any) {
	logPrefix := fmt.Sprintf("%s[%s?%s]:", prefix, qw.parentPathW.watchPath, qw.pathQuery)
	logger.ErrLogf(logPrefix, format, args...)
}
