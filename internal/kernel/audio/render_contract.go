package audio

type FinalAudioReference struct {
	AssetID              string `json:"audio_asset_id"`
	Filename             string `json:"filename,omitempty"`
	Path                 string `json:"path,omitempty"`
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

// Language is the delivery language of a rendered metric row.
type Language string

// AudioPipelineMetrics is the audio observability projection.
type AudioPipelineMetrics struct {
	TTSMS                int64             `json:"tts_ms"`
	MediaFetchMS         int64             `json:"media_fetch_ms"`
	AudioAssetResolveMS  int64             `json:"audio_asset_resolve_ms"`
	TimelineCompileMS    int64             `json:"timeline_compile_ms"`
	AudioPlanCompileMS   int64             `json:"audio_plan_compile_ms"`
	ClipAudioPrepareMS   int64             `json:"clip_audio_prepare_ms"`
	PrepareMS            int64             `json:"prepare_ms"`
	LoudnessMS           int64             `json:"loudness_ms"`
	MixMS                int64             `json:"mix_ms"`
	AACEncodeMS          int64             `json:"aac_encode_ms"`
	ProbeMS              int64             `json:"probe_ms"`
	HashMS               int64             `json:"hash_ms"`
	UploadMS             int64             `json:"upload_ms"`
	TotalMS              int64             `json:"total_ms"`
	AudioDurationMS      int64             `json:"audio_duration_ms"`
	TTSCalls             int               `json:"tts_calls"`
	AudioRTF             float64           `json:"audio_rtf"`
	AudioSpeed           float64           `json:"audio_speed"`
	AudioEncodePasses    int               `json:"audio_encode_passes"`
	TTSScenes            []TTSSSceneMetric `json:"tts_scenes,omitempty"`
	VoiceoverRequested   int               `json:"voiceover_requested,omitempty"`
	VoiceoverReused      int               `json:"voiceover_reused,omitempty"`
	VoiceoverGenerated   int               `json:"voiceover_generated,omitempty"`
	VoiceoverDBCacheHits int               `json:"voiceover_db_cache_hits,omitempty"`
	CacheOutcome         string            `json:"cache_outcome,omitempty"`
}

type TTSSSceneMetric struct {
	SceneID          string   `json:"scene_id"`
	Language         Language `json:"language"`
	DurationMS       int64    `json:"duration_ms"`
	Characters       int      `json:"characters"`
	Words            int      `json:"words"`
	OutputDurationMS int64    `json:"output_duration_ms,omitempty"`
}
