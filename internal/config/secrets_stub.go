//go:build !windows

// secrets_stub.go provides the non-Windows factory for SecretStore.
//
// Factory enforcement (by build tag): on Windows, NewSecretStore is
// defined in secrets_windows.go and unconditionally returns
// *DPAPISecretStore — JSONFileSecretStore is never returned by the
// factory in production. The JSON store is reachable on Windows only
// via NewJSONFileSecretStore (direct constructor) for cross-platform
// tests. Production agents always get machine-scope DPAPI encryption.
//
// JSONFileSecretStore itself lives in secrets.go (cross-platform) so
// the cross-platform JSON round-trip test can compile and run on any
// host, including the Windows production target.
package config

// NewSecretStore returns a JSON-backed SecretStore on non-Windows builds.
//
// **DEV-ONLY.** See JSONFileSecretStore (in secrets.go) for the in-line
// production warning. Production deployments are Windows-only and use
// the DPAPI-backed implementation in secrets_windows.go.
func NewSecretStore(path string) (SecretStore, error) {
	return NewJSONFileSecretStore(path), nil
}
