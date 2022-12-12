package hotload

import (
	"context"
	"fmt"
	"net/url"
	"sync"

	"github.com/sirupsen/logrus"
)

type ManagableDriver[T any] interface {
	Open(name string) (ManagableConn[T], error)
}

type ManagableConn[T any] interface {
	Reset(bool)
	Close() error
}

// hdriver is the hotload driver.
type HotDriver[T any] struct {
	ctx      context.Context
	cgroup   map[string]*cGroup[T]
	mu       sync.Mutex
	registry map[string]ManagableDriver[T]
}

func NewHotDriver[T any]() *HotDriver[T] {
	return &HotDriver[T]{}
}

func (h *HotDriver[T]) Register(name string, driver ManagableDriver[T]) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if driver == nil {
		return fmt.Errorf("driver is nil")
	}
	if _, dup := h.registry[name]; dup {
		return fmt.Errorf("driver %s already registered", name)
	}
	h.registry[name] = driver
	return nil
}

func (h *HotDriver[T]) Open(name string) (ManagableConn[T], error) {
	uri, err := url.Parse(name)
	if err != nil {
		return nil, err
	}

	// look up in the chan group
	h.mu.Lock()
	defer h.mu.Unlock()
	cgroup, ok := h.cgroup[name]
	if !ok {
		strategy, ok := strategies[uri.Scheme]
		if !ok {
			return nil, ErrUnsupportedStrategy
		}

		driver, ok := h.registry[uri.Host]
		if !ok {
			return nil, ErrUnknownDriver
		}
		queryParams := uri.Query()
		value, values, err := strategy.Watch(h.ctx, uri.Path, queryParams)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithCancel(h.ctx)
		cgroup = &cGroup[T]{
			value:     value,
			values:    values,
			parentCtx: h.ctx,
			ctx:       ctx,
			cancel:    cancel,
			driver:    driver,
			conns:     make([]ManagableConn[T], 0),
		}
		cgroup.parseValues(queryParams)
		h.cgroup[name] = cgroup
		go cgroup.run()
	}
	return cgroup.Open()
}

// cGroup represents a hotload location that is being monitored
type cGroup[T any] struct {
	value     string
	values    <-chan string
	parentCtx context.Context
	ctx       context.Context
	cancel    context.CancelFunc
	driver    ManagableDriver[T]
	mu        sync.RWMutex
	forceKill bool
	conns     []ManagableConn[T]
}

// monitor the location for changes
func (cg *cGroup[T]) run() {
	for {
		select {
		case <-cg.parentCtx.Done():
			cg.cancel()
			logger.Debug("cancelling chanGroup context")
			return
		case v := <-cg.values:
			if v == cg.value {
				// next update is the same, just ignore it
				continue
			}
			cg.valueChanged(v)
			logger.Debug("connection information changed")
		}
	}
}

func (cg *cGroup[T]) valueChanged(v string) {
	cg.mu.Lock()
	defer cg.mu.Unlock()
	cg.cancel()
	cg.ctx, cg.cancel = context.WithCancel(cg.parentCtx)
	cg.resetConnections()

	cg.value = v
}

func (cg *cGroup[T]) resetConnections() {
	for _, c := range cg.conns {
		c.Reset(true)

		if cg.forceKill {
			// ignore errors from close
			c.Close()
		}
	}

	cg.conns = make([]ManagableConn[T], 0)
}

func (cg *cGroup[T]) Open() (ManagableConn[T], error) {
	cg.mu.Lock()
	defer cg.mu.Unlock()
	conn, err := cg.driver.Open(cg.value)
	if err != nil {
		return conn, err
	}

	manConn := newMConn(cg.ctx, conn)
	cg.conns = append(cg.conns, manConn)

	return manConn, nil
}

func (cg *cGroup[T]) parseValues(vs url.Values) {
	cg.mu.Lock()
	defer cg.mu.Unlock()
	logger.WithFields(logrus.Fields{"urlValues": vs}).Debug("parsing values")
	if v, ok := vs[forceKill]; ok {
		firstValue := v[0]
		cg.forceKill = firstValue == "true"
		logger.Debug("forceKill set to true")
	}
}
