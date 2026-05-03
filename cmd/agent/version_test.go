package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestVersionFlag verifies that `agent --version` (and `-version`)
// prints the build-injected Version constant to stdout and exits 0.
//
// Uses the helper-process pattern: the test exec's the test binary
// itself with a sentinel env var that re-routes the entry point through
// main() (via TestRunMainAsHelper below), passing args after `--`.
func TestVersionFlag(t *testing.T) {
	tests := []struct {
		name string
		flag string
	}{
		{"double-dash", "--version"},
		{"single-dash", "-version"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0],
				"-test.run", "^TestRunMainAsHelper$",
				"-test.timeout", "10s",
				"--", tt.flag,
			)
			cmd.Env = append(os.Environ(), "AGENT_TEST_RUN_MAIN=1")
			var out bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &out
			if err := cmd.Run(); err != nil {
				t.Fatalf("subprocess exit non-zero: %v\noutput:\n%s", err, out.String())
			}
			if !strings.Contains(out.String(), Version) {
				t.Errorf("output does not contain Version %q:\n%s", Version, out.String())
			}
		})
	}
}

// TestRunMainAsHelper is the helper-process body. When invoked normally
// by `go test`, the env var is unset and the test does nothing. When
// invoked via TestVersionFlag's exec, the env var is set; the test
// rewrites os.Args to strip the test framework's args and calls the
// production main(), then exits with main()'s return path (which
// returns 0 on the --version path).
func TestRunMainAsHelper(t *testing.T) {
	if os.Getenv("AGENT_TEST_RUN_MAIN") != "1" {
		return
	}
	// Strip everything up to and including "--" — what's after is the
	// args we want main() to see.
	for i, a := range os.Args {
		if a == "--" {
			os.Args = append([]string{"agent"}, os.Args[i+1:]...)
			break
		}
	}
	main()
	os.Exit(0)
}
