// Command agentctl is the operator-facing CLI for the Simsim POS Agent.
// It pairs/unpairs the agent with the Simsim cloud and reports status.
//
// Reads the same config.json as cmd/agent and writes to the same secrets
// file. The two binaries are intentionally separate: agentctl runs
// interactively when an operator sits in front of the cashier PC; agent
// runs in the background as a Windows service.
//
// M2 sub-task A3: thin CLI shell. Real orchestration lives inline here
// for now and will be extracted into internal/pairing in A4 for unit
// testability.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

// Version is the build-injected version string. The "dev" default
// applies to local `go build` invocations; release builds set it via
//
//   go build -ldflags "-X main.Version=0.3.0" ...
//
// Mirrors cmd/agent's Version — both binaries report the same version
// to the cloud (heartbeat, pair) and to the operator (--version flag).
var Version = "dev"

const usageTemplate = `Simsim POS Agent CLI %s

Usage:
  agentctl pair --code <6-digit-code> [flags]
  agentctl unpair [flags]
  agentctl status [flags]
  agentctl --version

Common flags:
  --config string    Path to config.json
  --secrets string   Path to secrets file
`

func main() {
	// Top-level --version, parsed via the standard flag package against
	// a private FlagSet so subcommand dispatch below is unaffected.
	if handleVersionFlag(os.Args[1:]) {
		return
	}

	if len(os.Args) < 2 {
		printUsage()
		return
	}
	var exitCode int
	switch os.Args[1] {
	case "pair":
		exitCode = runPair(os.Args[2:])
	case "unpair":
		exitCode = runUnpair(os.Args[2:])
	case "status":
		exitCode = runStatus(os.Args[2:])
	default:
		// Friendly fallback for unknown subcommands and "agent --help" /
		// "agent -h" patterns; mirrors cmd/agent's no-args behavior.
		printUsage()
		return
	}
	os.Exit(exitCode)
}

func printUsage() {
	fmt.Printf(usageTemplate, Version)
}

// handleVersionFlag inspects args for a top-level --version (or -version)
// flag; if present, prints Version to stdout and returns true to signal
// the caller to exit cleanly. Same shape as cmd/agent's helper.
func handleVersionFlag(args []string) bool {
	fs := flag.NewFlagSet("agentctl-top", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	showVersion := fs.Bool("version", false, "print version and exit")
	_ = fs.Parse(args)
	if *showVersion {
		fmt.Println(Version)
		return true
	}
	return false
}
