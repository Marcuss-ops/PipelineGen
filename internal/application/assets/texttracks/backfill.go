// Package texttracks — backfill.go (FACADE):
//
// godlike/06 SSOT: this file is the SOLE canonical owner of the
// BackfillService facade (struct + constructor + typed-port seam +
// DTOs). The per-responsibility split lives in:
//
//   - backfill_candidates.go : ListCandidates + IsAssetMissingForTargetSet
//     (pre-filter leaf)
//   - backfill_process.go    : (*BackfillService).ProcessAsset
//     (per-clip single-asset pipeline)
//   - backfill_acquire.go    : (*BackfillService).tryAcquire +
//     extractLocalPath + extractVideoID +
//     acquiredFromLabel + providerFromSourceType
//     (acquisition+save leaf + helpers)
//   - backfill_run.go        : (*BackfillService).Run
//     (high-level driver loop + counters)
//
// The CLI drives the loop via cmd/admin/text_tracks_backfill.go;
// the materializer (materializer.go) owns per-language fan-out;
// the acquire service (acquire.go) owns the source-text acquisition
// chain. Handlers MUST NOT re-implement the per-clip pipeline
// inline.
//
// godlike/07 fail-closed: the BackfillService never silently
// substitutes a missing source with an empty string. If the
// source track is not READY AND acquisition is not wired
// (acquirer == nil), the per-clip result carries a typed error;
// the CLI surfaces it in the report.
//
// Pipeline (Fase 5):
//  1. Read asset_text_tracks for the source language (priority 1).
//  2. If source is READY → fan-out to target languages via
//     Materializer.Materialize.
//  3. If source is missing AND acquirer is wired → call
//     AcquireService.Acquire (priorities 2-5: local VTT/SRT,
//     YouTube subs, Whisper). On success, save the acquired
//     text as a READY source track, then re-query and proceed
//     to step 2.
//  4. If source is missing AND acquirer is nil → surface
//     ErrNoSourceTrack; the operator can run a future
//     `acquire` subcommand to fill the gap.
package texttracks

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// MediaAssetLister is the narrow port the BackfillService uses to
// query candidate media_assets rows. Production wired via the
// concrete *assets.ClipsRepository (which has a List method with
// the same signature); tests may swap fakes.
//
// godlike/06 SSOT: this interface is the SOLE canonical owner
// of the candidate-listing contract — the file system MUST
// traverse this interface for any "give me candidate clips"
// query. Inline List() calls are forbidden.
type MediaAssetLister interface {
	List(ctx context.Context, filter asset.Filter) ([]*asset.Asset, error)
}

// BackfillService is the application-layer orchestrator. It is the
// SOLE canonical owner of the "process one clip for text-track
// backfill" decision; the CLI drives the loop.
//
// godlike/06 SSOT: the orchestrator's method bodies live in the
// per-responsibility leaves (backfill_process.go, backfill_run.go,
// etc.). This struct definition + constructor stay here because
// they're the package's composition root entry point — the
// leaf files compose deps that declare here.
type BackfillService struct {
	clips           MediaAssetLister
	repo            asset.TextTrackRepository // Fase 5: used by tryAcquire to save acquired source tracks
	cues            TimedCueWriter            // new field to save cues/segments to DB
	subArtRepo      asset.SubtitleArtifactRepository
	subMaterializer *SubtitleArtifactMaterializer
	materializer    *Materializer
	acquirer        *AcquireService // Fase 5: optional source-text acquisition
	publisher       delivery.Publisher
	driveFolderID   string
	log             *zap.Logger
}

// NewBackfillService constructs the canonical orchestrator.
//
// godlike/07 fail-closed: a nil dep surfaces as a typed error.
// The acquirer is OPTIONAL (nil → skip acquisition; the
// per-clip report surfaces "no source" for clips without a
// READY track). The repo is REQUIRED — tryAcquire uses it
// to save acquired source tracks without reaching through
// the Materializer's private fields.
func NewBackfillService(
	clips MediaAssetLister,
	repo asset.TextTrackRepository,
	cues TimedCueWriter,
	subArtRepo asset.SubtitleArtifactRepository,
	materializer *Materializer,
	acquirer *AcquireService,
	publisher delivery.Publisher,
	driveFolderID string,
	log *zap.Logger,
) (*BackfillService, error) {
	if clips == nil {
		return nil, fmt.Errorf("texttracks.NewBackfillService: clips lister is nil")
	}
	if repo == nil {
		return nil, fmt.Errorf("texttracks.NewBackfillService: repo is required (used by tryAcquire to save acquired source tracks)")
	}
	if cues == nil {
		return nil, fmt.Errorf("texttracks.NewBackfillService: cues writer is required")
	}
	if subArtRepo == nil {
		return nil, fmt.Errorf("texttracks.NewBackfillService: subArtRepo is required")
	}
	if materializer == nil {
		return nil, fmt.Errorf("texttracks.NewBackfillService: materializer is nil")
	}
	if publisher == nil {
		return nil, fmt.Errorf("texttracks.NewBackfillService: Drive publisher is required")
	}
	if log == nil {
		return nil, fmt.Errorf("texttracks.NewBackfillService: log is nil")
	}
	return &BackfillService{
		clips:           clips,
		repo:            repo,
		cues:            cues,
		subArtRepo:      subArtRepo,
		subMaterializer: NewSubtitleArtifactMaterializer(subArtRepo, "data/media/subtitles", publisher),
		materializer:    materializer,
		acquirer:        acquirer,
		publisher:       publisher,
		driveFolderID:   driveFolderID,
		log:             log,
	}, nil
}

// BackfillOptions bundles the operator's CLI flags into a typed
// value. Validate() enforces the invariants.
//
// godlike/06 SSOT: this is the SOLE canonical input shape for the
// backfill pipeline. CLI parsing layers (cmd/admin/...) MUST
// translate operator flags into a BackfillOptions struct; ad-hoc
// arg bags are forbidden.
type BackfillOptions struct {
	Source          string
	SourceLanguage  string
	TargetLanguages []string
	TextKind        asset.TextTrackKind
	OnlyMissing     bool
	Limit           int
	AssetIDs        []string
}

// Validate returns an error for any invalid input. Empty
// target_languages is allowed (the materializer falls back to
// its configured set) but the source language is REQUIRED.
func (o BackfillOptions) Validate() error {
	if o.Source == "" {
		return fmt.Errorf("texttracks.BackfillOptions.Validate: source is required")
	}
	if o.SourceLanguage == "" {
		return fmt.Errorf("texttracks.BackfillOptions.Validate: source_language is required")
	}
	if o.TextKind == "" {
		return fmt.Errorf("texttracks.BackfillOptions.Validate: text_kind is required")
	}
	return nil
}

// BackfillAssetResult is the per-clip result. Surfaces both
// success stats and a per-clip error so the CLI can build a
// fail-closed report (errors never silently collapse into
// "succeeded").
//
// godlike/06 SSOT: this is the SOLE canonical wire-format struct
// for the per-clip result. The CLI's textTracksBackfillReport
// mirrors the same fields; ad-hoc per-clip result types are
// forbidden.
type BackfillAssetResult struct {
	AssetID        string   `json:"asset_id"`
	Source         string   `json:"source"`
	SourceLanguage string   `json:"source_language"`
	SourceReady    bool     `json:"source_ready"`
	SourceAcquired bool     `json:"source_acquired"`         // Fase 5: true when acquirer filled the gap
	AcquiredFrom   string   `json:"acquired_from,omitempty"` // "local_file" | "youtube_subtitle" | "whisper" | ""
	Skipped        bool     `json:"skipped"`
	SkipReason     string   `json:"skip_reason,omitempty"`
	CreatedLangs   []string `json:"created_languages,omitempty"`
	SkippedLangs   []string `json:"skipped_languages,omitempty"`
	Retranslated   []string `json:"retranslated_languages,omitempty"`
	FailedLangs    []string `json:"failed_languages,omitempty"`
	DurationMs     int64    `json:"duration_ms"`
	Err            string   `json:"error,omitempty"`
}

// BackfillReport is the aggregate return value. Carries the
// per-clip slice + aggregate counters so the CLI can render a
// human + JSON report.
//
// godlike/06 SSOT: this is the SOLE canonical wire-format struct
// for the aggregate report; the CLI's textTracksBackfillReport
// mirrors the same fields.
type BackfillReport struct {
	Source              string                `json:"source"`
	SourceLanguage      string                `json:"source_language"`
	TargetLanguages     []string              `json:"target_languages"`
	TextKind            string                `json:"text_kind"`
	TotalCandidates     int                   `json:"total_candidates"`
	Processed           int                   `json:"processed"`
	SourceReadyCount    int                   `json:"source_ready_count"`
	SourceMissingCount  int                   `json:"source_missing_count"`
	SourceAcquiredCount int                   `json:"source_acquired_count"` // Fase 5
	SkippedOnlyMissing  int                   `json:"skipped_only_missing"`
	CreatedTotal        int                   `json:"created_total"`
	SkippedLangTotal    int                   `json:"skipped_lang_total"`
	RetranslatedTotal   int                   `json:"retranslated_total"`
	FailedLangTotal     int                   `json:"failed_lang_total"`
	FailedAssetIDs      []string              `json:"failed_asset_ids,omitempty"`
	SkippedAssetIDs     []string              `json:"skipped_asset_ids,omitempty"`
	DurationMs          int64                 `json:"duration_ms"`
	PerAsset            []BackfillAssetResult `json:"per_asset,omitempty"`
}
