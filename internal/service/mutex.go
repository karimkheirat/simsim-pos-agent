package service

import "errors"

// MutexName is the literal Windows kernel object name for the
// single-instance mutex. The "Global\" prefix makes it visible across
// terminal-server sessions, so a per-user `agent run` will see a
// service-account-held mutex and bail out cleanly. Spec §5.1.
const MutexName = `Global\SimsimPOSAgent`

// ErrAlreadyRunning indicates AcquireSingleInstance saw the mutex held
// by another process. Callers should log and exit 0 — this is not a
// failure.
var ErrAlreadyRunning = errors.New("service: another instance is already running")
