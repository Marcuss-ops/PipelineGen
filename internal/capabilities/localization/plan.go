// plan.go owns LocalizedClipPlan v1 — the canonical contract that means
// "create THIS clip in THIS language". It is the single shape every step of
// the localization fan-out speaks:
//
//	ResolvedTextBundle → TextTrack original → Translation Resolver
//	  → TextTrack (target) → LocalizedClipPlan → Fingerprint → queue
//	  → RenderPlan → Rust → LocalizedClipArtifact → Drive → Docs
//
// godlike/06 SSOT (one canonical owner per fact):
//   - LocalizedClipPlan is the SINGLE contract for a localized render. There
//     is no CreateEnglishClip / CreateSpanishClip / CreateItalianClip — only
//     RenderLocalizedClip(plan) where TargetLanguage / Priority /
//     SubtitleTrackID / SubtitleSHA256 change.
//   - The plan REFERENCES text tracks by (ID, SHA256) instead of embedding the
//     translated text: detail.TextTrack stays the owner of the translation
//     content (transcript + translated track), so the text is never duplicated
//     across DB / request / render plan / document model.
//   - Fingerprint and Validate() are the canonical single owners of their
//     facts: the fingerprint is computed by ONE function (fingerprint.go),
//     never re-derived per-runner/renderer/uploader/docs-writer, and
//     Validate() gates the plan fail-closed before Rust starts.
package localization

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// LocalizedClipPlanVersion is the canonical version of the localized-clip
// plan contract. It is a fingerprint input (contract_version) and the
// Validate() gate: a plan carrying any other version is rejected.
const LocalizedClipPlanVersion = "localized-clip-plan.v1"

// LocalizedClipPlan is the canonical sealed contract handed to the
// localization compiler (LocalizedClipPlan → RenderPlan). Every business
// selection — which source, which target language, which transcript/subtitle
// tracks, which style and output profile, which renderer, at which priority —
// is resolved BEFORE this plan is built; the compiler and Rust execute it
// verbatim, never re-resolving content.
type LocalizedClipPlan struct {
	// Version pins the contract shape. Always LocalizedClipPlanVersion.
	Version string `json:"version"`

	// ── Identity ─────────────────────────────────────────────────
	// JobID correlates the plan to its enclosing Master job.
	JobID string `json:"job_id"`
	// SceneID is the editorial scene the clip belongs to (may be empty
	// for standalone clips).
	SceneID string `json:"scene_id"`
	// ClipID is the canonical clip identity being localized.
	ClipID string `json:"clip_id"`

	// ── Source clip ──────────────────────────────────────────────
	// SourceAssetID is the canonical source clip asset id.
	SourceAssetID string `json:"source_asset_id"`
	// SourceSHA256 is the source clip content hash. It is the first
	// fingerprint input and certifies the render reads the expected bytes.
	SourceSHA256 string `json:"source_sha256"`

	// ── Languages (BCP-47) ───────────────────────────────────────
	// SourceLanguage is the canonical BCP-47 source language (e.g. "en").
	SourceLanguage string `json:"source_language"`
	// TargetLanguage is the BCP-47 language this plan renders into (e.g.
	// "es"). It is the one field that differs between per-language plans.
	TargetLanguage string `json:"target_language"`

	// ── Text tracks (referenced, never embedded) ─────────────────
	// TranscriptTrackID + TranscriptSHA256 reference the canonical source
	// transcript track (source-language text + timing). The resolver
	// fetches the content by these references; the plan never carries the
	// raw text.
	TranscriptTrackID int64  `json:"transcript_track_id"`
	TranscriptSHA256  string `json:"transcript_sha256"`

	// SubtitleTrackID + SubtitleSHA256 reference the translated
	// target-language text track. The resolver fetches the content and
	// compiles the .ass from it — this keeps the translation owned by
	// detail.TextTrack, not duplicated into the plan.
	SubtitleTrackID int64  `json:"subtitle_track_id"`
	SubtitleSHA256  string `json:"subtitle_sha256"`

	// ── Subtitle style ───────────────────────────────────────────
	// SubtitleStyleHash is the canonical ASS style + generator hash baked
	// into the .ass for this plan. It is a fingerprint input: changing the
	// style bumps every variant fingerprint.
	SubtitleStyleHash string `json:"subtitle_style_hash"`

	// ── Timing ───────────────────────────────────────────────────
	// DurationMS is the source clip duration in milliseconds. It is the
	// render window and the post-render drift tolerance reference.
	DurationMS int64 `json:"duration_ms"`

	// ── Render contract ──────────────────────────────────────────
	// OutputProfileHash identifies the canonical render output profile
	// (codec/pixel/resolution). It is a fingerprint input.
	OutputProfileHash string `json:"output_profile_hash"`
	// RendererVersion pins the renderer binary/behavior version. It is a
	// fingerprint input.
	RendererVersion string `json:"renderer_version"`

	// ── Queue ────────────────────────────────────────────────────
	// Priority is the render-queue priority (0 renders first, 1..N in
	// editorial order). It is copied verbatim from the localization
	// request and drives the deterministic report/docs order.
	Priority int `json:"priority"`

	Watermark     *cliprender.MaterializedAsset `json:"watermark,omitempty"`
	WatermarkSpec *cliprender.WatermarkSpec     `json:"watermark_spec,omitempty"`
	WatermarkText string                        `json:"watermark_text,omitempty"`

	// ── Background (visual layer behind the source) ───────────────
	// BackgroundMode is the request-level background selection
	// (none | blur_source | asset; "" normalises to none). Background is
	// the materialized asset, non-nil ONLY for mode=asset — exactly the
	// shape CompileInput expects, so the localized fan-out loses nothing.
	BackgroundMode string                        `json:"background_mode,omitempty"`
	Background     *cliprender.MaterializedAsset `json:"background,omitempty"`

	// ── Subtitle visual overrides ────────────────────────────────
	// SubtitlesStyle carries the caller's explicit subtitle visual block
	// (color, size, shadow, transition). It rides alongside the compiled
	// ASS (SubtitleStyleHash) so the sealed plan exposes the same style
	// facts to every render boundary.
	SubtitlesStyle *scriptpkg.VideoVisualStyleSpec `json:"subtitles_style,omitempty"`

	// ── Canonical fingerprint ────────────────────────────────────
	// Fingerprint is the canonical plan digest, computed by the SINGLE
	// Fingerprint(plan) function. It is persisted on the plan and re-checked
	// by Validate() so no component recomputes its own variant.
	Fingerprint string `json:"fingerprint"`
}

// ErrInvalidLocalizedClipPlan wraps every structural validation failure of a
// LocalizedClipPlan. Callers use errors.Is for classification; the message
// carries the human-readable reason.
var ErrInvalidLocalizedClipPlan = errors.New("invalid localized clip plan")

// ErrLocalizedClipPlanFingerprintMismatch is returned when the plan's stored
// Fingerprint does not equal the recomputed canonical digest (tamper /
// partial-mutation detection, mirror of render.ErrPlanDrift).
var ErrLocalizedClipPlanFingerprintMismatch = errors.New("localized clip plan fingerprint mismatch")

// Validate enforces the LocalizedClipPlan contract fail-closed, BEFORE Rust
// is ever invoked. Any violation returns a typed error; the plan is never
// partially accepted (godlike/07 fail-closed). Checks, in canonical order:
//
//	Version == LocalizedClipPlanVersion
//	JobID, ClipID, SourceAssetID present
//	SourceSHA256 is a 64-hex SHA-256
//	TargetLanguage is a valid BCP-47 tag (not "und")
//	TranscriptTrackID > 0, TranscriptSHA256 present
//	SubtitleTrackID > 0, SubtitleSHA256 present
//	DurationMS > 0
//	RendererVersion, OutputProfileHash present
//	Fingerprint == Fingerprint(plan) (recomputed)
//
// SceneID, SourceLanguage, SubtitleStyleHash and Priority are intentionally
// not gated here: SceneID is optional for standalone clips, SourceLanguage is
// captured by TranscriptSHA256, and Priority is enforced at the request
// boundary. They fold into the fingerprint where they matter.
func (p LocalizedClipPlan) Validate() error {
	if p.Version != LocalizedClipPlanVersion {
		return fmt.Errorf("%w: version must be %q (got %q)", ErrInvalidLocalizedClipPlan, LocalizedClipPlanVersion, p.Version)
	}
	if strings.TrimSpace(p.JobID) == "" {
		return fmt.Errorf("%w: job_id is required", ErrInvalidLocalizedClipPlan)
	}
	if strings.TrimSpace(p.ClipID) == "" {
		return fmt.Errorf("%w: clip_id is required", ErrInvalidLocalizedClipPlan)
	}
	if strings.TrimSpace(p.SourceAssetID) == "" {
		return fmt.Errorf("%w: source_asset_id is required", ErrInvalidLocalizedClipPlan)
	}
	if !isSHA256Hex(p.SourceSHA256) {
		return fmt.Errorf("%w: source_sha256 must be a 64-hex SHA-256 (got %q)", ErrInvalidLocalizedClipPlan, p.SourceSHA256)
	}
	if err := validateBCP47(p.TargetLanguage); err != nil {
		return fmt.Errorf("%w: target_language %v", ErrInvalidLocalizedClipPlan, err)
	}
	if p.TranscriptTrackID <= 0 {
		return fmt.Errorf("%w: transcript_track_id must be > 0 (got %d)", ErrInvalidLocalizedClipPlan, p.TranscriptTrackID)
	}
	if strings.TrimSpace(p.TranscriptSHA256) == "" {
		return fmt.Errorf("%w: transcript_sha256 is required", ErrInvalidLocalizedClipPlan)
	}
	if p.SubtitleTrackID <= 0 {
		return fmt.Errorf("%w: subtitle_track_id must be > 0 (got %d)", ErrInvalidLocalizedClipPlan, p.SubtitleTrackID)
	}
	if strings.TrimSpace(p.SubtitleSHA256) == "" {
		return fmt.Errorf("%w: subtitle_sha256 is required", ErrInvalidLocalizedClipPlan)
	}
	if p.DurationMS <= 0 {
		return fmt.Errorf("%w: duration_ms must be > 0 (got %d)", ErrInvalidLocalizedClipPlan, p.DurationMS)
	}
	if p.Watermark != nil {
		if strings.TrimSpace(p.Watermark.AssetID) == "" || strings.TrimSpace(p.Watermark.LocalPath) == "" || !isSHA256Hex(p.Watermark.SHA256) {
			return fmt.Errorf("%w: watermark materialized asset is incomplete", ErrInvalidLocalizedClipPlan)
		}
		if p.WatermarkSpec == nil || strings.TrimSpace(p.WatermarkSpec.AssetID) == "" {
			return fmt.Errorf("%w: watermark specification is incomplete", ErrInvalidLocalizedClipPlan)
		}
	}
	if strings.TrimSpace(p.WatermarkText) != "" && p.WatermarkSpec == nil {
		return fmt.Errorf("watermark text requires watermark spec")
	}
	if p.Background != nil {
		if p.BackgroundMode != cliprender.BackgroundModeAsset {
			return fmt.Errorf("%w: background materialized asset requires mode=asset (got %q)", ErrInvalidLocalizedClipPlan, p.BackgroundMode)
		}
		if strings.TrimSpace(p.Background.AssetID) == "" || strings.TrimSpace(p.Background.LocalPath) == "" || !isSHA256Hex(p.Background.SHA256) {
			return fmt.Errorf("%w: background materialized asset is incomplete", ErrInvalidLocalizedClipPlan)
		}
	}
	if p.BackgroundMode == cliprender.BackgroundModeAsset && p.Background == nil {
		return fmt.Errorf("%w: background mode=asset requires the materialized asset", ErrInvalidLocalizedClipPlan)
	}
	if strings.TrimSpace(p.RendererVersion) == "" {
		return fmt.Errorf("%w: renderer_version is required", ErrInvalidLocalizedClipPlan)
	}
	if strings.TrimSpace(p.OutputProfileHash) == "" {
		return fmt.Errorf("%w: output_profile_hash is required", ErrInvalidLocalizedClipPlan)
	}
	if expected := Fingerprint(p); p.Fingerprint != expected {
		return fmt.Errorf("%w: got %q want %q", ErrLocalizedClipPlanFingerprintMismatch, p.Fingerprint, expected)
	}
	return nil
}

// validateBCP47 rejects an empty, malformed, or undetermined language tag. It
// reuses asset.Normalize (the canonical BCP-47 owner) so no other file
// re-derives the tag rules.
func validateBCP47(code string) error {
	if strings.TrimSpace(code) == "" {
		return fmt.Errorf("is required")
	}
	norm, err := asset.Normalize(code)
	if err != nil {
		return err
	}
	if norm == "und" {
		return fmt.Errorf("resolves to undetermined (und)")
	}
	return nil
}

// isSHA256Hex reports whether value is a 64-character lowercase hex string
// (the canonical SHA-256 digest shape). Mirrors the cliprender gate so the
// two render contracts share one digest-shape rule.
func isSHA256Hex(value string) bool {
	if len(value) != digest.SHA256HexLength {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && strings.ToLower(value) == value && len(decoded) == digest.SHA256HexLength/2
}
