// Package k8ssecret implements a hotload strategy that watches Kubernetes
// Secrets for database connection string changes.
//
// This package is a separate Go module (github.com/infobloxopen/hotload/k8ssecret)
// to avoid pulling client-go and its transitive dependencies into the root
// hotload module.
//
// # DSN Format
//
//	k8ssecret://postgres/<secret-name>?namespace=<ns>&dsn=<key>
//
// Parameters:
//   - secret-name: the Kubernetes Secret name (the path component)
//   - namespace: the namespace containing the Secret (default: pod's namespace via
//     in-cluster config, or "default")
//   - dsn: the data key within the Secret that holds the DSN (default: "dsn.txt")
//
// # Usage
//
// Import the package for its side effect of registering the strategy:
//
//	import _ "github.com/infobloxopen/hotload/k8ssecret"
//
//	db, err := sql.Open("hotload", "k8ssecret://pgx/myapp-db?namespace=prod&dsn=dsn.txt")
package k8ssecret

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/infobloxopen/hotload"
	"github.com/infobloxopen/hotload/logger"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func init() {
	hotload.RegisterStrategy("k8ssecret", NewStrategy())
}

// ClientsetFunc constructs a Kubernetes clientset. Replaceable for testing.
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
	watches   map[string]*secretWatch // key: "namespace/secret"
}

// NewStrategy creates a new k8ssecret strategy.
func NewStrategy() *Strategy {
	return &Strategy{
		watches: make(map[string]*secretWatch),
	}
}

type secretWatch struct {
	cancel  context.CancelFunc
	value   string
	queries map[string]*queryWatch
}

type queryWatch struct {
	ch chan string
}

// Watch implements hotload.Strategy. The pth is the Secret name. Query params:
//   - namespace: Kubernetes namespace (default: from service account or "default")
//   - dsn: Secret data key containing the connection string (default: "dsn.txt")
func (s *Strategy) Watch(ctx context.Context, pth string, pathQry string) (string, <-chan string, error) {
	pth = strings.TrimSpace(pth)
	pathQry = strings.TrimSpace(pathQry)

	params, err := url.ParseQuery(pathQry)
	if err != nil {
		return "", nil, fmt.Errorf("k8ssecret: parse query %q: %w", pathQry, err)
	}

	namespace := params.Get("namespace")
	if namespace == "" {
		namespace = podNamespace()
	}
	key := params.Get("dsn")
	if key == "" {
		key = "dsn.txt"
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

	watchKey := namespace + "/" + pth

	sw, exists := s.watches[watchKey]
	if !exists {
		// Fetch the initial value.
		secret, err := s.clientset.CoreV1().Secrets(namespace).Get(ctx, pth, metav1.GetOptions{})
		if err != nil {
			return "", nil, fmt.Errorf("k8ssecret: get secret %s/%s: %w", namespace, pth, err)
		}
		val, ok := secret.Data[key]
		if !ok {
			return "", nil, fmt.Errorf("k8ssecret: secret %s/%s has no key %q", namespace, pth, key)
		}

		watchCtx, cancel := context.WithCancel(context.Background())
		sw = &secretWatch{
			cancel:  cancel,
			value:   string(val),
			queries: make(map[string]*queryWatch),
		}
		s.watches[watchKey] = sw

		go s.runWatch(watchCtx, namespace, pth, key, watchKey)
	}

	qw, exists := sw.queries[pathQry]
	if !exists {
		qw = &queryWatch{ch: make(chan string, 1)}
		sw.queries[pathQry] = qw
	}

	return sw.value, qw.ch, nil
}

// CloseWatch implements hotload.Strategy.
func (s *Strategy) CloseWatch(pth string, pathQry string) error {
	pth = strings.TrimSpace(pth)
	pathQry = strings.TrimSpace(pathQry)

	params, _ := url.ParseQuery(pathQry)
	namespace := params.Get("namespace")
	if namespace == "" {
		namespace = podNamespace()
	}
	watchKey := namespace + "/" + pth

	s.mu.Lock()
	defer s.mu.Unlock()

	sw, ok := s.watches[watchKey]
	if !ok {
		return nil
	}

	qw, ok := sw.queries[pathQry]
	if ok {
		close(qw.ch)
		delete(sw.queries, pathQry)
	}

	if len(sw.queries) == 0 {
		sw.cancel()
		delete(s.watches, watchKey)
	}

	return nil
}

// Close implements hotload.Strategy.
func (s *Strategy) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for watchKey, sw := range s.watches {
		sw.cancel()
		for _, qw := range sw.queries {
			close(qw.ch)
		}
		delete(s.watches, watchKey)
	}
}

// runWatch establishes a Kubernetes watch on the Secret and pushes updates.
func (s *Strategy) runWatch(ctx context.Context, namespace, name, key, watchKey string) {
	for {
		if ctx.Err() != nil {
			return
		}

		err := s.doWatch(ctx, namespace, name, key, watchKey)
		if ctx.Err() != nil {
			return
		}

		logger.Logf("[k8ssecret]", "watch %s/%s interrupted: %v, reconnecting...", namespace, name, err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

func (s *Strategy) doWatch(ctx context.Context, namespace, name, key, watchKey string) error {
	watcher, err := s.clientset.CoreV1().Secrets(namespace).Watch(ctx, metav1.ListOptions{
		FieldSelector: "metadata.name=" + name,
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
			if event.Type != watch.Modified {
				continue
			}
			secret, ok := event.Object.(*corev1.Secret)
			if !ok {
				continue
			}
			val, ok := secret.Data[key]
			if !ok {
				continue
			}

			s.mu.Lock()
			sw, exists := s.watches[watchKey]
			if exists && string(val) != sw.value {
				sw.value = string(val)
				for _, qw := range sw.queries {
					select {
					case qw.ch <- string(val):
					default:
						// Drop if consumer is slow — they'll get the next update.
					}
				}
			}
			s.mu.Unlock()
		}
	}
}

// podNamespace returns the namespace of the current pod from the service account
// mount, or "default" if not running in-cluster.
func podNamespace() string {
	if ns, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace"); err == nil {
		return strings.TrimSpace(string(ns))
	}
	return "default"
}
