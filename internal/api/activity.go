package api

import (
	"net/http"
	"sync/atomic"
	"time"
)

// activityTracker records when the agent last touched hardware on behalf
// of the till — a print, a label print, a test print or a drawer kick.
// The self-updater (internal/updater) reads it to refuse a binary swap
// within 60 s of a job (POS_AGENT_SPEC.md §9.3): a restart mid-sale
// would lose a receipt or leave the drawer shut on a cash sale.
//
// Cheap by construction: one atomic store on entry and exit of each
// hardware handler, plus an in-flight counter so a job slower than the
// quiet window still reads as "busy" while it runs.
type activityTracker struct {
	lastUnixNano atomic.Int64
	inFlight     atomic.Int64
}

// wrap marks activity when the handler starts and again when it ends.
func (a *activityTracker) wrap(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a.inFlight.Add(1)
		a.lastUnixNano.Store(time.Now().UnixNano())
		defer func() {
			a.lastUnixNano.Store(time.Now().UnixNano())
			a.inFlight.Add(-1)
		}()
		h(w, r)
	}
}

// LastHardwareActivity returns the time of the most recent print /
// label / test-print / drawer request (start or finish, whichever is
// later), and whether one is in flight right now. The zero time means
// no hardware request since the process started.
func (s *Server) LastHardwareActivity() (last time.Time, inFlight bool) {
	n := s.activity.lastUnixNano.Load()
	if n != 0 {
		last = time.Unix(0, n)
	}
	return last, s.activity.inFlight.Load() > 0
}

// Ready is closed once Run has bound its listener — the updater waits on
// it before starting the 60 s "healthy" clock that finalizes an update.
func (s *Server) Ready() <-chan struct{} {
	return s.ready
}
