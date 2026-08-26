package detail

import (
	"context"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// RenderVariantStatus is the lifecycle of a rendered per-language clip
// variant (migrations/sqlite/219_asset_render_variants.sql).
type RenderVariantStatus string

const (
	RenderVariantPending RenderVariantStatus = "PENDING"
	RenderVariantReady   RenderVariantStatus = "READY"
	RenderVariantFailed  RenderVariantStatus = "FAILED"
)

// RenderProfileFFmpegAss1080pV1 is the canonical render profile for the
// ffmpeg subtitles-filter burn-in path: scale+pad to 1920x1080 so the
// canonical ASS (PlayRes 1920x1080) renders legibly, libx264 + copy audio.
// Bumping this value in the fingerprint invalidates every cached variant.
const RenderProfileFFmpegAss1080pV1 = "ffmpeg-ass-1080p-v1"

// RenderVariant is one rendered, validated, uploaded per-language clip
// variant. Maps 1:1 to a row in asset_render_variants.
type RenderVariant struct {
	ID                   int64               `json:"id"`
	SourceClipID         string              `json:"source_clip_id"`
	LanguageCode         string              `json:"language_code"`
	Fingerprint          string              `json:"fingerprint"`
	SourceClipSHA256     string              `json:"source_clip_sha256"`
	TranscriptSHA256     string              `json:"transcript_sha256"`
	TranslationVersion   string              `json:"translation_version"`
	SubtitleStyleVersion string              `json:"subtitle_style_version"`
	RenderProfileVersion string              `json:"render_profile_version"`
	SubtitleHash         string              `json:"subtitle_hash"`
	OutputHash           string              `json:"output_hash"`
	DriveFileID          string              `json:"drive_file_id"`
	DriveLink            string              `json:"drive_link"`
	DurationMs           int64               `json:"duration_ms"`
	SizeBytes            int64               `json:"size_bytes"`
	Status               RenderVariantStatus `json:"status"`
	ValidationError      string              `json:"validation_error"`
	IsCurrent            bool                `json:"is_current"`
	CreatedAt            time.Time           `json:"created_at"`
	UpdatedAt            time.Time           `json:"updated_at"`
}

// RenderVariantRepository is the canonical port for persisting and querying
// rendered per-language clip variants.
type RenderVariantRepository interface {
	// Upsert inserts or updates a variant row. When IsCurrent is true the
	// prior is_current=1 row for the same (source_clip_id, language_code)
	// is flipped to 0 within the same transaction.
	Upsert(ctx context.Context, v *RenderVariant) error

	// FindCurrent returns the is_current=1 row for (source_clip_id,
	// language_code), or (nil, nil) when none exists.
	FindCurrent(ctx context.Context, sourceClipID, languageCode string) (*RenderVariant, error)

	// FindByFingerprint returns the row matching the exact fingerprint for
	// (source_clip_id, language_code), or (nil, nil) when none exists.
	FindByFingerprint(ctx context.Context, sourceClipID, languageCode, fingerprint string) (*RenderVariant, error)

	// ListBySourceClip returns all variants for a source clip, newest first.
	ListBySourceClip(ctx context.Context, sourceClipID string) ([]RenderVariant, error)
}

// RenderVariantFingerprint is the canonical deterministic identity of a
// rendered per-language variant. Same inputs → same fingerprint, so a re-run
// can skip translate/ASS/render/upload when nothing changed. The formula is
// owned here (godlike/06 SSOT): callers MUST NOT re-derive it inline.
func RenderVariantFingerprint(
	sourceClipSHA256, transcriptSHA256, targetLanguage,
	translationVersion, subtitleStyleVersion, renderProfileVersion string,
) string {
	return digest.Fingerprint(
		strings.TrimSpace(sourceClipSHA256),
		strings.TrimSpace(transcriptSHA256),
		strings.TrimSpace(targetLanguage),
		strings.TrimSpace(translationVersion),
		strings.TrimSpace(subtitleStyleVersion),
		strings.TrimSpace(renderProfileVersion),
	)
}

// RenderVariantContentFingerprint identifies the rendered pixels by their
// content inputs. Translation provider/model metadata is deliberately not
// included: when the localized TextTrack hash is identical, the subtitles
// and resulting MP4 are identical even if the provider version changed.
func RenderVariantContentFingerprint(
	sourceClipSHA256, transcriptSHA256, targetLanguage,
	subtitleStyleVersion, renderProfileVersion string,
) string {
	return digest.Fingerprint(
		strings.TrimSpace(sourceClipSHA256),
		strings.TrimSpace(transcriptSHA256),
		strings.TrimSpace(targetLanguage),
		strings.TrimSpace(subtitleStyleVersion),
		strings.TrimSpace(renderProfileVersion),
	)
}
