package fsnotify

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	rfsnotify "github.com/fsnotify/fsnotify"
	"github.com/infobloxopen/hotload"
	"github.com/infobloxopen/hotload/internal"
	"github.com/infobloxopen/hotload/logger"
)

func init() {
	hotload.RegisterStrategy("fsnotify", NewStrategy())
}

const (
	defaultResyncPeriod = 2 * time.Second
)

var (
	// updateChannelBufferSize is the buffer size for update channels.
	// Can be configured via SetUpdateChannelBufferSize before any Strategy is created.
	updateChannelBufferSize = 10
)

// SetUpdateChannelBufferSize sets the buffer size for update channels.
// This must be called before creating any Strategy instances to take effect.
func SetUpdateChannelBufferSize(size int) {
	if size < 0 {
		size = 0
	}
	updateChannelBufferSize = size
}

// NewStrategy creates a new fsnotify-based hotload strategy that monitors
// file changes using the fsnotify library.
func NewStrategy() *Strategy {
	return NewStrategyWithResync(defaultResyncPeriod)
}

// NewStrategyWithResync creates a strategy with a custom resync period.
// Exposed for testing purposes.
func NewStrategyWithResync(resyncPeriod time.Duration) *Strategy {
	return &Strategy{
		paths:        make(map[string]*pathWatch),
		resyncPeriod: resyncPeriod,
	}
}

// Strategy implements the hotload.Strategy interface using fsnotify
// to watch for file changes. It maintains a single goroutine per
// watched file and broadcasts updates to all subscribers.
//
// Thread-safety: All public methods are safe for concurrent use.
type Strategy struct {
	mu           sync.RWMutex
	paths        map[string]*pathWatch
	watcher      watcher
	resyncPeriod time.Duration
	runLoopDone  chan struct{}
}

// pathWatch represents a single file being monitored. It maintains
// the current value and broadcasts updates to all subscribers.
type pathWatch struct {
	mu          sync.RWMutex
	path        string
	value       string
	contentHash [32]byte // SHA-256 hash of current content for deduplication
	subscribers map[string]*subscriber
	ctx         context.Context
	cancel      context.CancelFunc
}

// subscriber represents a single Watch() caller receiving updates.
type subscriber struct {
	id         string
	updateChan chan string
	ctx        context.Context
}

// readConfigFile reads and trims the config file content.
func (s *Strategy) readConfigFile(filepath string) (string, error) {
	data, err := os.ReadFile(filepath)
	if err != nil {
		return "", fmt.Errorf("could not read %s: %w", filepath, err)
	}
	return strings.TrimSpace(string(data)), nil
}

// Watch implements the hotload.Strategy interface.
// It returns the current value and a channel for subsequent updates.
// The updates channel is closed when the context is cancelled.
func (s *Strategy) Watch(ctx context.Context, pth string, query map[string][]string) (value string, values <-chan string, err error) {
	pth = path.Clean(pth)

	s.mu.Lock()
	defer s.mu.Unlock()

	// Initialize watcher on first use
	if s.watcher == nil {
		watcher, err := notifyConstructor()
		if err != nil {
			return "", nil, fmt.Errorf("failed to create watcher: %w", err)
		}
		s.watcher = watcher
		s.runLoopDone = make(chan struct{})
		go s.runLoop()
	}

	// Get or create pathWatch
	pw, found := s.paths[pth]
	if !found {
		// Read initial value
		initialValue, err := s.readConfigFile(pth)
		if err != nil {
			return "", nil, err
		}

		// Add to fsnotify watcher
		if err := s.watcher.Add(pth); err != nil {
			return "", nil, fmt.Errorf("failed to watch %s: %w", pth, err)
		}

		watchCtx, cancel := context.WithCancel(context.Background())
		pw = &pathWatch{
			path:        pth,
			value:       initialValue,
			contentHash: sha256.Sum256([]byte(initialValue)),
			subscribers: make(map[string]*subscriber),
			ctx:         watchCtx,
			cancel:      cancel,
		}
		s.paths[pth] = pw
		s.logf("fsnotify.Watch", "started watching path: %s", pth)
	}

	// Create subscriber - use query map to build unique ID
	queryStr := url.Values(query).Encode()
	subID := fmt.Sprintf("%s?%s", pth, queryStr)
	updateChan := make(chan string, updateChannelBufferSize)

	sub := &subscriber{
		id:         subID,
		updateChan: updateChan,
		ctx:        ctx,
	}

	pw.mu.Lock()
	pw.subscribers[subID] = sub
	currentValue := pw.value
	pw.mu.Unlock()

	// Monitor context cancellation
	go s.monitorSubscriber(pth, subID, ctx)

	return currentValue, updateChan, nil
}

// monitorSubscriber watches for context cancellation and cleans up.
func (s *Strategy) monitorSubscriber(pth, subID string, ctx context.Context) {
	<-ctx.Done()
	s.removeSubscriber(pth, subID)
}

// removeSubscriber removes a subscriber and cleans up if no subscribers remain.
func (s *Strategy) removeSubscriber(pth, subID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	pw, found := s.paths[pth]
	if !found {
		return
	}

	pw.mu.Lock()
	sub, ok := pw.subscribers[subID]
	if ok {
		close(sub.updateChan)
		delete(pw.subscribers, subID)
		s.logf("fsnotify.removeSubscriber", "removed subscriber: %s", subID)
	}
	subscriberCount := len(pw.subscribers)
	pw.mu.Unlock()

	// If no more subscribers, stop watching the path
	if subscriberCount == 0 {
		pw.cancel()
		if err := s.watcher.Remove(pth); err != nil {
			s.errlogf("fsnotify.removeSubscriber", "failed to remove watch for %s: %v", pth, err)
		}
		delete(s.paths, pth)
		s.logf("fsnotify.removeSubscriber", "stopped watching path: %s", pth)
	}
}

// Close implements the hotload.Strategy interface.
// It stops watching all paths and closes all update channels.
func (s *Strategy) Close() error {
	s.mu.Lock()

	if s.watcher != nil {
		s.watcher.Close()
		s.mu.Unlock()

		// Wait for runLoop to finish before setting watcher to nil
		if s.runLoopDone != nil {
			<-s.runLoopDone
		}

		s.mu.Lock()
		s.watcher = nil
	}

	for _, pw := range s.paths {
		pw.cancel()
		pw.mu.Lock()
		for _, sub := range pw.subscribers {
			close(sub.updateChan)
		}
		pw.subscribers = nil
		pw.mu.Unlock()
	}

	s.paths = make(map[string]*pathWatch)
	s.logf("fsnotify.Close", "closed strategy")
	s.mu.Unlock()

	return nil
}

// runLoop is the main event loop that processes fsnotify events.
// It runs in a dedicated goroutine and handles file changes and errors.
func (s *Strategy) runLoop() {
	defer func() {
		if s.runLoopDone != nil {
			close(s.runLoopDone)
		}
	}()

	ticker := time.NewTicker(s.resyncPeriod)
	defer ticker.Stop()

	failedPaths := make(map[string]struct{})

	for {
		select {
		case ev, ok := <-s.watcher.GetEvents():
			if !ok {
				s.logf("fsnotify.runLoop", "events channel closed, terminating")
				return
			}

			// Only process Write and Remove events
			if !ev.Has(rfsnotify.Write) && !ev.Has(rfsnotify.Remove) {
				continue
			}

			s.logf("fsnotify.runLoop", "received event: %s", ev.String())

			if err := s.handleFileChange(ev.Name); err != nil {
				s.errlogf("fsnotify.runLoop", "failed to handle change for %s: %v", ev.Name, err)
				failedPaths[ev.Name] = struct{}{}
			} else {
				// Remove from failed paths if successfully processed
				delete(failedPaths, ev.Name)
			}

		case err, ok := <-s.watcher.GetErrors():
			if !ok {
				s.logf("fsnotify.runLoop", "errors channel closed, terminating")
				return
			}
			s.errlogf("fsnotify.runLoop", "watcher error: %v", err)

		case <-ticker.C:
			// Retry failed paths
			if len(failedPaths) > 0 {
				s.logf("fsnotify.runLoop", "ticker fired: retrying %d failed paths", len(failedPaths))
				for pth := range failedPaths {
					if err := s.handleFileChange(pth); err == nil {
						delete(failedPaths, pth)
						s.logf("fsnotify.runLoop", "successfully recovered path: %s", pth)
					} else {
						s.errlogf("fsnotify.runLoop", "retry failed for path %s: %v", pth, err)
					}
				}
			} else {
				// Periodically check all watched paths even if no failures
				s.mu.RLock()
				watchedPaths := make([]string, 0, len(s.paths))
				for pth := range s.paths {
					watchedPaths = append(watchedPaths, pth)
				}
				s.mu.RUnlock()

				if len(watchedPaths) > 0 {
					s.logf("fsnotify.runLoop", "ticker fired: checking %d watched paths", len(watchedPaths))
					for _, pth := range watchedPaths {
						if err := s.handleFileChange(pth); err != nil {
							s.errlogf("fsnotify.runLoop", "resync failed for %s: %v", pth, err)
							failedPaths[pth] = struct{}{}
						}
					}
				}
			}
		}
	}
}

// handleFileChange re-reads the file and broadcasts updates to subscribers.
// It uses content hashing to detect actual changes and only broadcasts if
// the content hash differs from the stored hash.
func (s *Strategy) handleFileChange(pth string) error {
	s.logf("fsnotify.handleFileChange", "processing change for path: %s", pth)

	// Re-add to watcher (handles file recreate scenarios)
	s.watcher.Remove(pth)
	if err := s.watcher.Add(pth); err != nil {
		s.errlogf("fsnotify.handleFileChange", "failed to re-add watch for %s: %v", pth, err)
		return fmt.Errorf("failed to re-add watch: %w", err)
	}
	s.logf("fsnotify.handleFileChange", "successfully re-added watch for: %s", pth)

	// Read new value
	newValue, err := s.readConfigFile(pth)
	if err != nil {
		s.errlogf("fsnotify.handleFileChange", "failed to read file %s: %v", pth, err)
		return err
	}
	s.logf("fsnotify.handleFileChange", "read %d bytes from %s", len(newValue), pth)

	// Calculate content hash
	newHash := sha256.Sum256([]byte(newValue))

	// Broadcast to subscribers
	s.mu.RLock()
	pw, found := s.paths[pth]
	s.mu.RUnlock()

	if !found {
		s.logf("fsnotify.handleFileChange", "path %s no longer watched", pth)
		return nil // Path no longer watched
	}

	pw.mu.Lock()
	oldHash := pw.contentHash

	// Compare hashes - if unchanged, no event is emitted
	if oldHash == newHash {
		pw.mu.Unlock()
		s.logf("fsnotify.handleFileChange", "content unchanged for %s (hash match)", pth)
		return nil // No change in content
	}

	// Content changed - update stored value and hash
	pw.value = newValue
	pw.contentHash = newHash

	redactedDSN := internal.RedactUrl(newValue)
	subscribers := make([]*subscriber, 0, len(pw.subscribers))
	for _, sub := range pw.subscribers {
		subscribers = append(subscribers, sub)
	}
	subscriberCount := len(subscribers)
	pw.mu.Unlock()

	// Send updates outside the lock
	s.logf("fsnotify.handleFileChange", "broadcasting update for %s to %d subscribers (redacted: %s)", pth, subscriberCount, redactedDSN)
	for _, sub := range subscribers {
		select {
		case sub.updateChan <- newValue:
			s.logf("fsnotify.handleFileChange", "sent update to subscriber %s", sub.id)
		case <-sub.ctx.Done():
			s.logf("fsnotify.handleFileChange", "subscriber %s context cancelled, skipping", sub.id)
		default:
			// Channel full, drop update (subscriber is slow)
			s.errlogf("fsnotify.handleFileChange", "dropped update for slow subscriber %s: channel buffer full", sub.id)
		}
	}

	return nil
}

// Logging helpers
func (s *Strategy) logf(prefix, format string, args ...any) {
	logger.Logf(prefix+":", format, args...)
}

func (s *Strategy) errlogf(prefix, format string, args ...any) {
	logger.ErrLogf(prefix+":", format, args...)
}
