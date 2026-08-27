package localization

// planbuilder.go owns the canonical plan-builder seam: it turns the resolved
// source facts + the ordered localization request into a fingerprinted
// []LocalizedClipPlan. This is the boundary between "which languages" (the
// request) and "which concrete render" (the plan): every business fact a plan
// references — source identity/hash, transcript + subtitle tracks, style,
// profile, renderer — is resolved HERE, so the compiler/Rust only ever execute
// a fully-resolved plan verbatim.
//
// godlike/06 SSOT (one canonical owner per fact): the plan references text
// tracks by (ID, SHA256) — never embedded text. The builder resolves those
// references through the narrow TrackResolver port; detail.TextTrack stays the
// owner of the text content.

import (
	"context"
	"fmt"
	"strings"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// SourceInput is the resolved source-clip identity a plan-builder needs. Every
// value is a canonical fact resolved upstream (registry hash, duration,
// source language); the builder never re-derives them.
type SourceInput struct {
	// JobID correlates every plan to its enclosing Master job. Defaults to a
	// stable per-source identity when empty.
	JobID string
	// SceneID is the editorial scene (optional for standalone clips).
	SceneID string
	// AssetID is the canonical source clip asset id (also the ClipID default).
	AssetID string
	// ClipID overrides the plan ClipID; empty means "use AssetID".
	ClipID string

	// SourceLanguage is the canonical BCP-47 source language (e.g. "en").
	SourceLanguage string
	// SourceSHA256 is the source clip content hash (the plan's SourceSHA256).
	SourceSHA256 string
	// DurationMS is the source clip duration in milliseconds.
	DurationMS int64

	// OutputProfileHash / RendererVersion / SubtitleStyleHash are the
	// deployment-scoped render facts every plan fingerprints.
	OutputProfileHash string
	RendererVersion   string
	SubtitleStyleHash string
	Watermark         *cliprender.MaterializedAsset
	WatermarkSpec     *cliprender.WatermarkSpec
	WatermarkText     string

	// Background / BackgroundMode are the resolved background selection
	// (materialized asset only for mode=asset).
	Background     *cliprender.MaterializedAsset
	BackgroundMode string

	// SubtitlesStyle is the caller's subtitle visual override block.
	SubtitlesStyle *scriptpkg.VideoVisualStyleSpec
}

// TrackRef is a referenced text track: its canonical ID + content hash. The
// resolver returns these; the plan stores them verbatim (never the text).
type TrackRef struct {
	TrackID int64
	SHA256  string
}

// TrackResolver resolves a READY text track for (asset, language, kind) into
// its canonical (TrackID, SHA256) reference. Fail-closed: no READY track is a
// typed error, never a silent zero reference.
type TrackResolver interface {
	ResolveTrack(ctx context.Context, assetID string, language string, kind detail.TextTrackKind) (*TrackRef, error)
}

// PlanBuilder builds the fingerprinted, priority-ordered []LocalizedClipPlan
// for one source + ordered language request.
type PlanBuilder interface {
	Build(ctx context.Context, source SourceInput, languages []LanguageRequest) ([]LocalizedClipPlan, error)
}

// LocalizationPlanBuilder is the canonical PlanBuilder. It is immutable after
// construction and safe for concurrent Build calls.
type LocalizationPlanBuilder struct {
	tracks TrackResolver
}

// NewLocalizationPlanBuilder builds the canonical plan builder. Fail-closed: a
// nil track resolver is rejected at construction.
func NewLocalizationPlanBuilder(tracks TrackResolver) (*LocalizationPlanBuilder, error) {
	if tracks == nil {
		return nil, fmt.Errorf("localization.NewLocalizationPlanBuilder: track resolver is required")
	}
	return &LocalizationPlanBuilder{tracks: tracks}, nil
}

// Build resolves the source transcript track once, then produces one
// fingerprinted plan per language in REQUEST order. For the source language the
// subtitle track IS the transcript; for every target language it is the
// translated transcript track resolved via the track resolver.
//
// Fail-closed: an incomplete source, a missing source transcript, or a missing
// translated track for any target aborts the whole build (never a partially
// built fan-out).
func (b *LocalizationPlanBuilder) Build(ctx context.Context, source SourceInput, languages []LanguageRequest) ([]LocalizedClipPlan, error) {
	if b == nil || b.tracks == nil {
		return nil, fmt.Errorf("localization: plan builder is not initialized")
	}
	if err := source.validate(); err != nil {
		return nil, err
	}
	if len(languages) == 0 {
		return nil, fmt.Errorf("localization: plan builder: languages is required")
	}

	transcript, err := b.tracks.ResolveTrack(ctx, source.AssetID, source.SourceLanguage, detail.TextTrackTranscript)
	if err != nil {
		return nil, fmt.Errorf("localization: plan builder: resolve source transcript (%s/%s): %w", source.AssetID, source.SourceLanguage, err)
	}
	if transcript == nil || transcript.TrackID <= 0 || strings.TrimSpace(transcript.SHA256) == "" {
		return nil, fmt.Errorf("localization: plan builder: source transcript (%s/%s) is unresolved", source.AssetID, source.SourceLanguage)
	}

	jobID := strings.TrimSpace(source.JobID)
	if jobID == "" {
		jobID = "localize:" + source.AssetID
	}
	clipID := strings.TrimSpace(source.ClipID)
	if clipID == "" {
		clipID = source.AssetID
	}

	plans := make([]LocalizedClipPlan, 0, len(languages))
	for i, lr := range languages {
		lang := strings.TrimSpace(lr.Language)

		// The source language burns its own transcript; every target burns its
		// translated transcript track.
		subtitle := transcript
		if lang != source.SourceLanguage {
			subtitle, err = b.tracks.ResolveTrack(ctx, source.AssetID, lang, detail.TextTrackTranscript)
			if err != nil {
				return nil, fmt.Errorf("localization: plan builder: resolve subtitle track (%s/%s): %w", source.AssetID, lang, err)
			}
			if subtitle == nil || subtitle.TrackID <= 0 || strings.TrimSpace(subtitle.SHA256) == "" {
				return nil, fmt.Errorf("localization: plan builder: subtitle track (%s/%s) is unresolved", source.AssetID, lang)
			}
		}

		plan := LocalizedClipPlan{
			Version:           LocalizedClipPlanVersion,
			JobID:             jobID,
			SceneID:           source.SceneID,
			ClipID:            clipID,
			SourceAssetID:     source.AssetID,
			SourceSHA256:      source.SourceSHA256,
			SourceLanguage:    source.SourceLanguage,
			TargetLanguage:    lang,
			TranscriptTrackID: transcript.TrackID,
			TranscriptSHA256:  transcript.SHA256,
			SubtitleTrackID:   subtitle.TrackID,
			SubtitleSHA256:    subtitle.SHA256,
			SubtitleStyleHash: source.SubtitleStyleHash,
			DurationMS:        source.DurationMS,
			OutputProfileHash: source.OutputProfileHash,
			RendererVersion:   source.RendererVersion,
			Priority:          lr.Priority,
			Watermark:         source.Watermark,
			WatermarkSpec:     source.WatermarkSpec,
			WatermarkText:     source.WatermarkText,
			Background:        source.Background,
			BackgroundMode:    source.BackgroundMode,
			SubtitlesStyle:    source.SubtitlesStyle,
		}
		plan.Fingerprint = Fingerprint(plan)
		if err := plan.Validate(); err != nil {
			return nil, fmt.Errorf("localization: plan builder: plan %d (%s): %w", i, lang, err)
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

// validate fails closed on an incomplete source: every fact the plans must
// fingerprint is required before any track is resolved.
func (s SourceInput) validate() error {
	if strings.TrimSpace(s.AssetID) == "" {
		return fmt.Errorf("localization: plan builder: source asset_id is required")
	}
	if strings.TrimSpace(s.SourceLanguage) == "" {
		return fmt.Errorf("localization: plan builder: source_language is required")
	}
	if !isSHA256Hex(s.SourceSHA256) {
		return fmt.Errorf("localization: plan builder: source_sha256 must be a 64-hex SHA-256 (got %q)", s.SourceSHA256)
	}
	if s.DurationMS <= 0 {
		return fmt.Errorf("localization: plan builder: duration_ms must be > 0 (got %d)", s.DurationMS)
	}
	if strings.TrimSpace(s.OutputProfileHash) == "" {
		return fmt.Errorf("localization: plan builder: output_profile_hash is required")
	}
	if strings.TrimSpace(s.RendererVersion) == "" {
		return fmt.Errorf("localization: plan builder: renderer_version is required")
	}
	return nil
}
