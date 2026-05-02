package api

import (
	"net"
	"net/http"
	"time"
)

// recoverMiddleware catches panics from any downstream handler, logs them,
// and replies with the INTERNAL error envelope.
func (s *Server) recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.logger.Error("panic in handler",
					"panic", rec,
					"method", r.Method,
					"path", r.URL.Path,
				)
				writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// requestLogMiddleware emits a structured slog Info record per request.
func (s *Server) requestLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rwc := &responseWriterCapture{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rwc, r)
		s.logger.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"remote", r.RemoteAddr,
			"status", rwc.status,
			"bytes", rwc.bytes,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}

// responseWriterCapture observes status code and bytes written.
type responseWriterCapture struct {
	http.ResponseWriter
	status      int
	bytes       int
	wroteHeader bool
}

func (rwc *responseWriterCapture) WriteHeader(status int) {
	if !rwc.wroteHeader {
		rwc.status = status
		rwc.wroteHeader = true
	}
	rwc.ResponseWriter.WriteHeader(status)
}

func (rwc *responseWriterCapture) Write(b []byte) (int, error) {
	if !rwc.wroteHeader {
		rwc.wroteHeader = true
	}
	n, err := rwc.ResponseWriter.Write(b)
	rwc.bytes += n
	return n, err
}

// checkLoopbackMiddleware rejects requests from any host that isn't the
// IPv4 or IPv6 loopback. Defense in depth on top of the tcp4/127.0.0.1
// listen bind in Run.
func (s *Server) checkLoopbackMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil || (host != "127.0.0.1" && host != "::1") {
			http.Error(w, "loopback only", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// corsMiddleware applies the spec §5.3 CORS policy: only AllowedOrigins
// receive the headers. Requests without an Origin header (curl, agentctl)
// pass through unannotated. OPTIONS preflight short-circuits with 204.
func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	allowed := make(map[string]bool, len(s.cfg.AllowedOrigins))
	for _, o := range s.cfg.AllowedOrigins {
		allowed[o] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && allowed[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Terminal-Token")
		}
		if r.Method == http.MethodOptions {
			// Preflight (or any OPTIONS) — never reach the inner handler.
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
