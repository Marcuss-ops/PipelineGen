// cmd/admin/text_tracks_backfill.go — operator-facing CLI for
// per-clip text-track materialization (Fase 5, July 2026).
//
// pipelinegen-admin text-tracks-backfill
//
//	--source youtube
//	--languages it,en,es,pt-BR,fr,de
//	--only-missing
//
// Per-clip pipeline (PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 5, July 2026):
//
//  1. List candidate media_assets filtered by --source.
//  2. For each asset, look up the source track in
//     asset_text_tracks for (asset, sourceLanguage, textKind).
//  3. If the source track is READY → fan-out translation to all
//     target languages via texttracks.Materializer.
//  4. If the source track is MISSING AND the AcquireService is
//     wired → run the 5-priority chain (local VTT/SRT → YouTube
//     subs → Whisper) to acquire the source. On success, save
//     the acquired text as a READY source track and proceed to
//     step 3.
//  5. If the source is still missing → surface a typed error
//     in the per-clip result; the run continues with the next
//     asset (fail-soft at the asset level, hard-fail only on
//     configuration errors).
//  6. The Materializer emits the asset.index.requested outbox
//     event for any language that was created or retranslated.
//
// Idempotency: the Materializer classifies each
// (asset, kind, target_language) triple via MaterializationKey
// (SourceVersion + ModelVersion). Re-running on the same asset
// with the same source text is a no-op for READY tracks
// (skip); only stale keys trigger retranslation. The
// AcquireService is also idempotent: the saved source track's
// TextHash is part of the UNIQUE constraint, so a second run
// finds the track in priority 1 and skips acquisition.
//
// Flags:
//
//	--source <name>         Filter by media_assets.source (required)
//	--languages <csv>       BCP-47 list: [0] = source, [1:] = targets
//	                        (required; first entry is the source lang)
//	--text-kind <kind>      Default: "transcript". One of
//	                        transcript | description | summary |
//	                        title | keywords
//	--only-missing          Skip clips that have ALL target
//	                        languages READY (uses
//	                        ListReadyLanguages)
//	--asset-ids             CSV of canonical media_assets.id values
//	                        to process instead of scanning the
//	                        whole source catalog
//	--limit <n>             Cap the number of candidate assets
//	--progress <n>          Log + checkpoint flush every N assets
//	--checkpoint <path>     Save resume state to JSON
//	--resume                Resume from a previous checkpoint
//	--retry-failed          Retry only the failed asset IDs from
//	                        a previous run
//	--apply                 Write to DB (default: dry-run)
//	--dry-run               Count only, don't write
//	--json                  Machine-readable JSON output
//
// Usage:
//
//	go run ./cmd/admin text-tracks-backfill \
//	    --source youtube --languages it,en,es,pt-BR,fr,de \
//	    --only-missing --apply --json
package backfill

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/texttracks"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// textTracksBackfillDeps groups parsed CLI flags. Pure data; the
// parsing function below is the only mutator.
type textTracksBackfillDeps struct {
	Apply       bool
	DryRun      bool
	JSON        bool
	OnlyMissing bool
	Limit       int
	Progress    int
	Source      string
	Languages   string
	TextKind    string
	AssetIDs    string
	Checkpoint  string
	Resume      bool
	RetryFailed bool
}

// textTracksBackfillReport is the JSON-serialisable report.
type textTracksBackfillReport struct {
	Mode               string   `json:"mode"`
	Source             string   `json:"source"`
	SourceLanguage     string   `json:"source_language"`
	TargetLanguages    []string `json:"target_languages"`
	TextKind           string   `json:"text_kind"`
	OnlyMissing        bool     `json:"only_missing"`
	Limit              int      `json:"limit,omitempty"`
	AssetIDs           []string `json:"asset_ids,omitempty"`
	TotalCandidates    int      `json:"total_candidates"`
	Processed          int      `json:"processed"`
	SourceReady        int      `json:"source_ready_count"`
	SourceAcquired     int      `json:"source_acquired_count"`
	SourceMissing      int      `json:"source_missing_count"`
	SkippedOnlyMissing int      `json:"skipped_only_missing"`
	CreatedTotal       int      `json:"created_total"`
	SkippedLangTotal   int      `json:"skipped_lang_total"`
	RetranslatedTotal  int      `json:"retranslated_total"`
	FailedLangTotal    int      `json:"failed_lang_total"`
	FailedAssetIDs     []string `json:"failed_asset_ids,omitempty"`
	SkippedAssetIDs    []string `json:"skipped_asset_ids,omitempty"`
	Checkpoint         string   `json:"checkpoint,omitempty"`
	DurationMs         int64    `json:"duration_ms"`
	FailedReasonNoSrc  int      `json:"failed_reason_no_source"`
	FailedReasonOther  int      `json:"failed_reason_other"`
}

// textTracksCheckpoint is the on-disk resume state.
type textTracksCheckpoint struct {
	JobID               string   `json:"job_id"`
	Source              string   `json:"source"`
	SourceLanguage      string   `json:"source_language"`
	TargetLanguages     []string `json:"target_languages"`
	TextKind            string   `json:"text_kind"`
	OnlyMissing         bool     `json:"only_missing"`
	Limit               int      `json:"limit,omitempty"`
	AssetIDs            []string `json:"asset_ids,omitempty"`
	LastAssetID         string   `json:"last_asset_id"`
	ProcessedCount      int      `json:"processed_count"`
	SourceReadyCount    int      `json:"source_ready_count"`
	SourceAcquiredCount int      `json:"source_acquired_count"` // Fase 5
	FailedAssetIDs      []string `json:"failed_asset_ids"`
	Status              string   `json:"status"`
	StartedAt           string   `json:"started_at"`
	UpdatedAt           string   `json:"updated_at"`
}

// runTextTracksBackfill is the entry point wired into
// cmd/admin/main.go.
func RunTextTracksBackfill(args []string) error {
	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	deps, err := parseTextTracksBackfillArgs(args)
	if err != nil {
		return err
	}

	ctx := cli.CmdContext()

	sourceLang, targetLangs, err := splitLanguages(deps.Languages)
	if err != nil {
		return err
	}

	textKind := asset.TextTrackKind(deps.TextKind)
	if !isKnownTextTrackKind(textKind) {
		return fmt.Errorf("text-tracks-backfill: unknown --text-kind %q (allowed: transcript, description, summary, title, keywords)", deps.TextKind)
	}

	log.Info("text-tracks-backfill starting",
		zap.Bool("apply", deps.Apply),
		zap.Bool("dry_run", deps.DryRun || !deps.Apply),
		zap.String("source", deps.Source),
		zap.String("source_language", sourceLang),
		zap.Strings("target_languages", targetLangs),
		zap.String("text_kind", string(textKind)),
		zap.Bool("only_missing", deps.OnlyMissing),
		zap.Int("limit", deps.Limit),
		zap.String("asset_ids", deps.AssetIDs),
		zap.Int("progress", deps.Progress),
		zap.String("checkpoint", deps.Checkpoint),
		zap.Bool("resume", deps.Resume),
		zap.Bool("retry_failed", deps.RetryFailed))

	// Reuse the full composition root so the Materializer
	// gets its canonical ResolverConfig (MultilingualConfig +
	// ModelVersion + PromptVersion) and the AI translator is
	// already wired. The composition root also opens the
	// SQLite DB and runs migrations — matches the pattern in
	// backfill_asset_embeddings.go.
	root, _, rootCleanup, err := app.InitComposition(cfg, log)
	if err != nil {
		return fmt.Errorf("text-tracks-backfill: init composition: %w", err)
	}
	defer rootCleanup()

	if root.TextTracks == nil || root.TextTracks.Materializer == nil {
		return fmt.Errorf("text-tracks-backfill: TextTracks.Materializer is nil — verify build_bundles_texttracks.go wires the bundle")
	}
	if root.Repos == nil || root.Repos.ClipsRepo == nil {
		return fmt.Errorf("text-tracks-backfill: Repos.ClipsRepo is nil — verify build_bundles_* wires the clips repository")
	}

	// Build the canonical BackfillService. godlike/06 SSOT:
	// the orchestrator lives in the application layer, not
	// in cmd/. AcquireService is OPTIONAL (Fase 5): when
	// nil, the per-clip report surfaces "no source" for
	// clips without a READY track. The repo is REQUIRED
	// (used by tryAcquire to save acquired source tracks).
	svc, err := texttracks.NewBackfillService(texttracks.BackfillServiceDeps{
		Data: texttracks.BackfillDataDeps{
			Clips:      root.Repos.ClipsRepo,
			Repo:       root.Repos.TextTrackRepo,
			Cues:       root.Domains.CueWriter,
			SubArtRepo: root.Repos.SubtitleArtifactRepo,
		},
		Pipeline: texttracks.BackfillPipelineDeps{
			Materializer: root.TextTracks.Materializer,
			Acquirer:     root.TextTracks.AcquireService,
		},
		Delivery: texttracks.BackfillDeliveryDeps{
			Publisher:     root.Drive.Publisher,
			DriveFolderID: cfg.Drive.ClipsFolder(),
		},
		Log: log,
	})
	if err != nil {
		return fmt.Errorf("text-tracks-backfill: new backfill service: %w", err)
	}

	opts := texttracks.BackfillOptions{
		Source:          deps.Source,
		SourceLanguage:  sourceLang,
		TargetLanguages: targetLangs,
		TextKind:        textKind,
		OnlyMissing:     deps.OnlyMissing,
		Limit:           deps.Limit,
		AssetIDs:        splitCSV(deps.AssetIDs),
	}
	if err := opts.Validate(); err != nil {
		return fmt.Errorf("text-tracks-backfill: %w", err)
	}

	// Checkpoint load / save.
	cp, err := loadOrInitTextTracksCheckpoint(deps)
	if err != nil {
		return err
	}

	// Dry-run path: list candidates only, no DB writes.
	if !deps.Apply {
		candidates, err := svc.ListCandidates(ctx, opts)
		if err != nil {
			return fmt.Errorf("text-tracks-backfill: list candidates: %w", err)
		}
		report := textTracksBackfillReport{
			Mode:            "dry-run",
			Source:          deps.Source,
			SourceLanguage:  sourceLang,
			TargetLanguages: targetLangs,
			TextKind:        string(textKind),
			OnlyMissing:     deps.OnlyMissing,
			Limit:           deps.Limit,
			TotalCandidates: len(candidates),
			FailedAssetIDs:  []string{},
			SkippedAssetIDs: []string{},
		}
		if deps.JSON {
			printTextTracksBackfillJSON(report)
			return nil
		}
		fmt.Println("=== Text-Tracks Backfill DRY-RUN ===")
		fmt.Printf("  Source:           %s\n", deps.Source)
		fmt.Printf("  Source language:  %s\n", sourceLang)
		fmt.Printf("  Target languages: %s\n", strings.Join(targetLangs, ", "))
		fmt.Printf("  Text kind:        %s\n", textKind)
		fmt.Printf("  Only-missing:     %v\n", deps.OnlyMissing)
		fmt.Printf("  Candidates:       %d\n", len(candidates))
		fmt.Println("\nRe-run with --apply to materialize.")
		return nil
	}

	// Apply path: run the BackfillService.
	report, err := svc.Run(ctx, root.Repos.TextTrackRepo, opts)
	if err != nil {
		return fmt.Errorf("text-tracks-backfill: run: %w", err)
	}

	// Update checkpoint.
	if cp != nil {
		cp.Status = "completed"
		if len(report.FailedAssetIDs) > 0 {
			cp.Status = "failed"
		}
		cp.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		cp.SourceReadyCount = report.SourceReadyCount
		cp.FailedAssetIDs = report.FailedAssetIDs
		cp.TargetLanguages = targetLangs
		cp.SourceLanguage = sourceLang
		cp.TextKind = string(textKind)
		cp.OnlyMissing = deps.OnlyMissing
		cp.Limit = deps.Limit
		cp.AssetIDs = splitCSV(deps.AssetIDs)
	}

	out := textTracksBackfillReport{
		Mode:               "apply",
		Source:             deps.Source,
		SourceLanguage:     sourceLang,
		TargetLanguages:    targetLangs,
		TextKind:           string(textKind),
		OnlyMissing:        deps.OnlyMissing,
		Limit:              deps.Limit,
		TotalCandidates:    report.TotalCandidates,
		Processed:          report.Processed,
		SourceReady:        report.SourceReadyCount,
		SourceAcquired:     report.SourceAcquiredCount,
		SourceMissing:      report.SourceMissingCount,
		SkippedOnlyMissing: report.SkippedOnlyMissing,
		CreatedTotal:       report.CreatedTotal,
		SkippedLangTotal:   report.SkippedLangTotal,
		RetranslatedTotal:  report.RetranslatedTotal,
		FailedLangTotal:    report.FailedLangTotal,
		FailedAssetIDs:     report.FailedAssetIDs,
		SkippedAssetIDs:    report.SkippedAssetIDs,
		Checkpoint:         deps.Checkpoint,
		DurationMs:         report.DurationMs,
	}
	for _, p := range report.PerAsset {
		if p.Err == "no_source_track" || p.Err == "source_track_not_ready" || p.Err == "acquired_but_save_failed" {
			out.FailedReasonNoSrc++
		} else if p.Err != "" {
			out.FailedReasonOther++
		}
	}

	if deps.JSON {
		printTextTracksBackfillJSON(out)
	} else {
		printHumanTextTracksBackfill(out)
	}

	if cp != nil && deps.Checkpoint != "" {
		if b, mErr := json.MarshalIndent(cp, "", "  "); mErr == nil {
			_ = os.WriteFile(deps.Checkpoint, b, 0o644)
		}
	}
	return nil
}
