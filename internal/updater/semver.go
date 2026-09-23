package updater

import (
	"fmt"
	"strconv"
	"strings"
)

// CompareVersions compares two release versions segment by segment,
// numerically: "0.3.10" > "0.3.9" (a string compare would say the
// opposite). Returns -1, 0 or +1 for a < b, a == b, a > b.
//
// Accepted shape: an optional leading "v", then one or more dot-separated
// non-negative integers. Missing trailing segments count as 0, so "1.2"
// == "1.2.0". Anything else — "dev", "0.0.0-manual", "1.2.x", "" — is an
// error: the updater never acts on a version it cannot order, and a
// pre-release tag is not something a shop should be moved onto.
func CompareVersions(a, b string) (int, error) {
	pa, err := parseVersion(a)
	if err != nil {
		return 0, err
	}
	pb, err := parseVersion(b)
	if err != nil {
		return 0, err
	}
	n := len(pa)
	if len(pb) > n {
		n = len(pb)
	}
	for i := 0; i < n; i++ {
		var x, y uint64
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		switch {
		case x < y:
			return -1, nil
		case x > y:
			return 1, nil
		}
	}
	return 0, nil
}

func parseVersion(v string) ([]uint64, error) {
	s := strings.TrimPrefix(strings.TrimSpace(v), "v")
	if s == "" {
		return nil, fmt.Errorf("updater: empty version %q", v)
	}
	parts := strings.Split(s, ".")
	out := make([]uint64, len(parts))
	for i, p := range parts {
		if p == "" {
			return nil, fmt.Errorf("updater: malformed version %q", v)
		}
		n, err := strconv.ParseUint(p, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("updater: non-numeric version segment %q in %q", p, v)
		}
		out[i] = n
	}
	return out, nil
}
