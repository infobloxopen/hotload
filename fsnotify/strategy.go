package fsnotify

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/infobloxopen/hotload"
)

func init() {
	hotload.RegisterStrategy("fsnotify", NewStrategy())
}

type Strategy struct {
	mu      sync.RWMutex
	watcher *fsnotify.Watcher
	watches map[string]*fileWatch
	started bool
}

type fileWatch struct {
	path        string
	hash        [32]byte // SHA-256 of current content
	subscribers []subscriber
	ctx         context.Context
	cancel      context.CancelFunc
	mu          sync.RWMutex
}

// subscriber represents a single Watch() caller.
type subscriber struct {
	updates chan<- string
	ctx     context.Context
}

// NewStrategy creates a new fsnotify-based strategy.
func NewStrategy() *Strategy {
	return &Strategy{
		watches: make(map[string]*fileWatch),
	}
}

func (s *Strategy) Watch(ctx context.Context, path string, query url.Values) (initial string, updates <-chan string, err error) {
	path = filepath.Clean(path)

	content, hash, err := s.readFile(path)
	if err != nil {
		return "", nil, fmt.Errorf("failed to read initial file: %w", err)
	}

	updateChan := make(chan string, 10)

	if err := s.ensureWatcher(); err != nil {
		return "", nil, fmt.Errorf("failed to start watcher: %w", err)
	}

	fw, err := s.getOrCreateFileWatch(path, hash)
	if err != nil {
		return "", nil, err
	}

	fw.mu.Lock()
	fw.subscribers = append(fw.subscribers, subscriber{
		updates: updateChan,
		ctx:     ctx,
	})
	fw.mu.Unlock()

	// Monitor context cancellation
	go s.monitorContext(ctx, path, updateChan)

	return content, updateChan, nil
}

func (s *Strategy) ensureWatcher() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.started {
		return nil
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create watcher: %w", err)
	}

	s.watcher = watcher
	s.started = true

	go s.eventLoop()

	return nil
}

func (s *Strategy) getOrCreateFileWatch(path string, initialHash [32]byte) (*fileWatch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if fw, ok := s.watches[path]; ok {
		return fw, nil
	}

	// Add to fsnotify watcher
	if err := s.watcher.Add(path); err != nil {
		return nil, fmt.Errorf("failed to add watch: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	fw := &fileWatch{
		path:        path,
		hash:        initialHash,
		subscribers: make([]subscriber, 0),
		ctx:         ctx,
		cancel:      cancel,
	}

	s.watches[path] = fw
	return fw, nil
}

func (s *Strategy) eventLoop() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case event, ok := <-s.watcher.Events:
			if !ok {
				return
			}
			s.handleEvent(event)

		case err, ok := <-s.watcher.Errors:
			if !ok {
				return
			}
			_ = err

		case <-ticker.C:
			s.recheckAllFiles()
		}
	}
}

func (s *Strategy) handleEvent(event fsnotify.Event) {
	if !event.Has(fsnotify.Write) && !event.Has(fsnotify.Remove) &&
	   !event.Has(fsnotify.Create) && !event.Has(fsnotify.Rename) {
		return
	}

	path := filepath.Clean(event.Name)

	if event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
		time.Sleep(10 * time.Millisecond) // Brief delay for atomic operations
		s.watcher.Remove(path)
		s.watcher.Add(path)
	}

	s.checkFileChange(path)
}

func (s *Strategy) checkFileChange(path string) {
	s.mu.RLock()
	fw, ok := s.watches[path]
	s.mu.RUnlock()

	if !ok {
		return
	}

	content, newHash, err := s.readFile(path)
	if err != nil {
		return
	}

	fw.mu.Lock()
	oldHash := fw.hash
	if oldHash == newHash {
		fw.mu.Unlock()
		return // No change
	}

	fw.hash = newHash
	subscribers := make([]subscriber, len(fw.subscribers))
	copy(subscribers, fw.subscribers)
	fw.mu.Unlock()

	for _, sub := range subscribers {
		select {
		case sub.updates <- content:
		case <-sub.ctx.Done():
		default:
			// Channel full, drop update (subscriber is slow)
		}
	}
}

func (s *Strategy) recheckAllFiles() {
	s.mu.RLock()
	paths := make([]string, 0, len(s.watches))
	for path := range s.watches {
		paths = append(paths, path)
	}
	s.mu.RUnlock()

	for _, path := range paths {
		s.checkFileChange(path)
	}
}

func (s *Strategy) monitorContext(ctx context.Context, path string, updateChan chan<- string) {
	<-ctx.Done()

	s.mu.RLock()
	fw, ok := s.watches[path]
	s.mu.RUnlock()

	if !ok {
		close(updateChan)
		return
	}

	fw.mu.Lock()
	for i, sub := range fw.subscribers {
		if sub.updates == updateChan {
			fw.subscribers = append(fw.subscribers[:i], fw.subscribers[i+1:]...)
			break
		}
	}
	remaining := len(fw.subscribers)
	fw.mu.Unlock()

	close(updateChan)

	// If no more subscribers, stop watching this file
	if remaining == 0 {
		s.mu.Lock()
		if fw, ok := s.watches[path]; ok {
			fw.cancel()
			s.watcher.Remove(path)
			delete(s.watches, path)
		}
		s.mu.Unlock()
	}
}

func (s *Strategy) readFile(path string) (string, [32]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", [32]byte{}, err
	}

	content := strings.TrimSpace(string(data))

	hash := sha256.Sum256([]byte(content))

	return content, hash, nil
}
