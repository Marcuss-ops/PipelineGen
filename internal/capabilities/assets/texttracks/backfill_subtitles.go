// Package texttracks — backfill_subtitles.go: the canonical per-language
// subtitle-artifact delivery step.
//
// POSTGRES-MEDIA-CUTOVER, September 2026. This step used to live inline in
// ProcessAsset. It is extracted here because it has TWO callers that must
// produce byte-identical artifacts:
//
//  1. the operator backfill (ProcessAsset), which walks the catalog, and
//  2. the `asset.text.materialize` job handler's fast path, which the direct
//     YouTube pipeline now fans out to after every successful clip commit.
//
// Before this extraction only (1) existed. A YouTube clip committed WITH a
// transcript took the fast path — the fan-out carries the committed source
// hash, so the handler never re-enters ProcessAsset — with the result that
// the clip got its 10 language rows but never got the 10 subtitle files
// uploaded to Drive. Two copies of this loop would have drifted (the style
// id, the content-hash fallback and the READY-language set are all
// correctness-relevant), so the loop has exactly one owner.
//
// godlike/06 SSOT: this file is the SOLE canonical owner of
// "READY text tracks → .ass artifacts → Drive + subtitle_artifacts registry".
// Neither caller may re-implement any part of it.
package texttracks

// SubtitleArtifactStyleID is the canonical ASS style for generated subtitle
// artifacts. Named here so the direct YouTube path and the operator backfill
// cannot disagree about it.
const SubtitleArtifactStyleID = "vidrush-default"

// SubtitleDeliveryReport is the per-clip result of the subtitle-artifact
// delivery step. A failure is recorded per language rather than returned as
// a fatal error: the text tracks are already durable at this point, so an
// upload problem must not fail the whole materialization. The caller decides
// whether to surface it.
type SubtitleDeliveryReport struct {
	// Delivered counts the languages whose artifact reached the registry
	// (and Drive, when a publisher is wired).
	Delivered int `json:"delivered"`
	// Languages lists the languages considered for delivery.
	Languages []string `json:"languages,omitempty"`
	// Failed maps language → error, for the artifacts that did not make it.
	Failed map[string]string `json:"failed,omitempty"`
	// UnTimed maps language → reason for a language that HAS text but no
	// timed cues, so no artifact could be built (and the alignment pass
	// could not repair it). Reported separately from Failed because the
	// text tracks are valid — this is "subtitle not ready", not "upload
	// failed". Named explicitly so a clip that loses nine of its ten
	// artifacts can never be mistaken for a complete one.
	UnTimed map[string]string `json:"untimed,omitempty"`
	// Skipped reports that no artifact was attempted at all.
	Skipped bool `json:"skipped"`
	// SkipReason names why, so "not applicable" is never confused with
	// "silently did nothing".
	SkipReason string `json:"skip_reason,omitempty"`
}
