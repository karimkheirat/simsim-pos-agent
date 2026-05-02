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
	"fmt"
	"os"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

const usageTemplate = `Simsim POS Agent CLI %s

Usage:
  agentctl pair --code <6-digit-code> [flags]
  agentctl unpair [flags]
  agentctl status [flags]

Common flags:
  --config string    Path to config.json
  --secrets string   Path to secrets file
`

func main() {
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
	fmt.Printf(usageTemplate, version)
}
