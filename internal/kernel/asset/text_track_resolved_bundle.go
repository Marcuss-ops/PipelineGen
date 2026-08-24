package asset

// text_track_resolved_bundle.go defines the canonical typed result of
// the text-track acquisition pipeline (PR-PY-CLIPS-CORRETTE-TRADOTTE,
// Fase 1.a, July 2026).
//
// The acquisition pipeline (TextTrackResolver.AcquireSegmentText) returns
// a *ResolvedTextBundle so handlers carry both the plain transcript (for
// LLM indexing + search-text composition) and the timed cues (for video
// re-rendering + future caption styling) in ONE typed struct.
//
// godlike/06 SSOT: this is the SOLE canonical shape. Handlers MUST NOT
// reconstruct bundles inline; they MUST call the acquisition port
// (TextTrackResolver.AcquireSegmentText) and forward the typed result.
// Reconstructing the bundle in handlers was the root cause of the
// pre-PR drift: step6to9 inlined Subtitles.SliceSubtitles + Whisper
// fallback while the resolver covered payload+DB only, leaving the
// YouTube-subtitle path SKIPPED and the Whisper path duplicated with
// step10 (see godlike/audit 2026-07-11 §2.b).

// ResolvedTextBundle is the canonical typed result of the text-track
// acquisition pipeline. It carries both the plain transcript (for LLM
// indexing + search-text) and the timed cues (for video re-rendering
// + future caption styling).
//
// The bundle represents the PRIMARY (original-language) track acquired
// for a single clip. The caller (Step 6-9) is responsible for ALSO
// materializing any additional Segment.Texts[] entries via
// TextTrackResolver.SaveMany so the multilingua coverage stays
// first-class in asset_text_tracks.
type ResolvedTextBundle struct {
	// LanguageCode is the BCP-47 code of the resolved primary text.
	// Empty when the bundle is "NotFound" (no acquisition path
	// produced usable content).
	LanguageCode string

	// SourceLanguageCode is the BCP-47 code of the LANGUAGE the
	// text was originally produced IN. For an original YouTube
	// subtitle or a Whisper transcript, LanguageCode and
	// SourceLanguageCode coincide. For a translation row they differ.
	SourceLanguageCode string

	// PlainText is the concatenated transcript (no timestamps). It
	// is the text used as Qdrant-transcript-embedding + LLM
	// summarization input. Empty when acquisition failed.
	PlainText string

	// Cues is the per-segment timed text. Nil/empty when the source
	// is payload-text, DB-stored text, or Whisper (Whisper returns
	// a single block, no per-segment timing). Populated when the
	// source is a parsed VTT (YouTube subtitles).
	//
	// godlike/06 SSOT: this slice is the canonical input for the
	// (Fase 2) asset_text_track_segments table. Cues with SequenceNo
	// assigned at persist time (NOT derived from array index — see
	// PR-CLIPTRACKS-SEGMENTS-MIGRATION, July 2026).
	Cues []TimedCue

	// SourceType is the provenance of the resolved text.
	// Maps to asset.TextTrackSource. Drives future
	// provider/model-version stamping on derived translations.
	SourceType TextTrackSource

	// IsOriginal is true when the text was produced in the
	// clip's ORIGINAL language (not derived via translation).
	// Originals: payload-provided, YouTube subtitle, Whisper
	// original speech. Non-originals: translations.
	IsOriginal bool

	// Provider is the source-system label (e.g. "yt-dlp" for
	// YouTube subtitles, "whisper" for Whisper, "ollama" for an
	// upstream translation). Empty when provenance is not
	// attributed to a single provider.
	Provider string

	// ModelName is the model label the provider used (e.g.
	// "gpt-4o-mini", "whisper-large-v3", "qwen2.5"). Empty
	// when the provider doesn't expose a model name (e.g.
	// payload-provided).
	ModelName string

	// ModelVersion is the provider's version of the model.
	// Stable across model upgrades — pairs with ModelName to
	// stamp SourceVersion.
	ModelVersion string

	// Confidence is the provider-reported confidence in [0, 1].
	// Nil when the provider doesn't report a confidence score.
	Confidence *float64
}

// TimedCue represents a single subtitle cue with millisecond
// precision. Used by the (Fase 2) asset_text_track_segments table —
// each cue becomes one timed row preserving sequence_no (persisted by
// the DB layer) along with start_ms + end_ms + text.
//
// In-memory SequenceNo is intentionally absent here: assignment
// happens at the persistence step (UPSERT order), NOT from the array
// index of the in-memory bundle, so reordering at the resolver layer
// (e.g. duplicate-stripped cues) does not destabilize the FK.
type TimedCue struct {
	StartMs int64  `json:"start_ms"`
	EndMs   int64  `json:"end_ms"`
	Text    string `json:"text"`
}

// IsEmpty reports whether the bundle carries no usable content.
// Empty bundles signal the caller that acquisition failed
// across all priority levels (payload, DB, YT subtitle, Whisper)
// and that PENDING/FAILED persistence + Qdrant indexing should NOT
// proceed via the primary track path.
func (b *ResolvedTextBundle) IsEmpty() bool {
	if b == nil {
		return true
	}
	return b.PlainText == "" && len(b.Cues) == 0
}
