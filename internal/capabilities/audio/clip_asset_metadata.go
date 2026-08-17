package audio

import kernelasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"

// ClipAssetMetadata is the canonical, pre-resolved clip-asset fact projected
// into a document. Duration is the certified total source duration of the
// complete asset, with provenance (probe / provider_metadata / unknown).
// Renderers only format this value; they never convert or derive durations
// themselves, and they read the Known flag to distinguish "we don't know"
// from a real (possibly zero-length) duration.
type ClipAssetMetadata struct {
	AssetID  string                    `json:"asset_id"`
	Duration kernelasset.AssetDuration `json:"duration"`
}

// DocumentAudioSummary is the pre-computed aggregate of the audio facts the
// document renderer projects verbatim. It is resolved once at the capability
// boundary (not inside the renderer) so the renderer never sums durations
// across scenes itself. ClipTotalKnown=false means at least one clip carried
// no known total duration, so the renderer formats "Unknown" instead of a
// fabricated total.
type DocumentAudioSummary struct {
	ClipCount        int   `json:"clip_count"`
	ClipTotalUS      int64 `json:"clip_total_us"`
	ClipTotalKnown   bool  `json:"clip_total_known"`
	VoiceoverCount   int   `json:"voiceover_count"`
	VoiceoverTotalUS int64 `json:"voiceover_total_us"`
}
