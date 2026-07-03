// cmd/admin/backfill_asset_embeddings.go — asset embedding backfill command (Task 5, July 2026)
//
// One-shot backfill for media_assets rows that are missing one or more
// embedding channels (text, transcript, visual, audio). Uses the canonical
// ClipIndexerService.IndexClip path so every channel is generated and
// persisted through the same code path as the normal indexing flow.
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
//   - Idempotent: IndexClip is idempotent — already-embedded assets are
//     skipped by the fast-path check, so re-running is safe.
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
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
)

// ── Types ─────────────────────────────────────────────────────────────

// backfillEmbeddingsDeps holds parsed CLI flags.
type backfillEmbeddingsDeps struct {
	Apply       bool
	DryRun      bool
	JSON        bool
	OnlyMissing bool
	Limit       int
	Progress    int // progress-report + checkpoint flush interval
	Source      string
	Checkpoint  string // path to checkpoint JSON file
	Resume      bool   // resume from checkpoint
	RetryFailed bool   // retry previously-failed assets
}

// backfillEmbeddingsReport is the JSON-serialisable output.
type backfillEmbeddingsReport struct {
	Mode              string   `json:"mode"`
	Source            string   `json:"source,omitempty"`
	Limit             int      `json:"limit,omitempty"`
	TotalCandidates   int      `json:"total_candidates"`
	MissingText       int      `json:"missing_text"`
	MissingTranscript int      `json:"missing_transcript"`
	MissingVisual     int      `json:"missing_visual"`
	MissingAudio      int      `json:"missing_audio"`
	AnyMissing        int      `json:"any_missing"`
	AlreadyComplete   int      `json:"already_complete"`
	Processed         int      `json:"processed"`
	Succeeded         int      `json:"succeeded"`
	Failed            int      `json:"failed"`
	Skipped           int      `json:"skipped"`
	FailedIDs         []string `json:"failed_ids,omitempty"`
	Errors            []string `json:"errors,omitempty"`
	Checkpoint        string   `json:"checkpoint,omitempty"`
	DurationMs        int64    `json:"duration_ms"`
}

// embeddingCheckpoint is the on-disk resume state.
type embeddingCheckpoint struct {
	JobID           string   `json:"job_id"`
	Source          string   `json:"source,omitempty"`
	LastProcessedID string   `json:"last_processed_id"`
	ProcessedCount  int      `json:"processed_count"`
	SucceededCount  int      `json:"succeeded_count"`
	FailedCount     int      `json:"failed_count"`
	FailedIDs       []string `json:"failed_ids"`
	Status          string   `json:"status"` // running | completed | failed
	StartedAt       string   `json:"started_at"`
	UpdatedAt       string   `json:"updated_at"`
}

// assetEmbeddingStatus holds per-asset embedding state for query+report.
type assetEmbeddingStatus struct {
	ID            string
	Source        string
	Name          string
	MediaType     string
	HasText       bool
	HasTranscript bool
	HasVisual     bool
	HasAudio      bool
	LocalPath     string
}

// ── Entry point ───────────────────────────────────────────────────────

func runBackfillAssetEmbeddings(args []string) error {
	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	deps, err := parseBackfillEmbeddingsArgs(args)
	if err != nil {
		return err
	}

	ctx := cmdContext()

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
	root, _, rootCleanup, err := app.InitComposition(cfg, log)
	if err != nil {
		return fmt.Errorf("init composition: %w", err)
	}
	defer rootCleanup()

	if root.Process.ClipIndexerService == nil {
		return fmt.Errorf("clip indexer service is not initialized or configured")
	}
	if root.DB == nil || root.DB.DB == nil {
		return fmt.Errorf("database not initialized in composition root")
	}

	report, cp, err := backfillAssetEmbeddings(ctx, root.DB.DB, root.Process.ClipIndexerService, deps, log)
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

func parseBackfillEmbeddingsArgs(args []string) (backfillEmbeddingsDeps, error) {
	deps := backfillEmbeddingsDeps{
		Progress:    50,  // log + checkpoint flush every 50 assets
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
			n, err := parsePositiveFlag(a, "--limit")
			if err != nil {
				return deps, err
			}
			deps.Limit = n
		case strings.HasPrefix(a, "--progress="):
			n, err := parsePositiveFlag(a, "--progress")
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

// ── Core logic ────────────────────────────────────────────────────────

// backfillAssetEmbeddings is the pure, testable core. It returns a report
// and a checkpoint suitable for serialisation.
func backfillAssetEmbeddings(
	ctx context.Context,
	db *sql.DB,
	indexer clipIndexer,
	deps backfillEmbeddingsDeps,
	log *zap.Logger,
) (backfillEmbeddingsReport, *embeddingCheckpoint, error) {
	start := time.Now()
	report := backfillEmbeddingsReport{
		Mode:   "dry-run",
		Source: deps.Source,
		Limit:  deps.Limit,
	}
	if deps.Apply {
		report.Mode = "apply"
	}

	// ── Load or init checkpoint ─────────────────────────────────────
	var cp *embeddingCheckpoint
	if deps.Checkpoint != "" {
		loaded, err := loadCheckpoint(deps.Checkpoint)
		if err != nil && !os.IsNotExist(err) {
			return report, nil, fmt.Errorf("load checkpoint %q: %w", deps.Checkpoint, err)
		}
		if loaded != nil && deps.Resume {
			cp = loaded
			log.Info("resuming from checkpoint",
				zap.String("job_id", cp.JobID),
				zap.String("last_processed_id", cp.LastProcessedID),
				zap.Int("processed_count", cp.ProcessedCount),
				zap.Int("succeeded_count", cp.SucceededCount),
				zap.Int("failed_count", cp.FailedCount))
		} else if deps.RetryFailed && loaded != nil {
			cp = loaded
			log.Info("retrying failed assets from checkpoint",
				zap.String("job_id", cp.JobID),
				zap.Int("failed_count", len(cp.FailedIDs)))
		} else {
			cp = &embeddingCheckpoint{
				JobID:     fmt.Sprintf("backfill-emb-%s", uuid.NewString()[:8]),
				Source:    deps.Source,
				Status:    "running",
				StartedAt: time.Now().UTC().Format(time.RFC3339Nano),
			}
		}
	}

	// ── Determine candidates ────────────────────────────────────────
	var candidates []assetEmbeddingStatus
	var err error
	if deps.RetryFailed && cp != nil && len(cp.FailedIDs) > 0 {
		// Retry mode: only process previously-failed assets.
		candidates, err = fetchFailedCandidates(ctx, db, cp.FailedIDs)
	} else {
		candidates, err = fetchEmbeddingCandidates(ctx, db, deps, cp)
	}
	if err != nil {
		return report, cp, fmt.Errorf("fetch candidates: %w", err)
	}

	if len(candidates) == 0 {
		report.DurationMs = time.Since(start).Milliseconds()
		if cp != nil {
			cp.Status = "completed"
			cp.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		}
		log.Info("no embedding candidates found")
		return report, cp, nil
	}

	// ── Count missing channels ──────────────────────────────────────
	report.TotalCandidates = len(candidates)
	for _, c := range candidates {
		hasAll := true
		if !c.HasText {
			report.MissingText++
			hasAll = false
		}
		if !c.HasTranscript {
			report.MissingTranscript++
			hasAll = false
		}
		if !c.HasVisual {
			report.MissingVisual++
			hasAll = false
		}
		if !c.HasAudio {
			report.MissingAudio++
			hasAll = false
		}
		if hasAll {
			report.AlreadyComplete++
		} else {
			report.AnyMissing++
		}
	}

	if !deps.Apply {
		report.DurationMs = time.Since(start).Milliseconds()
		if cp != nil {
			cp.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		}
		return report, cp, nil
	}

	// ── Apply: process candidates ───────────────────────────────────
	// At-least-once checkpointing: cp.LastProcessedID is updated in
	// memory on every success, but flushed to disk only every --progress
	// assets. A crash between flushes causes up to --progress assets to
	// be re-processed on resume. This is safe because IndexClip is
	// idempotent — the fast-path check skips already-embedded assets.
	report.Checkpoint = deps.Checkpoint
	for i, c := range candidates {
		select {
		case <-ctx.Done():
			report.Errors = append(report.Errors, "cancelled by signal")
			if cp != nil {
				cp.Status = "failed"
				cp.LastProcessedID = c.ID
			}
			report.DurationMs = time.Since(start).Milliseconds()
			return report, cp, ctx.Err()
		default:
		}

		// Skip already-complete assets in --only-missing mode.
		if deps.OnlyMissing && c.HasText && c.HasTranscript && c.HasVisual && c.HasAudio {
			report.Skipped++
			continue
		}

		report.Processed++

		if (i+1)%deps.Progress == 0 || i+1 == len(candidates) {
			log.Info("backfill progress",
				zap.Int("processed", report.Processed),
				zap.Int("succeeded", report.Succeeded),
				zap.Int("failed", report.Failed),
				zap.Int("remaining", len(candidates)-i-1))
		}

		idxStart := time.Now()
		err := indexer.IndexClip(ctx, c.ID)
		if err != nil {
			report.Failed++
			if cp != nil {
				cp.FailedCount++
				cp.FailedIDs = append(cp.FailedIDs, c.ID)
			}
			log.Warn("IndexClip failed",
				zap.String("asset_id", c.ID),
				zap.String("source", c.Source),
				zap.Error(err))
			report.Errors = append(report.Errors,
				fmt.Sprintf("%s: %v", c.ID, err))
			continue
		}

		report.Succeeded++
		log.Debug("IndexClip succeeded",
			zap.String("asset_id", c.ID),
			zap.Duration("elapsed", time.Since(idxStart)))

		// Update in-memory checkpoint after each success.
		if cp != nil {
			cp.LastProcessedID = c.ID
			cp.ProcessedCount = report.Processed
			cp.SucceededCount = report.Succeeded
			cp.FailedCount = report.Failed
			cp.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		}

		// Periodic checkpoint flush to disk.
		// At-least-once delivery: crash between flushes → re-process
		// up to --progress assets on resume. Safe because IndexClip
		// is idempotent (fast-path check on already-embedded assets).
		if cp != nil && deps.Checkpoint != "" && (i+1)%deps.Progress == 0 {
			if b, err := json.MarshalIndent(cp, "", "  "); err == nil {
				_ = os.WriteFile(deps.Checkpoint, b, 0o644)
			}
		}
	}

	report.DurationMs = time.Since(start).Milliseconds()
	if cp != nil {
		report.FailedIDs = cp.FailedIDs
		if report.Failed == 0 {
			cp.Status = "completed"
		} else {
			cp.Status = "failed"
		}
		cp.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}

	return report, cp, nil
}

// ── Candidate queries ─────────────────────────────────────────────────

// fetchEmbeddingCandidates queries media_assets for assets that need
// embedding backfill. In --only-missing mode, only returns assets with
// at least one empty embedding column.
func fetchEmbeddingCandidates(
	ctx context.Context,
	db *sql.DB,
	deps backfillEmbeddingsDeps,
	cp *embeddingCheckpoint,
) ([]assetEmbeddingStatus, error) {
	query := `
		SELECT id, COALESCE(source, ''), COALESCE(name, ''), COALESCE(media_type, ''),
		       COALESCE(local_path, ''),
		       CASE WHEN embedding_json IS NOT NULL AND embedding_json != '' AND embedding_json != '[]' AND embedding_json != '{}' THEN 1 ELSE 0 END,
		       CASE WHEN transcript_embedding IS NOT NULL AND transcript_embedding != '' AND transcript_embedding != '[]' AND transcript_embedding != '{}' THEN 1 ELSE 0 END,
		       CASE WHEN visual_embedding IS NOT NULL AND visual_embedding != '' AND visual_embedding != '[]' AND visual_embedding != '{}' THEN 1 ELSE 0 END,
		       CASE WHEN audio_embedding IS NOT NULL AND audio_embedding != '' AND audio_embedding != '[]' AND audio_embedding != '{}' THEN 1 ELSE 0 END
		FROM media_assets
		WHERE media_type != 'folder'
		  AND (deleted_at IS NULL OR deleted_at = '')`

	var queryArgs []any

	// Resume: start after last processed ID.
	if cp != nil && cp.LastProcessedID != "" && deps.Resume {
		query += ` AND id > ?`
		queryArgs = append(queryArgs, cp.LastProcessedID)
	}

	// Source filter.
	if deps.Source != "" {
		query += ` AND source = ?`
		queryArgs = append(queryArgs, deps.Source)
	}

	query += ` ORDER BY id ASC`

	if deps.Limit > 0 {
		query += ` LIMIT ?`
		queryArgs = append(queryArgs, deps.Limit)
	}

	rows, err := db.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("query embedding candidates: %w", err)
	}
	defer rows.Close()

	var out []assetEmbeddingStatus
	for rows.Next() {
		var a assetEmbeddingStatus
		var hasText, hasTranscript, hasVisual, hasAudio int
		if err := rows.Scan(&a.ID, &a.Source, &a.Name, &a.MediaType,
			&a.LocalPath, &hasText, &hasTranscript, &hasVisual, &hasAudio); err != nil {
			return nil, fmt.Errorf("scan candidate: %w", err)
		}
		a.HasText = hasText == 1
		a.HasTranscript = hasTranscript == 1
		a.HasVisual = hasVisual == 1
		a.HasAudio = hasAudio == 1

		if deps.OnlyMissing && a.HasText && a.HasTranscript && a.HasVisual && a.HasAudio {
			continue // fully embedded, skip in --only-missing mode
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// fetchFailedCandidates returns candidate rows only for the given asset IDs.
func fetchFailedCandidates(ctx context.Context, db *sql.DB, ids []string) ([]assetEmbeddingStatus, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf(`
		SELECT id, COALESCE(source, ''), COALESCE(name, ''), COALESCE(media_type, ''),
		       COALESCE(local_path, ''),
		       CASE WHEN embedding_json IS NOT NULL AND embedding_json != '' AND embedding_json != '[]' AND embedding_json != '{}' THEN 1 ELSE 0 END,
		       CASE WHEN transcript_embedding IS NOT NULL AND transcript_embedding != '' AND transcript_embedding != '[]' AND transcript_embedding != '{}' THEN 1 ELSE 0 END,
		       CASE WHEN visual_embedding IS NOT NULL AND visual_embedding != '' AND visual_embedding != '[]' AND visual_embedding != '{}' THEN 1 ELSE 0 END,
		       CASE WHEN audio_embedding IS NOT NULL AND audio_embedding != '' AND audio_embedding != '[]' AND audio_embedding != '{}' THEN 1 ELSE 0 END
		FROM media_assets
		WHERE id IN (%s)
		  AND media_type != 'folder'
		  AND (deleted_at IS NULL OR deleted_at = '')
		ORDER BY id ASC`, strings.Join(placeholders, ","))

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query failed candidates: %w", err)
	}
	defer rows.Close()

	var out []assetEmbeddingStatus
	for rows.Next() {
		var a assetEmbeddingStatus
		var hasText, hasTranscript, hasVisual, hasAudio int
		if err := rows.Scan(&a.ID, &a.Source, &a.Name, &a.MediaType,
			&a.LocalPath, &hasText, &hasTranscript, &hasVisual, &hasAudio); err != nil {
			return nil, fmt.Errorf("scan failed candidate: %w", err)
		}
		a.HasText = hasText == 1
		a.HasTranscript = hasTranscript == 1
		a.HasVisual = hasVisual == 1
		a.HasAudio = hasAudio == 1
		out = append(out, a)
	}
	return out, rows.Err()
}

// ── Checkpoint I/O ────────────────────────────────────────────────────

func loadCheckpoint(path string) (*embeddingCheckpoint, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cp embeddingCheckpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return nil, fmt.Errorf("parse checkpoint: %w", err)
	}
	if cp.JobID == "" {
		return nil, fmt.Errorf("checkpoint %q is missing job_id", path)
	}
	return &cp, nil
}

// ── Interface for testability ─────────────────────────────────────────

// clipIndexer is the subset of clipindexer.Service used by the backfill.
// Production wired via root.Process.ClipIndexerService.
type clipIndexer interface {
	IndexClip(ctx context.Context, clipID string) error
}
