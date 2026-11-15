// Package audio owns the delivery-audio DTOs shared by the audio compiler,
// the combined renderer and final-job assembly. It has no root-package
// dependency; implementations consume these types through ports.
package audio

// FinalAudioReference is the certified delivery-audio reference. It is the
// contract between the audio compiler, the renderer and final-job assembly.
type FinalAudioReference struct {
	AssetID string `json:"audio_asset_id"`
	// Filename is the caller-facing output name used when publishing the
	// certified master. It is derived from the payload title, never a generic
	// final_audio name.
	Filename string `json:"filename,omitempty"`
	Path     string `json:"path,omitempty"`
	// DriveFileID is an in-process publication fact; DriveLink remains the
	// wire location so one fact has one owner (media-identity gate).
	DriveFileID          string `json:"-"`
	DriveLink            string `json:"drive_link,omitempty"`
	Container            string `json:"container,omitempty"`
	AudioContractVersion string `json:"audio_contract_version,omitempty"`
	AudioPlanVersion     string `json:"audio_plan_version,omitempty"`
	PlanSHA256           string `json:"audio_plan_sha256"`
	FinalAudioSHA256     string `json:"final_audio_sha256"`
	Codec                string `json:"codec,omitempty"`
	Profile              string `json:"profile,omitempty"`
	SampleRate           int    `json:"sample_rate,omitempty"`
	Channels             int    `json:"channels,omitempty"`
	ChannelLayout        string `json:"channel_layout,omitempty"`
	Bitrate              int64  `json:"bitrate,omitempty"`
	DurationUS           int64  `json:"duration_us,omitempty"`
	DurationMS           int64  `json:"duration_ms"`
	StartPTS             int64  `json:"start_pts,omitempty"`
	SizeBytes            int64  `json:"size_bytes,omitempty"`
	FinalMix             bool   `json:"final_mix,omitempty"`
	CopyEligible         bool   `json:"copy_eligible"`
}

// CompileTimings captures the per-stage wall-clock of combined-audio
// compilation: asset resolution, timeline build, clip preparation and final
// audio-plan compile.
type CompileTimings struct {
	AudioAssetResolveMS int64
	TimelineCompileMS   int64
	ClipAudioPrepareMS  int64
	AudioPlanCompileMS  int64
}
