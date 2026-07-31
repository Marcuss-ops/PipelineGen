// cmd/admin/main.go — admin CLI entry point
//
// Pure composition delegator: parse argv, route to dispatchSubcommand
// (in cmd/admin/subcommands.go), and surface the result to the
// operator. All subcommand implementations, the canonical command
// list, the dispatch switch, and the usage block live in
// cmd/admin/subcommands.go; the cmd-context helper lives in
// cmd/admin/flags.go.

package main

import (
	"errors"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	name := os.Args[1]
	args := os.Args[2:]

	err := dispatchSubcommand(name, args)
	if err != nil {
		if errors.Is(err, errUnknownCommand) {
			fmt.Printf("Unknown command: %s\n\n", name)
			printUsage()
		} else {
			fmt.Fprintf(os.Stderr, "Error running command %s: %v\n", name, err)
		}
		os.Exit(1)
	}
}
