// cmd/admin/backfill_asset_embeddings.go — asset embedding backfill command (Task 5, July 2026)
//
// One-shot backfill for media_assets rows that are missing one or more
// embedding channels (text, transcript, visual, audio). Enqueues an
// asset.index.requested outbox event (force=true) for each candidate so
// the worker indexes it through the canonical outbox path.
//
// Features:
//   - Dry-run (default): counts candidates and missing channels.
//   - Apply: generates and persists missing embeddings.
//   - --only-missing: skip assets that have all four embeddings (default).
//     Use --all to process every candidate including already-complete ones.
//   - --source: filter by asset source (youtube, artlist, stock, etc.).
//   - Checkpoint/resume: save progress to a JSON file; resume from it.
//     Checkpoint is flushed every --progress assets. Between flushes,
//     a crash loses up to --progress assets of progress — safe because
//     IndexClip is idempotent (fast-path check skips already-embedded
//     assets), so re-processing on resume is a no-op.
//   - Retry failed: re-process assets that failed in a previous run.
//   - Idempotent: the outbox event_key is deterministic per
//     (assetID, schemaVersion, contentHash), so re-running is safe.
//   - JSON output: machine-readable report.
//
// Usage:
//
//	go run ./cmd/admin backfill-asset-embeddings                           # dry-run
//	go run ./cmd/admin backfill-asset-embeddings --apply                   # generate + persist
//	go run ./cmd/admin backfill-asset-embeddings --apply --source=youtube  # filter by source
//	go run ./cmd/admin backfill-asset-embeddings --apply --limit=100       # cap
//	go run ./cmd/admin backfill-asset-embeddings --apply --all             # process even already-complete
//	go run ./cmd/admin backfill-asset-embeddings --apply --progress=100    # log + checkpoint every 100
//	go run ./cmd/admin backfill-asset-embeddings --apply --checkpoint=./bf.json  # save progress
//	go run ./cmd/admin backfill-asset-embeddings --resume --checkpoint=./bf.json # resume
//	go run ./cmd/admin backfill-asset-embeddings --retry-failed --checkpoint=./bf.json # retry failures
//	go run ./cmd/admin backfill-asset-embeddings --json                    # machine-readable
//
// Layout (Commit F, August 2026): the reusable backfill engine (batch
// processing, retry recovery, checkpointing, reporting) lives in
// internal/application/indexing/backfill. This file keeps the CLI
// surface — entry point, arg parsing, output formatting, composition-root
// wiring — and supplies the SQL-backed candidate fetcher.
//
// The 2 SQL-pure candidate-fetch helpers (fetchEmbeddingCandidates +
// fetchFailedCandidates) live in the sibling file
// backfill_asset_embeddings_db.go (same package main). The sibling stays
// in cmd/admin (NOT internal/infrastructure) per the Commit E user
// constraint: "NON spostare nulla in internal/infrastructure (creerebbe
// interfacce morte)". These are one-shot-CLI-only queries; promoting
// them to infrastructure would force a typed-port interface with no
// second consumer. The reusable core moved to the application layer
// because it is SQL-free and port-driven (Fetcher + Enqueuer).
package backfill

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/indexing/backfill"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// ── Entry point ───────────────────────────────────────────────────────

func RunBackfillAssetEmbeddings(args []string) error {
	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	deps, err := parseBackfillEmbeddingsArgs(args)
	if err != nil {
		return err
	}

	ctx := cli.CmdContext()

	log.Info("backfill-asset-embeddings starting",
		zap.Bool("apply", deps.Apply),
		zap.Bool("dry_run", deps.DryRun || !deps.Apply),
		zap.Int("limit", deps.Limit),
		zap.Int("progress", deps.Progress),
		zap.String("source", deps.Source),
		zap.String("checkpoint", deps.Checkpoint),
		zap.Bool("resume", deps.Resume),
		zap.Bool("retry_failed", deps.RetryFailed))

	// Init the full composition root so we have canonical services.
	root, _, rootCleanup, err := wiring.InitComposition(cfg, log)
	if err != nil {
		return fmt.Errorf("init composition: %w", err)
	}
	defer rootCleanup()

	if root.DB == nil || root.DB.DB == nil {
		return fmt.Errorf("database not initialized in composition root")
	}

	adapter := outbox.NewRepairAdapter(root.DB.DB, outboxevents.NewRepository(root.DB.DB), outboxevents.ReindexEnvelopeV1Schema)

	// SQL-backed candidate source: retry mode only re-processes
	// previously-failed assets; otherwise the forward resume-anchored query.
	fetch := func(ctx context.Context, d indexing.Deps, cp *indexing.Checkpoint) ([]indexing.Candidate, error) {
		if d.RetryFailed && cp != nil && len(cp.FailedIDs) > 0 {
			return fetchFailedCandidates(ctx, root.DB.DB, cp.FailedIDs)
		}
		return fetchEmbeddingCandidates(ctx, root.DB.DB, d, cp)
	}

	report, cp, err := indexing.Run(ctx, deps, fetch, adapter, log)
	if err != nil {
		return err
	}

	// Write checkpoint on exit if path provided.
	if deps.Checkpoint != "" && cp != nil {
		cp.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if b, err := json.MarshalIndent(cp, "", "  "); err == nil {
			_ = os.WriteFile(deps.Checkpoint, b, 0o644)
		}
	}

	if deps.JSON {
		b, _ := json.MarshalIndent(report, "", "  ")
		fmt.Println(string(b))
		return nil
	}

	if !deps.Apply {
		fmt.Println("=== Embedding Backfill DRY-RUN ===")
		fmt.Printf("  Candidates:        %d\n", report.TotalCandidates)
		fmt.Printf("  Missing text:      %d\n", report.MissingText)
		fmt.Printf("  Missing transcript:%d\n", report.MissingTranscript)
		fmt.Printf("  Missing visual:    %d\n", report.MissingVisual)
		fmt.Printf("  Missing audio:     %d\n", report.MissingAudio)
		fmt.Printf("  Any missing:       %d\n", report.AnyMissing)
		fmt.Printf("  Already complete:  %d\n", report.AlreadyComplete)
		fmt.Println("\nRe-run with --apply to generate embeddings.")
		return nil
	}

	fmt.Println("=== Embedding Backfill Complete ===")
	fmt.Printf("  Processed:   %d\n", report.Processed)
	fmt.Printf("  Succeeded:   %d\n", report.Succeeded)
	fmt.Printf("  Failed:      %d\n", report.Failed)
	fmt.Printf("  Skipped:     %d\n", report.Skipped)
	fmt.Printf("  Duration:    %dms\n", report.DurationMs)
	if report.Checkpoint != "" {
		fmt.Printf("  Checkpoint:  %s\n", report.Checkpoint)
	}
	if len(report.FailedIDs) > 0 {
		fmt.Printf("  Failed IDs (%d):\n", len(report.FailedIDs))
		for _, id := range report.FailedIDs {
			fmt.Printf("    - %s\n", id)
		}
		fmt.Println("  Re-run with --retry-failed --checkpoint=<path> to retry.")
	}
	return nil
}

// ── Arg parsing ───────────────────────────────────────────────────────

func parseBackfillEmbeddingsArgs(args []string) (indexing.Deps, error) {
	deps := indexing.Deps{
		Progress:    50,   // log + checkpoint flush every 50 assets
		OnlyMissing: true, // default: skip already-complete assets
	}
	for _, a := range args {
		a = strings.TrimSpace(a)
		switch {
		case a == "--apply":
			deps.Apply = true
		case a == "--dry-run":
			deps.DryRun = true
		case a == "--json":
			deps.JSON = true
		case a == "--only-missing":
			deps.OnlyMissing = true
		case a == "--all":
			deps.OnlyMissing = false
		case a == "--resume":
			deps.Resume = true
		case a == "--retry-failed":
			deps.RetryFailed = true
		case strings.HasPrefix(a, "--source="):
			deps.Source = strings.TrimPrefix(a, "--source=")
		case strings.HasPrefix(a, "--limit="):
			n, err := cli.ParsePositiveFlag(a, "--limit")
			if err != nil {
				return deps, err
			}
			deps.Limit = n
		case strings.HasPrefix(a, "--progress="):
			n, err := cli.ParsePositiveFlag(a, "--progress")
			if err != nil {
				return deps, err
			}
			deps.Progress = n
		case strings.HasPrefix(a, "--checkpoint="):
			deps.Checkpoint = strings.TrimPrefix(a, "--checkpoint=")
		default:
			if strings.HasPrefix(a, "-") {
				return deps, fmt.Errorf("unknown flag: %s", a)
			}
		}
	}
	if deps.Apply && deps.DryRun {
		return deps, fmt.Errorf("--apply and --dry-run are mutually exclusive")
	}
	if deps.Resume && deps.Checkpoint == "" {
		return deps, fmt.Errorf("--resume requires --checkpoint=<path>")
	}
	if deps.RetryFailed && deps.Checkpoint == "" {
		return deps, fmt.Errorf("--retry-failed requires --checkpoint=<path>")
	}
	if deps.Progress <= 0 {
		deps.Progress = 50
	}
	return deps, nil
}
