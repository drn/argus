// Package clipboard provides an in-memory, per-task staging area for text
// that agents want the user to copy. Solves the iOS Safari constraint that
// navigator.clipboard.writeText() requires a synchronous user gesture: the
// agent stages text via Set, the user takes a one-tap/one-keypress action
// that performs the actual OS-clipboard write.
//
// The store is intentionally ephemeral: per-task slot, last-write-wins, no
// persistence across daemon restarts, and entries auto-expire after a TTL.
package clipboard

import (
	"sync"
	"time"
)

// DefaultTTL bounds how long a staged payload stays available without being
// copied. Five minutes is generous for an agent → user handoff and short
// enough that stale buttons don't sit forever.
const DefaultTTL = 5 * time.Minute

// MaxTextSize caps the size of a single staged payload. Anything larger is
// almost certainly a bug — the user can't usefully one-tap-copy a megabyte
// of text. Set returns ErrTooLarge when exceeded.
const MaxTextSize = 1 << 20 // 1 MiB

// ErrTooLarge is returned by Set when text exceeds MaxTextSize.
type ErrTooLarge struct {
	Size int
	Max  int
}

func (e *ErrTooLarge) Error() string {
	return "clipboard: text too large"
}

type entry struct {
	text     string
	expireAt time.Time
}

type subscriber struct {
	id int64
	fn func(text string)
}

// Store holds per-task staged text. Safe for concurrent use.
type Store struct {
	ttl  time.Duration
	now  func() time.Time // injectable clock for tests
	mu   sync.Mutex
	data map[string]entry
	subs map[string][]subscriber
	next int64
}

// New creates a Store with the default TTL.
func New() *Store {
	return NewWithTTL(DefaultTTL)
}

// NewWithTTL creates a Store with a custom TTL. Mostly used by tests.
func NewWithTTL(ttl time.Duration) *Store {
	return &Store{
		ttl:  ttl,
		now:  time.Now,
		data: make(map[string]entry),
		subs: make(map[string][]subscriber),
	}
}

// Set stages text for a task. Last-write-wins. An empty taskID is rejected.
// Notifies all subscribers for that taskID with the new text.
func (s *Store) Set(taskID, text string) error {
	if taskID == "" {
		return nil
	}
	if len(text) > MaxTextSize {
		return &ErrTooLarge{Size: len(text), Max: MaxTextSize}
	}
	s.mu.Lock()
	s.data[taskID] = entry{text: text, expireAt: s.now().Add(s.ttl)}
	subs := append([]subscriber(nil), s.subs[taskID]...)
	s.mu.Unlock()
	for _, sub := range subs {
		sub.fn(text)
	}
	return nil
}

// Get returns the staged text and a presence flag. Expired entries are
// pruned and reported as absent.
func (s *Store) Get(taskID string) (string, bool) {
	if taskID == "" {
		return "", false
	}
	s.mu.Lock()
	e, ok := s.data[taskID]
	if !ok {
		s.mu.Unlock()
		return "", false
	}
	if !e.expireAt.IsZero() && s.now().After(e.expireAt) {
		delete(s.data, taskID)
		subs := append([]subscriber(nil), s.subs[taskID]...)
		s.mu.Unlock()
		// Notify subscribers that the entry was cleared via expiry.
		for _, sub := range subs {
			sub.fn("")
		}
		return "", false
	}
	s.mu.Unlock()
	return e.text, true
}

// Clear removes the staged text for a task. Notifies subscribers with an
// empty string so listeners can hide their UI affordance. No-op if nothing
// was staged.
func (s *Store) Clear(taskID string) {
	if taskID == "" {
		return
	}
	s.mu.Lock()
	_, had := s.data[taskID]
	delete(s.data, taskID)
	var subs []subscriber
	if had {
		subs = append([]subscriber(nil), s.subs[taskID]...)
	}
	s.mu.Unlock()
	for _, sub := range subs {
		sub.fn("")
	}
}

// Subscribe registers fn to be called whenever the entry for taskID is set
// or cleared. Returns an unsubscribe function. Subscribers are NOT called
// for the existing value at registration time — the caller is expected to
// call Get to fetch initial state.
func (s *Store) Subscribe(taskID string, fn func(text string)) func() {
	s.mu.Lock()
	id := s.next
	s.next++
	s.subs[taskID] = append(s.subs[taskID], subscriber{id: id, fn: fn})
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		list := s.subs[taskID]
		for i, sub := range list {
			if sub.id == id {
				s.subs[taskID] = append(list[:i], list[i+1:]...)
				if len(s.subs[taskID]) == 0 {
					delete(s.subs, taskID)
				}
				return
			}
		}
	}
}

// Prune removes expired entries. Call periodically; not strictly required
// because Get also prunes lazily.
func (s *Store) Prune() {
	now := s.now()
	s.mu.Lock()
	expired := make([]string, 0)
	for id, e := range s.data {
		if !e.expireAt.IsZero() && now.After(e.expireAt) {
			expired = append(expired, id)
		}
	}
	for _, id := range expired {
		delete(s.data, id)
	}
	subs := make(map[string][]subscriber, len(expired))
	for _, id := range expired {
		if list, ok := s.subs[id]; ok {
			subs[id] = append([]subscriber(nil), list...)
		}
	}
	s.mu.Unlock()
	for _, list := range subs {
		for _, sub := range list {
			sub.fn("")
		}
	}
}
