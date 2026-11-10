//go:build !windows

package printer

// ListInstalled has nothing to ask outside Windows: the agent only drives
// the Windows spooler (and files, in development).
func ListInstalled() ([]Installed, error) {
	return []Installed{}, nil
}
