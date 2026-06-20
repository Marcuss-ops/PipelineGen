package main

import (
	"fmt"
	"os"

	"go.uber.org/zap"
)

// runSyncOutros — invoked by `admin sync-outros`.
//
// TODO: port the original sync-outros logic. For now it logs intent and
// returns 0 so the build is green and any operator invocation gets a
// clear no-op rather than a runtime error.
//
// Historical reference: per AGENTS.md / SESSION_SUMMARY.md, June 2026
// cleanup removed several legacy admin commands without removing the
// pointer from main.go's command map. The state-machine rework left the
// sync-outros content unused.
func runSyncOutros(args []string) error {
	log, cleanup, err := productionLogger()
	if err != nil {
		return fmt.Errorf("init logger: %w", err)
	}
	defer cleanup()

	log.Warn("sync-outros invoked (stub — not yet reimplemented)",
		zap.Int("argc", len(args)),
	)
	fmt.Fprintln(os.Stderr, "sync-outros: stub — actual sync logic not yet reimplemented.")
	return nil
}

// runBackfillMissing — invoked by `admin backfill-missing`.
//
// TODO: re-port the original backfill-missing behaviour (was scanning
// artlist SQLite for clips missing derived fields and re-deriving them).
func runBackfillMissing(args []string) error {
	log, cleanup, err := productionLogger()
	if err != nil {
		return fmt.Errorf("init logger: %w", err)
	}
	defer cleanup()

	log.Warn("backfill-missing invoked (stub — not yet reimplemented)",
		zap.Int("argc", len(args)),
	)
	fmt.Fprintln(os.Stderr, "backfill-missing: stub — actual logic not yet reimplemented.")
	return nil
}

// runSummarizeBook — invoked by `admin summarize-book`. Historical
// command name kept for CLI parity; the real summarisation is offered
// elsewhere as an async HTTP endpoint.
func runSummarizeBook(args []string) error {
	log, cleanup, err := productionLogger()
	if err != nil {
		return fmt.Errorf("init logger: %w", err)
	}
	defer cleanup()

	log.Warn("summarize-book invoked (stub — use the async API instead)",
		zap.Strings("args", args),
	)
	fmt.Fprintln(os.Stderr, "summarize-book: stub — use POST /api/books/summarize and poll /api/jobs/<id>/full instead.")
	return nil
}

// runSeedChannels — invoked by `admin seed-channels`. The real seed
// lives in `scripts/seed_fixture/main.go` (see AGENTS.md); this CLI
// command is kept so existing dashboards / cron scripts that call it
// do not crash.
func runSeedChannels(args []string) error {
	log, cleanup, err := productionLogger()
	if err != nil {
		return fmt.Errorf("init logger: %w", err)
	}
	defer cleanup()

	log.Warn("seed-channels invoked (stub — use scripts/seed_fixture/main.go)",
		zap.Int("argc", len(args)),
	)
	fmt.Fprintln(os.Stderr, "seed-channels: stub — use scripts/seed_fixture/main.go for the canonical seed.")
	return nil
}
