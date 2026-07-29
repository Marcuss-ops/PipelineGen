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
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"
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
func runTextTracksBackfill(args []string) error {
	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	deps, err := parseTextTracksBackfillArgs(args)
	if err != nil {
		return err
	}

	ctx := cmdContext()

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

// parseTextTracksBackfillArgs is the pure, testable flag parser.
func parseTextTracksBackfillArgs(args []string) (textTracksBackfillDeps, error) {
	deps := textTracksBackfillDeps{
		Progress: 50,
		TextKind: "transcript",
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
		case strings.HasPrefix(a, "--languages="):
			deps.Languages = strings.TrimPrefix(a, "--languages=")
		case strings.HasPrefix(a, "--text-kind="):
			deps.TextKind = strings.TrimPrefix(a, "--text-kind=")
		case strings.HasPrefix(a, "--asset-ids="):
			deps.AssetIDs = strings.TrimPrefix(a, "--asset-ids=")
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
	if deps.Source == "" {
		return deps, fmt.Errorf("--source is required (e.g. --source=youtube)")
	}
	if deps.Languages == "" {
		return deps, fmt.Errorf("--languages is required (e.g. --languages=it,en,es,pt-BR,fr,de; first entry is the source language)")
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

// splitCSV splits a comma-separated list into trimmed non-empty values.
func splitCSV(csv string) []string {
	parts := strings.Split(csv, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		v := strings.TrimSpace(p)
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// splitLanguages splits the --languages CSV into (source, targets).
// The first entry is the source language; the rest are targets.
func splitLanguages(csv string) (string, []string, error) {
	parts := strings.Split(csv, ",")
	source := strings.TrimSpace(parts[0])
	if source == "" {
		return "", nil, fmt.Errorf("--languages: first entry is the source language and must be non-empty")
	}
	targets := make([]string, 0, len(parts)-1)
	for _, p := range parts[1:] {
		t := strings.TrimSpace(p)
		if t == "" {
			continue
		}
		targets = append(targets, t)
	}
	return source, targets, nil
}

// isKnownTextTrackKind is duplicated from jobs.go to keep the
// CLI self-contained (the application-layer
// texttracks.isKnownTextTrackKind is unexported). The set
// MUST match the canonical list in jobs.go::isKnownTextTrackKind.
func isKnownTextTrackKind(k asset.TextTrackKind) bool {
	switch k {
	case asset.TextTrackTranscript,
		asset.TextTrackDescription,
		asset.TextTrackSummary,
		asset.TextTrackTitle,
		asset.TextTrackKeywords:
		return true
	}
	return false
}

// loadOrInitTextTracksCheckpoint loads a checkpoint from disk
// when --checkpoint is set, or returns a fresh one.
func loadOrInitTextTracksCheckpoint(deps textTracksBackfillDeps) (*textTracksCheckpoint, error) {
	if deps.Checkpoint == "" {
		return nil, nil
	}
	if deps.Resume || deps.RetryFailed {
		data, err := os.ReadFile(deps.Checkpoint)
		if err != nil {
			return nil, fmt.Errorf("--resume/--retry-failed: read checkpoint %q: %w", deps.Checkpoint, err)
		}
		var cp textTracksCheckpoint
		if err := json.Unmarshal(data, &cp); err != nil {
			return nil, fmt.Errorf("--resume/--retry-failed: parse checkpoint %q: %w", deps.Checkpoint, err)
		}
		if cp.JobID == "" {
			return nil, fmt.Errorf("checkpoint %q is missing job_id (corrupt?)", deps.Checkpoint)
		}
		return &cp, nil
	}
	return &textTracksCheckpoint{
		JobID:     fmt.Sprintf("text-tracks-backfill-%s", uuid.NewString()[:8]),
		Source:    deps.Source,
		Status:    "running",
		StartedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}, nil
}

// printTextTracksBackfillJSON is the local JSON helper. Named
// to avoid the redeclaration with drive_doctor.go's printJSON
// (which takes a specific doctorReport type).
func printTextTracksBackfillJSON(v any) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Printf("json marshal failed: %v\n", err)
		return
	}
	fmt.Println(string(b))
}

// printHumanTextTracksBackfill prints the human-readable
// summary for the --apply path.
func printHumanTextTracksBackfill(r textTracksBackfillReport) {
	fmt.Println("=== Text-Tracks Backfill Complete ===")
	fmt.Printf("  Source:            %s\n", r.Source)
	fmt.Printf("  Source language:   %s\n", r.SourceLanguage)
	fmt.Printf("  Target languages:  %s\n", strings.Join(r.TargetLanguages, ", "))
	fmt.Printf("  Text kind:         %s\n", r.TextKind)
	fmt.Printf("  Only-missing:      %v\n", r.OnlyMissing)
	fmt.Printf("  Total candidates:  %d\n", r.TotalCandidates)
	fmt.Printf("  Processed:         %d\n", r.Processed)
	fmt.Printf("  Source READY:      %d\n", r.SourceReady)
	fmt.Printf("  Source ACQUIRED:   %d\n", r.SourceAcquired)
	fmt.Printf("  Source missing:    %d\n", r.SourceMissing)
	fmt.Printf("  Skipped (complete):%d\n", r.SkippedOnlyMissing)
	fmt.Printf("  Languages created: %d\n", r.CreatedTotal)
	fmt.Printf("  Languages skipped: %d\n", r.SkippedLangTotal)
	fmt.Printf("  Languages re-tr:   %d\n", r.RetranslatedTotal)
	fmt.Printf("  Languages failed:  %d\n", r.FailedLangTotal)
	fmt.Printf("  Duration:          %dms\n", r.DurationMs)
	if r.Checkpoint != "" {
		fmt.Printf("  Checkpoint:        %s\n", r.Checkpoint)
	}
	if len(r.FailedAssetIDs) > 0 {
		fmt.Printf("  Failed asset IDs (%d):\n", len(r.FailedAssetIDs))
		for _, id := range r.FailedAssetIDs {
			fmt.Printf("    - %s\n", id)
		}
		fmt.Println("  Re-run with --retry-failed --checkpoint=<path> to retry.")
	}
}
