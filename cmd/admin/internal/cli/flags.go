// cmd/admin/internal/cli/flags.go — command-line flags / signal-context
// plumbing for the admin CLI composition root.
//
// Canonical home for the cross-cutting helpers (CmdContext, flag parsers)
// used by every admin subcommand. Keep it thin: real subcommand flags
// belong in the per-subcommand <name>.go files inside cmd/admin/* or
// cmd/admin/reconcile/*; this file holds only the cross-cutting
// context + flag-parsing plumbing.
//
// Created in PR-PKG-SIZE-CMD-ADMIN-1 (July 2026) to satisfy the
// pkg_size archcheck rule (max_files_per_package=65) by moving
// reconcile/reindex/dr subcommands into cmd/admin/reconcile/. See
// architecture/issues.yaml PR-PKG-SIZE-CMD-ADMIN-1 for the migration
// plan + follow-up tracking.
package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
)

// CmdContext returns a context that is cancelled on SIGINT / SIGTERM.
// AGENTS.md §7 post-write save ctx — admin CLI composition root; same
// rationale as cmd/worker/main.go — admin is a one-shot binary whose
// lifetime is bounded by the operator invocation, so we synthesise
// the cancellation context here rather than relying on a parent
// request ctx.
//
// Canonical replacement for the former `cmdContext()` in
// cmd/admin/flags.go (PR-PKG-SIZE-CMD-ADMIN-1 extraction). The
// lowercase symbol was renamed to CmdContext to satisfy Go's
// exported-symbol rule for cross-package import.
func CmdContext() context.Context {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ctx.Done()
		cancel()
	}()
	return ctx
}

// ParsePositiveFlag parses a `--flag=N` CLI arg into a non-negative int.
// Returns an error on: malformed integer, or negative value. The
// flagName argument is used in the error message for operator
// diagnostics.
//
// Canonical replacement for the former `parsePositiveFlag` in
// cmd/admin/backfill_media_assets_search_terms.go
// (PR-PKG-SIZE-CMD-ADMIN-1 extraction). The lowercase symbol was
// renamed to ParsePositiveFlag to satisfy Go's exported-symbol rule
// for cross-package import. Both call sites (backfill in
// cmd/admin/backfill_media_assets_search_terms.go and reconcile in
// cmd/admin/reconcile/reconcile_qdrant.go) now route through this
// helper.
func ParsePositiveFlag(arg, flagName string) (int, error) {
	raw := strings.TrimPrefix(arg, flagName+"=")
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid %s=%q: must be a non-negative integer", flagName, raw)
	}
	if n < 0 {
		return 0, fmt.Errorf("invalid %s=%q: must be non-negative", flagName, raw)
	}
	return n, nil
}
