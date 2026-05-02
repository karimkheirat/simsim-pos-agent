package util

import (
	"regexp"
	"testing"
)

// v4Re matches the canonical RFC 4122 v4 UUID form: lowercase hex, with
// the version nibble fixed at 4 and the variant nibble in {8,9,a,b}.
var v4Re = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestNewUUIDv4_Format(t *testing.T) {
	for i := 0; i < 100; i++ {
		id, err := NewUUIDv4()
		if err != nil {
			t.Fatalf("call %d: unexpected err: %v", i, err)
		}
		if !v4Re.MatchString(id) {
			t.Errorf("UUID %q does not match v4 format", id)
		}
	}
}

func TestNewUUIDv4_Uniqueness(t *testing.T) {
	const N = 1000
	seen := make(map[string]struct{}, N)
	for i := 0; i < N; i++ {
		id, err := NewUUIDv4()
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate UUID at call %d: %s", i, id)
		}
		seen[id] = struct{}{}
	}
}

// TestNewUUIDv4_StructuralReturnContract asserts the (string, error)
// shape on the success path. The error path is unreachable in practice
// (would require crypto/rand to fail) and is not exercised here.
func TestNewUUIDv4_StructuralReturnContract(t *testing.T) {
	id, err := NewUUIDv4()
	if err != nil {
		t.Fatalf("err = %v, want nil on healthy system", err)
	}
	if id == "" {
		t.Error("id is empty string")
	}
}
