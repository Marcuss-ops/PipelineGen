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
	"crypto/sha256"
	"encoding/hex"
	"strings"
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
		strings.TrimSpace(plan.Version),
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}
