// Package persistence provides in-memory implementations of domain.Store and
// domain.Locker (SPECS §8). Offline default for tests and cmd bootstrap;
// production swaps in PostgreSQL (provisioning_record) + Redis (SET NX lock).
package persistence

import (
	"sync"
	"time"

	"github.com/open-strata-ai/ai-provisioning-engine/domain"
)

// Memory is a thread-safe domain.Store backed by an append-only record slice.
type Memory struct {
	mu      sync.RWMutex
	records []domain.Record
}

// NewStore builds an empty store.
func NewStore() *Memory { return &Memory{} }

// Save appends an audit record (SKILLS §12 S2).
func (m *Memory) Save(rec domain.Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now()
	}
	m.records = append(m.records, rec)
	return nil
}

// ByChecksum returns all records for a plan checksum in insertion order.
func (m *Memory) ByChecksum(checksum string) []domain.Record {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []domain.Record
	for _, r := range m.records {
		if r.PlanChecksum == checksum {
			out = append(out, r)
		}
	}
	return out
}

// Revisions returns the distinct revisions of a component in chronological order
// of first appearance (SPECS §8.2, up to the full history).
func (m *Memory) Revisions(component string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	seen := map[string]bool{}
	var out []string
	for _, r := range m.records {
		if r.Component != component || r.Revision == "" {
			continue
		}
		if seen[r.Revision] {
			continue
		}
		seen[r.Revision] = true
		out = append(out, r.Revision)
	}
	return out
}

// LastRevision returns the most recently recorded revision of a component.
func (m *Memory) LastRevision(component string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for i := len(m.records) - 1; i >= 0; i-- {
		if m.records[i].Component == component && m.records[i].Revision != "" {
			return m.records[i].Revision, true
		}
	}
	return "", false
}

// HasRevision reports whether a component ever had the given revision (S5).
func (m *Memory) HasRevision(component, revision string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, r := range m.records {
		if r.Component == component && r.Revision == revision {
			return true
		}
	}
	return false
}

var _ domain.Store = (*Memory)(nil)

// MemoryLocker is a thread-safe in-memory domain.Locker with TTL expiry
// (production: Redis SET NX, SKILLS §12 S7).
type MemoryLocker struct {
	mu    sync.Mutex
	locks map[string]time.Time // key -> expiry
	now   func() time.Time
}

// NewLocker builds an empty locker.
func NewLocker() *MemoryLocker {
	return &MemoryLocker{locks: map[string]time.Time{}, now: time.Now}
}

// Acquire grabs the lock if free or expired; returns false if held and live.
func (l *MemoryLocker) Acquire(key string, ttl time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if exp, ok := l.locks[key]; ok && exp.After(now) {
		return false
	}
	l.locks[key] = now.Add(ttl)
	return true
}

// Release frees the lock.
func (l *MemoryLocker) Release(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.locks, key)
}

var _ domain.Locker = (*MemoryLocker)(nil)
