package engine

import (
	"crypto/sha256"
	"fmt"
	"sync"
	"time"

	"github.com/infobloxopen/hotload/internal/clock"
	"github.com/infobloxopen/hotload/internal/dsnutil"
)

// Event represents a DSN promotion event.
type Event struct {
	DSN       string
	Epoch     uint64
	At        time.Time
	SkipDrain bool // true if credential-only change with CredentialOnlyReload enabled
}

// Policy defines the behavior for DSN changes and connection draining.
type Policy struct {
	DrainTimeout         time.Duration // 0 => skip waiting (immediate kill allowed)
	ForceKill            bool
	Debounce             time.Duration // suppress churn (e.g., 250ms)
	Preconnect           bool          // try opening 1 probe conn before promotion
	CredentialOnlyReload bool          // skip draining when only username/password change
}

// Hooks provides callbacks for engine lifecycle events.
type Hooks struct {
	OnPromote     func(ev Event)
	OnDrainStart  func(oldEpoch uint64)
	OnDrainFinish func(oldEpoch uint64, drained, killed int)
	OnError       func(error)
}

// Engine manages DSN versioning with deduplication and debouncing.
type Engine interface {
	Current() Event
	Update(rawDSN string) (promoted *Event, changed bool)
	Policy() Policy
}

type engine struct {
	mu sync.RWMutex

	policy Policy
	hooks  Hooks
	clock  clock.Clock

	current     Event
	previousDSN string // Track previous DSN for credential-only detection
	lastHash    [32]byte
	lastUpdate  time.Time
	debounceEnd time.Time
}

// New creates a new Engine with the given policy, hooks, and clock.
func New(pol Policy, hooks Hooks, clk clock.Clock) Engine {
	if clk == nil {
		clk = clock.Real{}
	}
	return &engine{
		policy: pol,
		hooks:  hooks,
		clock:  clk,
		current: Event{
			DSN:   "",
			Epoch: 0,
			At:    clk.Now(),
		},
	}
}

func (e *engine) Current() Event {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.current
}

func (e *engine) Policy() Policy {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.policy
}

// Update processes a new DSN value with deduplication and debouncing.
// Returns promoted event and whether it was actually promoted.
func (e *engine) Update(rawDSN string) (*Event, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	now := e.clock.Now()
	hash := sha256.Sum256([]byte(rawDSN))

	// Deduplication: ignore if DSN hasn't changed
	if hash == e.lastHash {
		return nil, false
	}

	// Debounce: if we're within the debounce window, delay the update
	if e.policy.Debounce > 0 && now.Before(e.debounceEnd) {
		// Extend the debounce window
		e.debounceEnd = now.Add(e.policy.Debounce)
		return nil, false
	}

	// Check if this is a credential-only change
	skipDrain := false
	if e.policy.CredentialOnlyReload && e.previousDSN != "" {
		skipDrain = dsnutil.IsCredentialOnlyChange(e.previousDSN, rawDSN)
	}

	// Promote to new epoch
	newEpoch := e.current.Epoch + 1
	newEvent := Event{
		DSN:       rawDSN,
		Epoch:     newEpoch,
		At:        now,
		SkipDrain: skipDrain,
	}

	e.previousDSN = e.current.DSN
	e.current = newEvent
	e.lastHash = hash
	e.lastUpdate = now
	e.debounceEnd = now.Add(e.policy.Debounce)

	// Invoke promotion hook if provided
	if e.hooks.OnPromote != nil {
		// Call hook outside critical section to avoid deadlocks
		go func(ev Event) {
			defer func() {
				if r := recover(); r != nil && e.hooks.OnError != nil {
					e.hooks.OnError(fmt.Errorf("panic in OnPromote: %v", r))
				}
			}()
			e.hooks.OnPromote(ev)
		}(newEvent)
	}

	return &newEvent, true
}
