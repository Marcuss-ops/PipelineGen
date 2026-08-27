package localization

// fingerprint.go owns the canonical LocalizedClipPlan fingerprint — the
// deterministic identity of "create THIS clip in THIS language". It is the
// SINGLE function that computes the digest (godlike/06 SSOT): the runner, the
// renderer, the Drive uploader, and the Docs writer all consume the value
// stored on plan.Fingerprint and MUST NOT re-derive their own variant.
//
// The digest folds exactly the eight facts that change the rendered bytes
// (the plan's canonical fingerprint contract):
//
//	source_asset_sha256   → SourceSHA256       (which source bytes)
//	transcript_text_hash  → TranscriptSHA256   (which source text/timing)
//	translated_track_hash → SubtitleSHA256     (which translated text)
//	target_language       → TargetLanguage     (which language is burned)
//	subtitle_style_hash   → SubtitleStyleHash  (which ASS style)
//	output_profile_hash   → OutputProfileHash  (which codec/geometry)
//	renderer_version      → RendererVersion    (which renderer behavior)
//	contract_version      → Version            (which plan contract)
//
// Fields that do NOT change the rendered bytes are deliberately excluded:
// JobID, SceneID, ClipID, SourceAssetID, SourceLanguage, the track IDs,
// DurationMS, and Priority are identity/editorial metadata, not render
// content. plan.Fingerprint itself is the output, never an input.
//
// Same inputs → same fingerprint, so a re-run can skip translate/ASS/render/
// upload when nothing changed. Parts are joined with a NUL byte so adjacent
// fields cannot collide ("a|b" + "c" ≠ "a" + "b|c"); the input values are
// already canonical (BCP-47 tag, hex hashes) at the plan boundary.

import (
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// Fingerprint returns the canonical deterministic SHA-256 hex digest of the
// plan's render-relevant facts. The plan.Fingerprint field is NOT read — it
// is the value this function produces, so computing the fingerprint never
// depends on a previously-stored fingerprint (idempotent, drift-free).
func Fingerprint(plan LocalizedClipPlan) string {
	parts := []string{
		strings.TrimSpace(plan.SourceSHA256),
		strings.TrimSpace(plan.TranscriptSHA256),
		strings.TrimSpace(plan.SubtitleSHA256),
		strings.TrimSpace(plan.TargetLanguage),
		strings.TrimSpace(plan.SubtitleStyleHash),
		strings.TrimSpace(plan.OutputProfileHash),
		strings.TrimSpace(plan.RendererVersion),
		watermarkFingerprint(plan),
		backgroundFingerprint(plan),
		subtitleStyleFingerprint(plan),
		strings.TrimSpace(plan.Version),
	}
	return digest.Fingerprint(parts...)
}

func watermarkFingerprint(plan LocalizedClipPlan) string {
	if plan.WatermarkSpec == nil {
		return ""
	}
	assetID, sha := "", ""
	if plan.Watermark != nil {
		assetID, sha = plan.Watermark.AssetID, plan.Watermark.SHA256
	}
	return strings.Join([]string{
		strings.TrimSpace(assetID),
		strings.TrimSpace(sha),
		strings.TrimSpace(plan.WatermarkSpec.Text),
		strings.TrimSpace(plan.WatermarkSpec.Position),
		fmt.Sprintf("%.6f", plan.WatermarkSpec.Opacity),
		fmt.Sprintf("%d", plan.WatermarkSpec.MarginPX),
		visualStyleFingerprint(plan.WatermarkSpec.Style),
	}, "\x1f")
}

// backgroundFingerprint folds the background selection: the mode plus (for
// mode=asset) the content-addressed asset identity. It is part of the digest
// because the rendered bytes change with the background.
func backgroundFingerprint(plan LocalizedClipPlan) string {
	mode := strings.TrimSpace(plan.BackgroundMode)
	if mode == "" {
		mode = "none"
	}
	assetID, sha := "", ""
	if plan.Background != nil {
		assetID, sha = plan.Background.AssetID, plan.Background.SHA256
	}
	return strings.Join([]string{
		mode,
		strings.TrimSpace(assetID),
		strings.TrimSpace(sha),
	}, "\x1f")
}

// subtitleStyleFingerprint folds the caller's subtitle visual overrides.
// They change the burned subtitle pixels, so they are render content — never
// editorial metadata.
func subtitleStyleFingerprint(plan LocalizedClipPlan) string {
	return visualStyleFingerprint(plan.SubtitlesStyle)
}

// visualStyleFingerprint canonicalizes a VisualStyleSpec into a stable string
// (hex color, pixel sizes, %-scale, shadow, transition). Floats are formatted
// with a fixed precision so equivalent values fold identically.
func visualStyleFingerprint(s *scriptpkg.VideoVisualStyleSpec) string {
	if s == nil {
		return ""
	}
	parts := []string{
		strings.TrimSpace(s.Color),
		fmt.Sprintf("%.6f", s.FontSizePX),
		fmt.Sprintf("%d", s.WidthPX),
		fmt.Sprintf("%d", s.HeightPX),
		fmt.Sprintf("%.6f", s.ScalePercent),
	}
	if s.Shadow != nil {
		parts = append(parts, strings.Join([]string{
			strings.TrimSpace(s.Shadow.Color),
			fmt.Sprintf("%.6f", s.Shadow.Opacity),
			fmt.Sprintf("%.6f", s.Shadow.BlurPX),
			fmt.Sprintf("%.6f", s.Shadow.OffsetX),
			fmt.Sprintf("%.6f", s.Shadow.OffsetY),
		}, "\x1e"))
	}
	if s.TransitionIn != nil {
		parts = append(parts, strings.Join([]string{
			strings.TrimSpace(s.TransitionIn.Preset),
			fmt.Sprintf("%d", s.TransitionIn.DurationMS),
		}, "\x1e"))
	}
	return strings.Join(parts, "\x1f")
}
