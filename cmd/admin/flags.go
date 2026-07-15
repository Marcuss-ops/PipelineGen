// cmd/admin/flags.go — command-line flags / signal-context plumbing
// for the admin CLI composition root.
//
// This is the home for shared command-line helpers used by every
// admin subcommand and by `main()` itself. Keep it thin: real
// subcommand flags belong in the per-subcommand <name>.go files; this
// file holds only the cross-cutting flags/context plumbing.
//
// PR-PKG-SIZE-CMD-ADMIN-1 (July 2026): cmdContext was extracted to
// cmd/admin/internal/cli (exported as CmdContext) so the
// reconcile/reindex/dr subcommands (now in cmd/admin/reconcile
// subpackage) can import it. The lowercase symbol cmdContext is
// retained here as a thin shim that delegates to the canonical
// cli.CmdContext helper — the 60 non-moved cmd/admin files do NOT
// need to change as part of this PR. Tracked follow-up:
// PR-PKG-SIZE-CMD-ADMIN-1-FOLLOWUP will migrate all call sites to
// cli.CmdContext directly and delete this file.

package main

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"
)

// cmdContext is a thin shim around cli.CmdContext. See file header.
func cmdContext() context.Context {
	return cli.CmdContext()
}
