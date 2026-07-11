// Package texttracks — backfill.go: BackfillService orchestrates
// per-clip text-track materialization for the operator-facing CLI
// `pipelinegen-admin text-tracks-backfill`.
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 5 (July 2026).
//
// godlike/06 SSOT: this file is the SOLE canonical owner of the
// "process one clip for text-track backfill" decision. The CLI
// package (cmd/admin/text_tracks_backfill.go) drives the loop;
// the materializer (materializer.go) owns the per-language
// fan-out; the acquire service (acquire.go) owns the
// source-text acquisition chain. Handlers MUST NOT re-implement
// the per-clip pipeline inline.
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
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// MediaAssetLister is the narrow port the BackfillService uses to
// query candidate media_assets rows. Production wired via the
// concrete *assets.ClipsRepository (which has a List method with
// the same signature); tests may swap fakes.
type MediaAssetLister interface {
	List(ctx context.Context, filter asset.Filter) ([]*asset.Asset, error)
}

// BackfillService is the application-layer orchestrator. It is the
// SOLE canonical owner of the "process one clip for text-track
// backfill" decision; the CLI drives the loop.
type BackfillService struct {
	clips        MediaAssetLister
	repo         asset.TextTrackRepository // Fase 5: used by tryAcquire to save acquired source tracks
	materializer *Materializer
	acquirer     *AcquireService // Fase 5: optional source-text acquisition
	log          *zap.Logger
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
	materializer *Materializer,
	acquirer *AcquireService,
	log *zap.Logger,
) (*BackfillService, error) {
	if clips == nil {
		return nil, fmt.Errorf("texttracks.NewBackfillService: clips lister is nil")
	}
	if repo == nil {
		return nil, fmt.Errorf("texttracks.NewBackfillService: repo is required (used by tryAcquire to save acquired source tracks)")
	}
	if materializer == nil {
		return nil, fmt.Errorf("texttracks.NewBackfillService: materializer is nil")
	}
	if log == nil {
		return nil, fmt.Errorf("texttracks.NewBackfillService: log is nil")
	}
	return &BackfillService{
		clips:        clips,
		repo:         repo,
		materializer: materializer,
		acquirer:     acquirer,
		log:          log,
	}, nil
}

// BackfillAssetResult is the per-clip result. Surfaces both
// success stats and a per-clip error so the CLI can build a
// fail-closed report (errors never silently collapse into
// "succeeded").
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

// BackfillReport is the aggregate return value.
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

// BackfillOptions bundles the operator's CLI flags into a typed
// value. Validate() enforces the invariants.
type BackfillOptions struct {
	Source          string
	SourceLanguage  string
	TargetLanguages []string
	TextKind        asset.TextTrackKind
	OnlyMissing     bool
	Limit           int
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

// ListCandidates queries media_assets per the operator's filter.
// Only the assets matching the source filter are returned; the
// --only-missing filter is applied at the per-clip level (via
// ListReadyLanguages) so the SQL stays simple.
func (s *BackfillService) ListCandidates(
	ctx context.Context,
	opts BackfillOptions,
) ([]*asset.Asset, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}

	filter := asset.Filter{
		Source:    opts.Source,
		MediaType: "clip", // exclude folders
	}
	if opts.Limit > 0 {
		filter.Limit = opts.Limit
	}
	assets, err := s.clips.List(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("texttracks.BackfillService.ListCandidates: list: %w", err)
	}
	return assets, nil
}

// IsAssetMissingForTargetSet reports whether the asset has fewer
// READY target-language tracks than the operator's target set.
// The check uses the TextTrackRepository's ListReadyLanguages
// (NOT inline SQL) so the canonical "READY-only" decision stays
// in one place (godlike/06 SSOT).
//
// Returns (true, nil) when the asset is missing ≥ 1 target
// language. Returns (false, nil) when the asset has all target
// languages READY (or the target set is empty).
func (s *BackfillService) IsAssetMissingForTargetSet(
	ctx context.Context,
	repo asset.TextTrackRepository,
	assetID string,
	opts BackfillOptions,
) (bool, error) {
	if len(opts.TargetLanguages) == 0 {
		return true, nil
	}
	if repo == nil {
		return true, nil
	}
	ready, err := repo.ListReadyLanguages(ctx, assetID, opts.TextKind)
	if err != nil {
		return false, fmt.Errorf("texttracks.BackfillService.IsAssetMissingForTargetSet: list ready: %w", err)
	}
	readySet := make(map[string]struct{}, len(ready))
	for _, lang := range ready {
		readySet[lang] = struct{}{}
	}
	for _, target := range opts.TargetLanguages {
		if _, ok := readySet[target]; !ok {
			return true, nil
		}
	}
	return false, nil
}

// ProcessAsset runs the per-clip backfill pipeline:
//
//  1. Look up the source track in asset_text_tracks for the
//     (asset, sourceLanguage, textKind) triple.
//  2. If the source is not READY AND acquirer is wired → call
//     AcquireService.Acquire (priorities 2-5). On success,
//     save the acquired text as a READY source track and
//     re-query.
//  3. If the source is STILL not READY (acquisition failed or
//     acquirer is nil) → surface a typed error and skip
//     (no fan-out).
//  4. Call Materializer.Materialize which fan-outs to all
//     target languages, classifies (skip/create/retranslate),
//     upserts, and emits the asset.index.requested outbox
//     event.
//
// Returns a typed result + a per-clip error. The error is
// nil on success (including when all target languages are
// skipped because they're already READY with the matching
// key).
func (s *BackfillService) ProcessAsset(
	ctx context.Context,
	assetItem *asset.Asset,
	opts BackfillOptions,
) (BackfillAssetResult, error) {
	start := time.Now()
	res := BackfillAssetResult{
		AssetID:        assetItem.ID,
		Source:         string(assetItem.Source),
		SourceLanguage: opts.SourceLanguage,
	}

	if assetItem == nil || assetItem.ID == "" {
		res.Err = "asset is nil or has empty id"
		res.DurationMs = time.Since(start).Milliseconds()
		return res, errors.New(res.Err)
	}

	// Step 1: read the source track. The Materializer does this
	// internally, but we need the source track's text_hash to
	// satisfy the Materialize signature. We piggyback on the
	// materializer's FindSourceTrack via a fresh Resolver
	// (constructed from the same ResolverConfig as the
	// materializer).
	//
	// godlike/06 SSOT: the per-(asset,kind) source lookup is
	// owned by Resolver.FindSourceTrack; the BackfillService
	// delegates to it instead of inlining the SQL.
	resolver := NewResolver(s.materializer.resolverCfg, s.materializer.repo, assetItem.ID, opts.TextKind)
	source, err := resolver.FindSourceTrack(ctx)
	if err != nil {
		var noSrc *ErrNoSourceTrack
		var notReady *ErrTrackNotReady
		switch {
		case errors.As(err, &noSrc):
			// Fase 5: try acquisition (priorities 2-5) before
			// giving up. If the acquirer is nil OR acquisition
			// fails, surface the original "no source" error.
			if s.acquirer != nil {
				acquired, acqErr := s.tryAcquire(ctx, assetItem, opts)
				if acqErr != nil {
					s.log.Warn("backfill: acquisition failed; clip will be skipped",
						zap.String("asset_id", assetItem.ID),
						zap.Error(acqErr))
					res.Err = "no_source_track"
					res.SourceReady = false
					res.DurationMs = time.Since(start).Milliseconds()
					return res, nil
				}
				res.SourceAcquired = true
				res.AcquiredFrom = acquiredFromLabel(acquired.SourceType)
				// Re-query the source track (now it should be READY).
				source, err = resolver.FindSourceTrack(ctx)
				if err != nil {
					res.Err = "acquired_but_save_failed"
					res.DurationMs = time.Since(start).Milliseconds()
					return res, nil
				}
				// Fall through to Materializer.
			} else {
				res.Err = "no_source_track"
				res.SourceReady = false
				res.DurationMs = time.Since(start).Milliseconds()
				s.log.Info("backfill: clip has no source track and AcquireService is not wired; run with AcquireService to fill the gap",
					zap.String("asset_id", assetItem.ID),
					zap.String("source_language", opts.SourceLanguage),
					zap.String("text_kind", string(opts.TextKind)))
				return res, nil
			}
		case errors.As(err, &notReady):
			res.Err = "source_track_not_ready"
			res.SourceReady = false
			res.DurationMs = time.Since(start).Milliseconds()
			s.log.Info("backfill: source track is not READY",
				zap.String("asset_id", assetItem.ID),
				zap.String("source_language", opts.SourceLanguage),
				zap.String("current_status", string(notReady.CurrentStatus)),
				zap.Strings("ready_languages", notReady.AvailableLanguages))
			return res, nil
		default:
			res.Err = err.Error()
			res.DurationMs = time.Since(start).Milliseconds()
			return res, fmt.Errorf("texttracks.BackfillService.ProcessAsset: find source: %w", err)
		}
	}
	res.SourceReady = true

	// Step 4: call the Materializer with the source's text_hash.
	// The Materializer emits the asset.index.requested outbox
	// event when any language is created or retranslated, and
	// classifies (skip/retranslate) using the canonical
	// MaterializationKey (SourceVersion + ModelVersion) so
	// re-running is idempotent.
	report, err := s.materializer.Materialize(
		ctx,
		assetItem.ID,
		opts.SourceLanguage,
		source.TextHash,
		opts.TextKind,
		opts.TargetLanguages,
	)
	if err != nil {
		// A Materialize error is non-fatal at the per-clip
		// level: report carries the partial state. Surface
		// the error to the CLI so the report carries a
		// non-empty FailedLanguages map.
		res.DurationMs = time.Since(start).Milliseconds()
		res.Err = err.Error()
		if report != nil {
			res.CreatedLangs = append(res.CreatedLangs, report.CreatedLanguages...)
			res.SkippedLangs = append(res.SkippedLangs, report.SkippedLanguages...)
			res.Retranslated = append(res.Retranslated, report.RetranslatedLanguages...)
			for k := range report.FailedLanguages {
				res.FailedLangs = append(res.FailedLangs, k)
			}
		}
		return res, fmt.Errorf("texttracks.BackfillService.ProcessAsset: materialize: %w", err)
	}

	res.CreatedLangs = append(res.CreatedLangs, report.CreatedLanguages...)
	res.SkippedLangs = append(res.SkippedLangs, report.SkippedLanguages...)
	res.Retranslated = append(res.Retranslated, report.RetranslatedLanguages...)
	for k := range report.FailedLanguages {
		res.FailedLangs = append(res.FailedLangs, k)
	}
	res.DurationMs = time.Since(start).Milliseconds()
	return res, nil
}

// tryAcquire is the Fase 5 Fase 5 helper: runs the
// AcquireService chain (priorities 2-5) and saves the result
// as a READY source track. Returns (result, nil) on success;
// (nil, err) when acquisition fails.
//
// godlike/06 SSOT: the per-clip acquisition decision is owned
// by the BackfillService. The AcquireService is a
// "find-or-fail" primitive; the BackfillService is the
// "save-on-success" wrapper.
func (s *BackfillService) tryAcquire(
	ctx context.Context,
	assetItem *asset.Asset,
	opts BackfillOptions,
) (*AcquireResult, error) {
	localPath := extractLocalPath(assetItem)
	videoID := extractVideoID(assetItem)
	if localPath == "" && videoID == "" {
		return nil, fmt.Errorf("backfill: cannot acquire — no local_path or video_id on asset %s", assetItem.ID)
	}
	acqResult, err := s.acquirer.Acquire(ctx, AcquireCommand{
		AssetID:   assetItem.ID,
		VideoID:   videoID,
		LocalPath: localPath,
		Language:  opts.SourceLanguage,
	})
	if err != nil {
		return nil, err
	}
	// Save the acquired text as a READY source track. Uses
	// the canonical text-hash factory + source-version
	// computation. The track is upserted on
	// UNIQUE(asset_id, language_code, text_kind) so the
	// operation is idempotent.
	lang := acqResult.LanguageCode
	if lang == "" {
		lang = opts.SourceLanguage
	}
	hash := asset.TextHash(acqResult.PlainText, lang, opts.TextKind)
	srcType := acqResult.SourceType
	// Convert the priority-2 "local_file" sentinel to the
	// canonical asset.TextSourceProvided (local files are
	// treated as user-provided provenance).
	if srcType == "local_file" {
		srcType = asset.TextSourceProvided
	}
	track := asset.TextTrack{
		AssetID:            assetItem.ID,
		LanguageCode:       lang,
		TextKind:           opts.TextKind,
		TextContent:        acqResult.PlainText,
		SourceType:         srcType,
		SourceLanguageCode: lang,
		IsOriginal:         true,
		Provider:           providerFromSourceType(acqResult.SourceType),
		TextHash:           hash,
		SourceVersion:      asset.SourceVersion(hash, lang, lang, providerFromSourceType(acqResult.SourceType), "", "", ""),
		Confidence:         acqResult.Confidence,
		Status:             asset.TextTrackReady,
	}
	if err := s.repo.UpsertBatch(ctx, []asset.TextTrack{track}); err != nil {
		return acqResult, fmt.Errorf("backfill: save acquired source track: %w", err)
	}
	s.log.Info("backfill: acquired source track saved",
		zap.String("asset_id", assetItem.ID),
		zap.String("language", lang),
		zap.String("source_type", string(acqResult.SourceType)),
		zap.Int("priority", acqResult.Priority))
	return acqResult, nil
}

// extractLocalPath reads the local_path from the asset's
// Metadata map (the canonical YouTube-pipeline field). Falls
// back to the Filename field when Metadata is empty.
func extractLocalPath(a *asset.Asset) string {
	if a == nil {
		return ""
	}
	if a.Metadata != nil {
		if lp, ok := a.Metadata["local_path"].(string); ok {
			return lp
		}
	}
	return a.Filename
}

// extractVideoID reads the YouTube video ID from the asset's
// Metadata map. The canonical YouTube-pipeline field is
// `source_id` (or `video_id` for legacy clips).
func extractVideoID(a *asset.Asset) string {
	if a == nil {
		return ""
	}
	if a.Metadata != nil {
		if v, ok := a.Metadata["source_id"].(string); ok && v != "" {
			return v
		}
		if v, ok := a.Metadata["video_id"].(string); ok && v != "" {
			return v
		}
	}
	// Fallback: strip the "yt_" prefix from the asset ID
	// (canonical clip ID format: yt_<videoID>_<start>_<end>_<policy>).
	if len(a.ID) > 3 && a.ID[:3] == "yt_" {
		// Find the second underscore.
		for i := 3; i < len(a.ID); i++ {
			if a.ID[i] == '_' {
				return a.ID[3:i]
			}
		}
	}
	return ""
}

// acquiredFromLabel maps the source_type to a human-readable
// label for the per-clip report.
func acquiredFromLabel(st asset.TextTrackSource) string {
	switch st {
	case asset.TextSourceProvided:
		return "local_file"
	case asset.TextSourceYouTubeSubtitle:
		return "youtube_subtitle"
	case asset.TextSourceWhisper:
		return "whisper"
	default:
		return string(st)
	}
}

// providerFromSourceType returns a canonical Provider string
// for the acquired text-track's source_type. The Materializer
// uses Provider as part of the MaterializationKey.
func providerFromSourceType(st asset.TextTrackSource) string {
	switch st {
	case asset.TextSourceYouTubeSubtitle:
		return "youtube"
	case asset.TextSourceWhisper:
		return "whisper"
	default:
		return ""
	}
}

// Run is the high-level driver: query candidates, optionally
// pre-filter via --only-missing, then process each asset. The
// per-asset processing is fail-soft (a single failed clip is
// logged + counted, not raised); the returned report carries
// the failure IDs so the CLI can surface them.
func (s *BackfillService) Run(
	ctx context.Context,
	repo asset.TextTrackRepository,
	opts BackfillOptions,
) (*BackfillReport, error) {
	start := time.Now()
	if err := opts.Validate(); err != nil {
		return nil, err
	}

	candidates, err := s.ListCandidates(ctx, opts)
	if err != nil {
		return nil, err
	}

	report := &BackfillReport{
		Source:          opts.Source,
		SourceLanguage:  opts.SourceLanguage,
		TargetLanguages: append([]string{}, opts.TargetLanguages...),
		TextKind:        string(opts.TextKind),
		TotalCandidates: len(candidates),
		PerAsset:        make([]BackfillAssetResult, 0, len(candidates)),
		FailedAssetIDs:  []string{},
		SkippedAssetIDs: []string{},
	}

	for i, item := range candidates {
		select {
		case <-ctx.Done():
			report.DurationMs = time.Since(start).Milliseconds()
			return report, ctx.Err()
		default:
		}

		// --only-missing pre-filter: skip the clip entirely
		// when all target languages are READY.
		if opts.OnlyMissing && repo != nil {
			missing, err := s.IsAssetMissingForTargetSet(ctx, repo, item.ID, opts)
			if err != nil {
				s.log.Warn("backfill: IsAssetMissingForTargetSet failed; processing anyway",
					zap.String("asset_id", item.ID),
					zap.Error(err))
			} else if !missing {
				report.SkippedAssetIDs = append(report.SkippedAssetIDs, item.ID)
				report.SkippedOnlyMissing++
				report.PerAsset = append(report.PerAsset, BackfillAssetResult{
					AssetID:        item.ID,
					Source:         string(item.Source),
					SourceLanguage: opts.SourceLanguage,
					Skipped:        true,
					SkipReason:     "all_target_languages_ready",
				})
				continue
			}
		}

		report.Processed++
		res, perErr := s.ProcessAsset(ctx, item, opts)
		report.PerAsset = append(report.PerAsset, res)

		switch {
		case perErr != nil && res.Err == "no_source_track":
			report.SourceMissingCount++
			report.FailedAssetIDs = append(report.FailedAssetIDs, item.ID)
		case perErr != nil && res.Err == "source_track_not_ready":
			report.SourceMissingCount++
			report.FailedAssetIDs = append(report.FailedAssetIDs, item.ID)
		case perErr != nil && res.Err == "acquired_but_save_failed":
			report.SourceMissingCount++
			report.FailedAssetIDs = append(report.FailedAssetIDs, item.ID)
		case perErr != nil:
			report.FailedAssetIDs = append(report.FailedAssetIDs, item.ID)
		default:
			if res.SourceReady {
				report.SourceReadyCount++
			}
			if res.SourceAcquired {
				report.SourceAcquiredCount++
			}
		}

		report.CreatedTotal += len(res.CreatedLangs)
		report.SkippedLangTotal += len(res.SkippedLangs)
		report.RetranslatedTotal += len(res.Retranslated)
		report.FailedLangTotal += len(res.FailedLangs)

		if (i+1)%50 == 0 {
			s.log.Info("backfill progress",
				zap.Int("processed", report.Processed),
				zap.Int("source_ready", report.SourceReadyCount),
				zap.Int("source_acquired", report.SourceAcquiredCount),
				zap.Int("source_missing", report.SourceMissingCount),
				zap.Int("skipped_only_missing", report.SkippedOnlyMissing),
				zap.Int("created_total", report.CreatedTotal),
				zap.Int("retranslated_total", report.RetranslatedTotal),
				zap.Int("failed_lang_total", report.FailedLangTotal),
				zap.Int("remaining", len(candidates)-i-1))
		}
	}

	report.DurationMs = time.Since(start).Milliseconds()
	return report, nil
}
