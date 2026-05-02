package service

// Status returns a human-readable service state ("running", "stopped",
// "not installed", etc.). The Windows implementation queries SCM via
// x/sys/windows/svc/mgr; non-Windows builds return "unsupported on this
// platform".
func Status() (string, error) {
	return statusImpl()
}
