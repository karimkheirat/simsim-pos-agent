package scale

import (
	"context"
	"sync"
)

// Mock is the test backend for Scale — the moral equivalent of the api
// package's fakePrinter, exported here so both this package's and the
// api package's tests can share it.
//
// Zero value: reachable=false, every PLU accepted. Configure fields
// before use; safe for concurrent calls.
type Mock struct {
	// MockName is returned by Name(); defaults to "mock".
	MockName string
	// Reachable is returned by IsReachable().
	Reachable bool
	// Err, when non-nil, is returned by SendPLUs as the session error
	// (entries are all marked "not attempted: session aborted").
	Err error
	// FailPLUs maps a PLU code to the per-PLU error string it should
	// fail with. Entries not present succeed.
	FailPLUs map[string]string

	mu    sync.Mutex
	calls [][]PLU
}

// SendPLUs records the call and fabricates Results per configuration.
func (m *Mock) SendPLUs(_ context.Context, entries []PLU) ([]Result, error) {
	m.mu.Lock()
	snapshot := make([]PLU, len(entries))
	copy(snapshot, entries)
	m.calls = append(m.calls, snapshot)
	m.mu.Unlock()

	results := make([]Result, len(entries))
	for i, e := range entries {
		results[i] = Result{PLU: e.PLU}
		if m.Err != nil {
			results[i].Error = "not attempted: session aborted"
			continue
		}
		if msg, bad := m.FailPLUs[e.PLU]; bad {
			results[i].Error = msg
			continue
		}
		results[i].OK = true
	}
	return results, m.Err
}

// IsReachable returns the configured flag.
func (m *Mock) IsReachable() bool { return m.Reachable }

// Name returns MockName or "mock".
func (m *Mock) Name() string {
	if m.MockName != "" {
		return m.MockName
	}
	return "mock"
}

// Calls returns a copy of every SendPLUs invocation's entries.
func (m *Mock) Calls() [][]PLU {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([][]PLU, len(m.calls))
	copy(out, m.calls)
	return out
}

// Compile-time assertion that *Mock satisfies Scale.
var _ Scale = (*Mock)(nil)
