// Package backfill — reusable core of the asset-embedding backfill
// (extracted from cmd/admin/backfill_asset_embeddings.go, Commit F,
// August 2026).
//
// The package owns the pure, testable backfill engine: candidate
// processing in batches, at-least-once checkpoint/resume, --retry-failed
// recovery, missing-channel reporting, and the JSON-serialisable report.
// It is intentionally SQL-free:
//
//   - The candidate set is injected via the Fetcher callback. The CLI
//     supplies the SQL-backed implementation (candidate queries live in
//     cmd/admin/backfill_asset_embeddings_db.go per the Commit E user
//     constraint — one-shot CLI queries stay in the command, they do not
//     become dead infrastructure interfaces).
//   - The enqueue side goes through the single typed port Enqueuer
//     (production: cmd/admin/internal/outbox.RepairAdapter).
//
// Reuse contract: any future backfill surface (e.g. a media-assets
// search-terms backfill or a stock-embedding backfill) can reuse Run by
// supplying its own Fetcher + Enqueuer; the checkpoint/report shapes are
// generic enough for id-based row backfills.
package indexing

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Deps carries the parsed run configuration. Presentation-only flags
// (DryRun, JSON) are consumed by the CLI entry point; the engine reads
// Apply/OnlyMissing/Limit/Progress/Source/Checkpoint/Resume/RetryFailed.
type Deps struct {
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

// Report is the JSON-serialisable output.
type Report struct {
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

// Checkpoint is the on-disk resume state.
type Checkpoint struct {
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

// Candidate holds per-asset embedding state for query + report.
// ContentHash is the resolved file fingerprint (populated by the
// SQL-backed fetcher); Run falls back to "legacy_no_hash_<id>" when
// the asset carries no hash.
type Candidate struct {
	ID            string
	Source        string
	Name          string
	MediaType     string
	HasText       bool
	HasTranscript bool
	HasVisual     bool
	HasAudio      bool
	LocalPath     string
	ContentHash   string
}

// Enqueuer is the typed port for persisting the index-request event.
// Production is wired via cmd/admin/internal/outbox.RepairAdapter.
type Enqueuer interface {
	EnqueueReindex(ctx context.Context, assetID, contentHash string, force bool) error
}

// Fetcher returns the candidate set for a run. The CLI supplies the
// SQL-backed implementation; fetch is invoked AFTER checkpoint
// load/init so resume/retry decisions are reflected in the query.
type Fetcher func(ctx context.Context, deps Deps, cp *Checkpoint) ([]Candidate, error)

// Run executes one backfill pass (dry-run or apply) and returns the
// report plus a checkpoint suitable for serialisation. It is the pure,
// testable core previously inlined in cmd/admin — batch processing,
// retry recovery, missing-channel accounting, and checkpoint flush.
func Run(ctx context.Context, deps Deps, fetch Fetcher, enq Enqueuer, log *zap.Logger) (Report, *Checkpoint, error) {
	start := time.Now()
	report := Report{
		Mode:   "dry-run",
		Source: deps.Source,
		Limit:  deps.Limit,
	}
	if deps.Apply {
		report.Mode = "apply"
	}

	// ── Load or init checkpoint ─────────────────────────────────────
	var cp *Checkpoint
	if deps.Checkpoint != "" {
		loaded, err := LoadCheckpoint(deps.Checkpoint)
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
			cp = &Checkpoint{
				JobID:     fmt.Sprintf("backfill-emb-%s", uuid.NewString()[:8]),
				Source:    deps.Source,
				Status:    "running",
				StartedAt: time.Now().UTC().Format(time.RFC3339Nano),
			}
		}
	}

	// ── Determine candidates via the injected fetcher ───────────────
	if fetch == nil {
		return report, cp, fmt.Errorf("backfill: nil fetcher (caller must supply the candidate source)")
	}
	candidates, err := fetch(ctx, deps, cp)
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
	// be re-processed on resume. This is safe because the outbox
	// event_key is deterministic per (assetID, schemaVersion, contentHash).
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
		// Deterministic event_key fingerprint: resolve the content hash
		// carried by the candidate (populated by the SQL fetcher) and
		// fall back to the legacy marker when the asset has no hash.
		ch := c.ContentHash
		if ch == "" {
			ch = "legacy_no_hash_" + c.ID
		}

		if err := enq.EnqueueReindex(ctx, c.ID, ch, true); err != nil {
			report.Failed++
			if cp != nil {
				cp.FailedCount++
				cp.FailedIDs = append(cp.FailedIDs, c.ID)
			}
			log.Warn("EnqueueReindex failed",
				zap.String("asset_id", c.ID),
				zap.String("source", c.Source),
				zap.Error(err))
			report.Errors = append(report.Errors,
				fmt.Sprintf("%s: %v", c.ID, err))
			continue
		}

		report.Succeeded++
		log.Debug("EnqueueReindex succeeded",
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
		// up to --progress assets on resume. Safe because the outbox
		// event_key is deterministic per (assetID, schemaVersion, contentHash).
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

// LoadCheckpoint reads and parses a checkpoint JSON file, failing
// closed when the file is corrupt or lacks the job_id marker.
func LoadCheckpoint(path string) (*Checkpoint, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cp Checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return nil, fmt.Errorf("parse checkpoint: %w", err)
	}
	if cp.JobID == "" {
		return nil, fmt.Errorf("checkpoint %q is missing job_id", path)
	}
	return &cp, nil
}
