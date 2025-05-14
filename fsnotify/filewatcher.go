package fsnotify

import (
	"context"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	rfsnotify "github.com/fsnotify/fsnotify"
	"github.com/infobloxopen/hotload"
	"github.com/infobloxopen/hotload/internal"
	hlogger "github.com/infobloxopen/hotload/logger"
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
	hlogger.DebugKV("resync", "method", "fsnotify.Strategy.resync", "path", pth)
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
	llog := hlogger.WithKV("method", "fsnotify.Strategy.runLoop")
	failedPaths := make(map[string]struct{})
	for {
		select {
		case ev, ok := <-s.watcher.GetEvents():
			if !ok {
				llog.InfoKV("Events chan closed, terminating")
				return
			}

			ellog := llog.WithKV("event", ev.String())
			ellog.InfoKV("got event")
			if !ev.Has(rfsnotify.Write) && !ev.Has(rfsnotify.Remove) {
				continue
			}

			val, err := s.resync(s.watcher, ev.Name)
			if err != nil {
				ellog.ErrorKV(err, "resync failed", "eventName", ev.Name)
				failedPaths[ev.Name] = struct{}{}
				break
			}

			s.setVal(ev.Name, val)

		case err, ok := <-s.watcher.GetErrors():
			if !ok {
				llog.InfoKV("Errors chan closed, terminating")
				return
			}
			llog.WarnKV("got error from Errors chan", "error", err)

		case <-time.After(resyncPeriod):
			llog.InfoKV("resyncPeriod timedout", "resyncPeriod", resyncPeriod.String())
			var fixedPaths []string
			for pth := range failedPaths {
				val, err := s.resync(s.watcher, pth)
				if err != nil {
					llog.ErrorKV(err, "resync failed", "path", pth)
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
	llog := hlogger.WithKV("method", "fsnotify.Strategy.setVal", "path", pth)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.paths[pth]; !ok {
		llog.WarnKV("path not in map")
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
	pllog := hlogger.WithKV("method", "fsnotify.Strategy.Watch", "path", pth)
	pathW, found := s.paths[pth]
	if found {
		pllog.DebugKV("path already being watched")
	} else {
		pllog.DebugKV("new path to be watched")
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

	qllog := pllog.WithKV("query", pathQry)
	qryW, found := pathW.queries[pathQry]
	if found {
		qllog.DebugKV("query already being watched")
	} else {
		qllog.DebugKV("new query to be watched")
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
			qllog := hlogger.WithKV("method", "fsnotify.Strategy.CloseWatch", "path", pth, "query", pathQry)
			qllog.DebugKV("sent pending close operation")
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
		pllog := hlogger.WithKV("method", "fsnotify.Strategy.processWatchClosure", "path", pendOp.watchPath)
		qllog := pllog.WithKV("query", pendOp.pathQuery)
		qryW, ok := pathW.queries[pendOp.pathQuery]
		if ok {
			delete(pathW.queries, pendOp.pathQuery)
			qryW.closeUpdateChan()
			qllog.DebugKV("closed update channel")
		}
		if len(pathW.queries) <= 0 {
			err = s.watcher.Remove(pendOp.watchPath)
			if err == nil {
				pllog.DebugKV("removed path from being watched")
			} else {
				pllog.ErrorKV(err, "failed to remove path from watcher")
			}
			delete(s.paths, pendOp.watchPath)
			pllog.DebugKV("strategy removed path")
		}
	}
	return err
}

// Close implements the hotload.Strategy interface.
// Closes this strategy by closing the internal watcher
// and closing all the update channels.
func (s *Strategy) Close() {
	llog := hlogger.WithKV("method", "fsnotify.Strategy.Close")
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.watcher != nil {
		s.watcher.Close()
		llog.DebugKV("closed internal watcher")
		s.watcher = nil
	}
	for _, pathW := range s.paths {
		for _, qryW := range pathW.queries {
			qryW.closeUpdateChan()
			llog.WithKV("path", qryW.parentPathW.watchPath, "query", qryW.pathQuery).DebugKV("closed update channel")
		}
		pathW.queries = nil
	}
	s.paths = nil
}

func (qw *queryWatch) sendUpdate(val, redactDsn string) {
	if qw.updateChan == nil {
		return
	}

	qllog := hlogger.WithKV("method", "fsnotify.queryWatch.sendUpdate",
		"path", qw.parentPathW.watchPath, "query", qw.pathQuery, "redactDsn", redactDsn)

	defer func() {
		// Recover/ignore from "panic: send on closed channel"
		panicObj := recover()
		if panicObj != nil {
			qllog.DebugKV("panic recovery", "panicObj", panicObj)
		}
	}()

	qllog.DebugKV("block-sending update")
	qw.updateChan <- val
	qllog.DebugKV("successfully sent update")
}

func (qw *queryWatch) closeUpdateChan() {
	close(qw.updateChan)
	qw.updateChan = nil
}

func (qw *queryWatch) opLoop() {
	qllog := hlogger.WithKV("method", "fsnotify.queryWatch.opLoop",
		"path", qw.parentPathW.watchPath, "query", qw.pathQuery)
	for {
		select {
		case pendOp, ok := <-qw.operChan:
			if !ok {
				qllog.DebugKV("operChan closed, terminating")
				return
			}
			ollog := qllog.WithKV("pendingOperation", pendOp.operation)
			switch pendOp.operation {
			case "close":
				ollog.DebugKV("got close op", "pendingPath", pendOp.watchPath, "pendingQuery", pendOp.pathQuery)
				qw.parentPathW.parentStrat.processWatchClosure(pendOp)
			case "send":
				ollog.DebugKV("got send op", "redactDsn", pendOp.redactDsn)
				qw.sendUpdate(pendOp.dsn, pendOp.redactDsn)
			default:
				ollog.DebugKV("ignore invalid pendingOperation")
			}
		}
	}
}

/*
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
*/
