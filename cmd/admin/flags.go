// cmd/admin/flags.go — command-line flags / signal-context plumbing
// for the admin CLI composition root.
//
// This is the home for shared command-line helpers used by every
// admin subcommand and by `main()` itself. Keep it thin: real
// subcommand flags belong in the per-subcommand <name>.go files; this
// file holds only the cross-cutting flags/context plumbing.

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// cmdContext returns a context that is cancelled on SIGINT / SIGTERM.
// AGENTS.md §7 post-write save ctx — admin CLI composition root; same
// rationale as cmd/worker/main.go — admin is a one-shot binary whose
// lifetime is bounded by the operator invocation, so we synthesise
// the cancellation context here rather than relying on a parent
// request ctx.
func cmdContext() context.Context {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ctx.Done()
		cancel()
	}()
	return ctx
}
