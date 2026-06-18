package api

import (
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/karimkheirat/simsim-pos-agent/internal/config"
	"github.com/karimkheirat/simsim-pos-agent/internal/jwt"
)

// French operator-facing messages for auth failures. The agent's local
// API speaks French because operators read the resulting POS web app
// errors directly.
const (
	errMsgNotPaired       = "Agent non jumelé. Exécutez 'agentctl pair'."
	errMsgUnauthenticated = "Token invalide ou manquant."
	errMsgJWTInvalid      = "Jeton JWT mal formé."
	errMsgJWTExpired      = "Jeton JWT expiré."
	errMsgSignatureBad    = "Signature du jeton invalide."
	errMsgAudienceBad     = "Audience du jeton incorrecte."
	errMsgIssuerBad       = "Émetteur du jeton incorrect."
)

// requireTerminalToken gates a handler on (1) the agent being paired
// (secrets present), and (2) the request carrying an X-Terminal-Token
// header that constant-time-equals the stored token.
//
// Constant-time compare via crypto/subtle is critical: a naive == compare
// leaks token bytes via timing on a long-lived listener. ConstantTimeCompare
// short-circuits on length mismatch — for our fixed-length tokens that's
// fine; in any case missing/wrong-length tokens fail through the same
// 401 UNAUTHENTICATED path.
func (s *Server) requireTerminalToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.secrets == nil {
			writeError(w, http.StatusUnauthorized, CodeNotPaired, errMsgNotPaired)
			return
		}
		secrets, err := s.secrets.Load()
		if errors.Is(err, config.ErrNoSecrets) {
			writeError(w, http.StatusUnauthorized, CodeNotPaired, errMsgNotPaired)
			return
		}
		if err != nil {
			s.logger.Error("requireTerminalToken: secret store load failed", "err", err.Error())
			writeError(w, http.StatusInternalServerError, CodeInternal, "Erreur d'accès aux secrets.")
			return
		}
		provided := r.Header.Get("X-Terminal-Token")
		if subtle.ConstantTimeCompare([]byte(provided), []byte(secrets.TerminalToken)) != 1 {
			writeError(w, http.StatusUnauthorized, CodeUnauthenticated, errMsgUnauthenticated)
			return
		}
		next(w, r)
	}
}

// requireAuth is the M13 A.1 auth gate for /print and /test-print. It
// accepts EITHER:
//
//   - a JWT in `Authorization: Bearer <jwt>` (the M13 path — minted by
//     GET /handshake, signed with the terminal token), OR
//   - the legacy `X-Terminal-Token` header (the pre-M13 path, kept
//     working alongside JWT during the migration window — removal is
//     A.3, once the web client has cut over).
//
// Header precedence: if `Authorization: Bearer` is present it is
// evaluated and its result is returned — a present-but-bad JWT fails
// with its specific code, it does NOT fall through to the
// X-Terminal-Token path. Only an ABSENT Authorization header falls
// through to the legacy check. This mirrors the flow in
// docs/agent-handshake-protocol.md §10.1 and avoids ambiguous
// double-evaluation.
//
// /drawer/open and /status are intentionally NOT moved to requireAuth
// in A.1 — they stay on requireTerminalToken. They aren't print
// operations and aren't in the A.1 scope.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.secrets == nil {
			writeError(w, http.StatusUnauthorized, CodeNotPaired, errMsgNotPaired)
			return
		}
		secrets, err := s.secrets.Load()
		if errors.Is(err, config.ErrNoSecrets) {
			writeError(w, http.StatusUnauthorized, CodeNotPaired, errMsgNotPaired)
			return
		}
		if err != nil {
			s.logger.Error("requireAuth: secret store load failed", "err", err.Error())
			writeError(w, http.StatusInternalServerError, CodeInternal, "Erreur d'accès aux secrets.")
			return
		}

		// JWT path — Authorization: Bearer <jwt>.
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			rawToken := strings.TrimPrefix(authHeader, "Bearer ")
			claims, verr := jwt.Verify(rawToken, []byte(secrets.TerminalToken), time.Now())
			if verr != nil {
				switch {
				case errors.Is(verr, jwt.ErrExpired):
					writeError(w, http.StatusUnauthorized, CodeJWTExpired, errMsgJWTExpired)
				case errors.Is(verr, jwt.ErrBadSignature):
					writeError(w, http.StatusUnauthorized, CodeSignatureInvalid, errMsgSignatureBad)
				default: // jwt.ErrMalformed and any other parse failure
					writeError(w, http.StatusUnauthorized, CodeJWTInvalid, errMsgJWTInvalid)
				}
				return
			}
			// Application-policy claim checks — aud + iss. The jwt
			// package owns integrity (signature, structure, expiry);
			// the agent owns "is this token FOR me, FOR this purpose."
			if claims.Aud != handshakeAudience {
				writeError(w, http.StatusUnauthorized, CodeAudienceMismatch, errMsgAudienceBad)
				return
			}
			if claims.Iss != secrets.TerminalID {
				writeError(w, http.StatusUnauthorized, CodeIssuerMismatch, errMsgIssuerBad)
				return
			}
			next(w, r)
			return
		}

		// Legacy path — X-Terminal-Token. Only reached when there is no
		// Authorization: Bearer header at all.
		provided := r.Header.Get("X-Terminal-Token")
		if provided != "" &&
			subtle.ConstantTimeCompare([]byte(provided), []byte(secrets.TerminalToken)) == 1 {
			next(w, r)
			return
		}

		// Neither a valid JWT nor a valid X-Terminal-Token.
		writeError(w, http.StatusUnauthorized, CodeUnauthenticated, errMsgUnauthenticated)
	}
}

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
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Terminal-Token")
		}
		if r.Method == http.MethodOptions {
			// Preflight (or any OPTIONS) — never reach the inner handler.
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
