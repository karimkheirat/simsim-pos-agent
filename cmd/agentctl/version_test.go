package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestVersionFlag — mirror of cmd/agent's version test. Verifies that
// `agentctl --version` prints the build-injected Version and exits 0.
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
			cmd.Env = append(os.Environ(), "AGENTCTL_TEST_RUN_MAIN=1")
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

// TestRunMainAsHelper is the helper-process body. Unset env → no-op.
// Set env → rewrite os.Args and call main().
func TestRunMainAsHelper(t *testing.T) {
	if os.Getenv("AGENTCTL_TEST_RUN_MAIN") != "1" {
		return
	}
	for i, a := range os.Args {
		if a == "--" {
			os.Args = append([]string{"agentctl"}, os.Args[i+1:]...)
			break
		}
	}
	main()
	os.Exit(0)
}
