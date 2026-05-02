package api

import (
	"context"
	"sync"
	"time"
)

// Result is a cached /print response, replayed verbatim on idempotent retries.
type Result struct {
	JobID     string
	Status    int
	Body      []byte
	ExpiresAt time.Time
}

// IdempotencyStore caches print results keyed by job_id with a TTL. Entries
// past their ExpiresAt are filtered on Get and physically removed by the
// janitor goroutine started via RunJanitor.
type IdempotencyStore struct {
	mu  sync.RWMutex
	m   map[string]Result
	ttl time.Duration
	now func() time.Time // injectable for tests
}

// NewIdempotencyStore returns a store with the given entry TTL.
func NewIdempotencyStore(ttl time.Duration) *IdempotencyStore {
	return &IdempotencyStore{
		m:   make(map[string]Result),
		ttl: ttl,
		now: time.Now,
	}
}

// Get returns the cached Result for jobID if present and unexpired.
func (s *IdempotencyStore) Get(jobID string) (Result, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.m[jobID]
	if !ok {
		return Result{}, false
	}
	if s.now().After(r.ExpiresAt) {
		return Result{}, false
	}
	return r, true
}

// Set stores r under jobID, stamping ExpiresAt = now + ttl.
func (s *IdempotencyStore) Set(jobID string, r Result) {
	r.ExpiresAt = s.now().Add(s.ttl)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[jobID] = r
}

// RunJanitor sweeps expired entries every interval until ctx is canceled.
// Returns immediately if interval is zero or negative.
func (s *IdempotencyStore) RunJanitor(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sweep()
		}
	}
}

func (s *IdempotencyStore) sweep() {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range s.m {
		if now.After(v.ExpiresAt) {
			delete(s.m, k)
		}
	}
}
