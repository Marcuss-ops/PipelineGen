package audio

import kernelasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"

// ClipAssetMetadata carries canonical, pre-resolved clip asset facts.
// Duration is the certified total source duration of the complete asset, with
// provenance (probe / provider_metadata / unknown). Consumers read the Known
// flag to distinguish "we don't know" from a real (possibly zero-length)
// duration.
type ClipAssetMetadata struct {
	AssetID  string                    `json:"asset_id"`
	Duration kernelasset.AssetDuration `json:"duration"`
}

// DocumentAudioSummary is a pre-computed aggregate of audio facts. It is
// resolved once at the capability boundary so consumers do not sum durations
// across scenes themselves. ClipTotalKnown=false means at least one clip
// carried no known total duration.
type DocumentAudioSummary struct {
	ClipCount        int   `json:"clip_count"`
	ClipTotalUS      int64 `json:"clip_total_us"`
	ClipTotalKnown   bool  `json:"clip_total_known"`
	VoiceoverCount   int   `json:"voiceover_count"`
	VoiceoverTotalUS int64 `json:"voiceover_total_us"`
}
