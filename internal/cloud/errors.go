package cloud

import (
	"errors"
	"fmt"
)

// Sentinel errors returned by the Client. Map onto the contract §5.1 codes
// plus a transport-level ErrNetwork. Callers should detect via errors.Is.
//
// Every cloud-side error returned by the Client is also a *CloudError,
// which exposes the original cloud-supplied French message via Message().
var (
	ErrInvalidRequest  = errors.New("cloud: invalid request")
	ErrInvalidCode     = errors.New("cloud: pairing code invalid or expired")
	ErrUnauthenticated = errors.New("cloud: terminal token invalid or revoked")
	ErrForbidden       = errors.New("cloud: forbidden")
	ErrNotFound        = errors.New("cloud: not found")
	ErrRateLimited     = errors.New("cloud: rate limited")
	ErrInternal        = errors.New("cloud: server error")
	ErrNetwork         = errors.New("cloud: network error")
)

// errorPayload mirrors the cloud's error envelope shape per contract §5.
type errorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// CloudError wraps a sentinel with the original cloud-supplied code and
// message. The CLI uses this to surface the cloud's French-language message
// directly to the operator, while still allowing programmatic dispatch via
// errors.Is(err, ErrInvalidCode) etc.
type CloudError struct {
	sentinel error
	code     string
	message  string
}

// Error implements error. Format: "<sentinel>: <message>" when message
// is set, else just the sentinel string.
func (e *CloudError) Error() string {
	if e.message != "" {
		return fmt.Sprintf("%s: %s", e.sentinel.Error(), e.message)
	}
	return e.sentinel.Error()
}

// Unwrap returns the sentinel so errors.Is works against the package vars.
func (e *CloudError) Unwrap() error { return e.sentinel }

// Code returns the machine-readable error code from the cloud envelope.
func (e *CloudError) Code() string { return e.code }

// Message returns the cloud-supplied human-readable message (French per
// contract §5).
func (e *CloudError) Message() string { return e.message }

// codeToSentinel maps contract §5.1 enum values to package sentinels.
var codeToSentinel = map[string]error{
	"INVALID_REQUEST":  ErrInvalidRequest,
	"INVALID_CODE":     ErrInvalidCode,
	"UNAUTHENTICATED":  ErrUnauthenticated,
	"FORBIDDEN":        ErrForbidden,
	"NOT_FOUND":        ErrNotFound,
	"RATE_LIMITED":     ErrRateLimited,
	"INTERNAL":         ErrInternal,
}

// mapError converts a decoded error envelope to a *CloudError wrapping
// the appropriate sentinel. Unknown codes fall back to ErrInternal so
// the caller still gets a typed error.
func mapError(p *errorPayload) error {
	sentinel, ok := codeToSentinel[p.Code]
	if !ok {
		sentinel = ErrInternal
	}
	return &CloudError{sentinel: sentinel, code: p.Code, message: p.Message}
}
