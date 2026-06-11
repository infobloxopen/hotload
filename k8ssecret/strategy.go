// Package k8ssecret implements a hotload strategy that watches Kubernetes
// Secrets for database connection string changes.
//
// In multi-namespace deployments a service sometimes needs credentials from
// a Secret in another team's namespace. Kubernetes does not allow mounting
// Secrets across namespaces, so the volume-based fsnotify strategy cannot
// see them; this strategy watches the Secret through the Kubernetes API
// instead.
//
// This package is a separate Go module
// (github.com/infobloxopen/hotload/k8ssecret) so client-go and its
// transitive dependencies stay out of the hotload core.
//
// # DSN format
//
//	k8ssecret://<driver>/<secret-name>?namespace=<ns>&dsn=<key>
//
// Parameters:
//   - secret-name: the Kubernetes Secret name (the path component)
//   - namespace: the namespace containing the Secret (default: the pod's
//     namespace from the service account mount, else "default")
//   - dsn: the data key within the Secret holding the connection string
//     (default: "dsn.txt")
//
// The hotload parameters (forceKill, killWindow) may appear in the same
// query string.
//
// # Usage
//
// Import the package for its side effect of registering the strategy:
//
//	import _ "github.com/infobloxopen/hotload/k8ssecret"
//
//	db, err := sql.Open("hotload", "k8ssecret://pgx/myapp-db?namespace=prod&dsn=dsn.txt")
//
// The pod's service account needs get and watch permissions on the Secret.
package k8ssecret

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	hotload "github.com/infobloxopen/hotload/v3"
	"github.com/infobloxopen/hotload/v3/logger"
)

func init() {
	hotload.RegisterStrategy("k8ssecret", NewStrategy())
}

const (
	defaultKey     = "dsn.txt"
	defaultBackoff = 2 * time.Second
)

// ClientsetFunc constructs the Kubernetes clientset used by strategies that
// were not given one explicitly (including the instance registered by this
// package's init). It is consulted lazily on the first Watch; replace it
// before opening connections to use out-of-cluster config or fakes.
var ClientsetFunc = defaultClientset

func defaultClientset() (kubernetes.Interface, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("k8ssecret: in-cluster config: %w", err)
	}
	return kubernetes.NewForConfig(cfg)
}

// Strategy implements hotload.Strategy by watching Kubernetes Secrets.
type Strategy struct {
	mu        sync.Mutex
	clientset kubernetes.Interface
	watches   map[watchKey]*secretWatch
	backoff   time.Duration
}

// watchKey identifies one watched value. The data key is part of the
// identity: two DSNs reading different keys of the same Secret carry
// different values and must not share state.
type watchKey struct {
	namespace string
	name      string
	key       string
}

// secretWatch fans one watched Secret value out to its subscribers (one per
// distinct pathQry). Subscriber channels have capacity 1 and are written
// with drop-oldest semantics, so a subscriber always converges on the
// latest value and a slow subscriber can never block delivery. All channel
// sends and closes happen under Strategy.mu, so they cannot race.
type secretWatch struct {
	cancel  context.CancelFunc
	value   string
	queries map[string]chan string
}

// NewStrategy creates a strategy that builds its clientset lazily from
// ClientsetFunc on first use.
func NewStrategy() *Strategy {
	return &Strategy{
		watches: make(map[watchKey]*secretWatch),
		backoff: defaultBackoff,
	}
}

// NewStrategyWithClientset creates a strategy using the given clientset;
// useful for tests (client-go's fake clientset) and out-of-cluster use.
func NewStrategyWithClientset(cs kubernetes.Interface) *Strategy {
	s := NewStrategy()
	s.clientset = cs
	return s
}

// secretName extracts the Secret name from the path component the hotload
// core passes to Watch. For "k8ssecret://pgx/myapp-db" that component is
// "/myapp-db" — with a leading slash that is not part of the name.
func secretName(pth string) string {
	return strings.TrimPrefix(path.Clean(strings.TrimSpace(pth)), "/")
}

// parseParams extracts the namespace and data key from the encoded query
// parameters of the hotload DSN.
func parseParams(pathQry string) (namespace, key string, err error) {
	params, err := url.ParseQuery(strings.TrimSpace(pathQry))
	if err != nil {
		return "", "", fmt.Errorf("k8ssecret: parse query %q: %w", pathQry, err)
	}
	namespace = params.Get("namespace")
	if namespace == "" {
		namespace = podNamespace()
	}
	key = params.Get("dsn")
	if key == "" {
		key = defaultKey
	}
	return namespace, key, nil
}

// Watch implements hotload.Strategy. pth is the Secret name; pathQry
// carries the namespace and dsn parameters (see the package documentation).
func (s *Strategy) Watch(ctx context.Context, pth string, pathQry string) (string, <-chan string, error) {
	name := secretName(pth)
	pathQry = strings.TrimSpace(pathQry)
	namespace, key, err := parseParams(pathQry)
	if err != nil {
		return "", nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.clientset == nil {
		cs, err := ClientsetFunc()
		if err != nil {
			return "", nil, err
		}
		s.clientset = cs
	}
	if s.watches == nil {
		// Re-initialize after Close.
		s.watches = make(map[watchKey]*secretWatch)
	}

	wk := watchKey{namespace: namespace, name: name, key: key}
	sw, exists := s.watches[wk]
	if !exists {
		secret, err := s.clientset.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", nil, fmt.Errorf("k8ssecret: get secret %s/%s: %w", namespace, name, err)
		}
		val, ok := secret.Data[key]
		if !ok {
			return "", nil, fmt.Errorf("k8ssecret: secret %s/%s has no key %q", namespace, name, key)
		}

		watchCtx, cancel := context.WithCancel(context.Background())
		sw = &secretWatch{
			cancel:  cancel,
			value:   string(val),
			queries: make(map[string]chan string),
		}
		s.watches[wk] = sw
		go s.runWatch(watchCtx, wk)
	}

	ch, exists := sw.queries[pathQry]
	if !exists {
		ch = make(chan string, 1)
		sw.queries[pathQry] = ch
	}
	return sw.value, ch, nil
}

// CloseWatch implements hotload.Strategy. When the last subscriber of a
// Secret value is closed, the API watch is stopped.
func (s *Strategy) CloseWatch(pth string, pathQry string) error {
	name := secretName(pth)
	pathQry = strings.TrimSpace(pathQry)
	namespace, key, err := parseParams(pathQry)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	wk := watchKey{namespace: namespace, name: name, key: key}
	sw, ok := s.watches[wk]
	if !ok {
		return nil
	}
	if ch, ok := sw.queries[pathQry]; ok {
		close(ch)
		delete(sw.queries, pathQry)
	}
	if len(sw.queries) == 0 {
		sw.cancel()
		delete(s.watches, wk)
	}
	return nil
}

// Close implements hotload.Strategy: stops every watch and closes every
// update channel.
func (s *Strategy) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for wk, sw := range s.watches {
		sw.cancel()
		for q, ch := range sw.queries {
			close(ch)
			delete(sw.queries, q)
		}
		delete(s.watches, wk)
	}
	s.watches = nil
}

// runWatch maintains the API watch for one Secret value until its context
// is canceled. Every (re)connect first re-reads the Secret and delivers any
// value missed while disconnected, then watches from that read's resource
// version — so no modification is lost between the read and the watch, or
// while a dropped watch was reconnecting.
func (s *Strategy) runWatch(ctx context.Context, wk watchKey) {
	for {
		rv, err := s.catchUp(ctx, wk)
		if err == nil {
			err = s.consumeWatch(ctx, wk, rv)
		}
		if ctx.Err() != nil {
			return
		}
		logger.ErrLogf("k8ssecret.runWatch:", "watch %s/%s interrupted: %v, reconnecting in %s",
			wk.namespace, wk.name, err, s.backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.backoff):
		}
	}
}

// catchUp reads the Secret's current value, delivers it if it changed, and
// returns the resource version to start the watch from.
func (s *Strategy) catchUp(ctx context.Context, wk watchKey) (string, error) {
	secret, err := s.clientset.CoreV1().Secrets(wk.namespace).Get(ctx, wk.name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get: %w", err)
	}
	if val, ok := secret.Data[wk.key]; ok {
		s.deliver(wk, string(val))
	}
	return secret.ResourceVersion, nil
}

// consumeWatch processes watch events until the watch drops or the context
// is canceled.
func (s *Strategy) consumeWatch(ctx context.Context, wk watchKey, rv string) error {
	watcher, err := s.clientset.CoreV1().Secrets(wk.namespace).Watch(ctx, metav1.ListOptions{
		FieldSelector:   "metadata.name=" + wk.name,
		ResourceVersion: rv,
	})
	if err != nil {
		return fmt.Errorf("start watch: %w", err)
	}
	defer watcher.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-watcher.ResultChan():
			if !ok {
				return fmt.Errorf("watch channel closed")
			}
			switch event.Type {
			case watch.Added, watch.Modified:
				// Added matters: a Secret deleted and recreated comes back
				// as Added, and so does the first event after a reconnect.
			case watch.Error:
				return fmt.Errorf("watch error event: %v", event.Object)
			default:
				// Deleted: keep serving the last known value, exactly like
				// the fsnotify strategy when the watched file disappears;
				// the recreate arrives as Added.
				continue
			}
			secret, ok := event.Object.(*corev1.Secret)
			if !ok || secret.Name != wk.name {
				// The fake clientset (and old API servers) can ignore field
				// selectors, so filter by name here too.
				continue
			}
			if val, ok := secret.Data[wk.key]; ok {
				s.deliver(wk, string(val))
			}
		}
	}
}

// deliver pushes a changed value to every subscriber of the watch.
// Channels have capacity 1; when full, the stale queued value is dropped so
// the subscriber always converges on the latest one (dropping the new value
// instead would leave a slow subscriber permanently stale).
func (s *Strategy) deliver(wk watchKey, val string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sw, ok := s.watches[wk]
	if !ok || sw.value == val {
		return
	}
	sw.value = val
	for _, ch := range sw.queries {
		select {
		case ch <- val:
			continue
		default:
		}
		select {
		case <-ch: // drop the stale queued value
		default:
		}
		select {
		case ch <- val:
		default:
		}
	}
}

// podNamespace returns the namespace of the current pod from the service
// account mount, or "default" when not running in-cluster.
func podNamespace() string {
	if ns, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace"); err == nil {
		return strings.TrimSpace(string(ns))
	}
	return "default"
}
