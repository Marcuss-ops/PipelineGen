// Package scriptgeneration — model_render.go: the render and audio projection
// types of the pure domain model (ZERO I/O; standard library only).
//
// RenderReference, RenderArtifact, FinalAudioReference, AudioPipelineMetrics,
// TTSSSceneMetric and DocumentsConfig — the shapes the durable runner hands to
// the document/assembly stages. The primitive value types live in
// model_values.go and the aggregates in model.go.
//
// Extracted 2026-09-12 from model.go to keep every file under
// max_lines_per_file_strict=600 (godlike/08 forward-prevention cap).
package scriptgeneration

// RenderReference identifies a completed RenderingGen queue job (the future
// Chronon overlay render path) and carries the certified artifact the
// downstream document/assembly steps consume. It is retained for that path
// and is NOT part of the removed video render pipeline. The Artifact field is
// nil while the job is still queued or running.
type RenderReference struct {
	JobID    string          `json:"job_id"`
	Status   string          `json:"status"`
	Artifact *RenderArtifact `json:"artifact,omitempty"`
}

// RenderArtifact is the certified artifact produced by the central
// RenderingGen queue. It mirrors the queue's artifact contract (including the
// copy-only certification) so the document renderer and Velox copy assembly
// consume the same immutable reference without probing the file themselves.
type RenderArtifact struct {
	ID                 string `json:"id,omitempty"`
	Kind               string `json:"kind,omitempty"`
	StorageKey         string `json:"storage_key,omitempty"`
	URL                string `json:"url,omitempty"`
	SHA256             string `json:"sha256,omitempty"`
	MimeType           string `json:"mime_type,omitempty"`
	SizeBytes          int64  `json:"size_bytes,omitempty"`
	Width              int    `json:"width,omitempty"`
	Height             int    `json:"height,omitempty"`
	FPSNum             int    `json:"fps_num,omitempty"`
	FPSDen             int    `json:"fps_den,omitempty"`
	FrameCount         int    `json:"frame_count,omitempty"`
	DurationUS         int64  `json:"duration_us,omitempty"`
	ProfileID          string `json:"profile_id,omitempty"`
	CopyEligible       bool   `json:"copy_eligible,omitempty"`
	Codec              string `json:"codec,omitempty"`
	CodecProfile       string `json:"codec_profile,omitempty"`
	ClosedGOP          bool   `json:"closed_gop,omitempty"`
	FirstFrameKeyframe bool   `json:"first_frame_keyframe,omitempty"`
	Backend            string `json:"backend,omitempty"`
	ChrononVersion     string `json:"chronon_version,omitempty"`
	// RenderMS and EncodeMS are the worker-measured wall durations of the
	// Chronon render and encode phases (from the queue artifact's metrics
	// map: render_ms / encode_ms). Zero means the worker did not report them.
	RenderMS int64 `json:"render_ms,omitempty"`
	EncodeMS int64 `json:"encode_ms,omitempty"`
	// The remaining phase durations are the RenderingGen worker's own
	// per-phase timings (materialize/plan/probe/hash/objectstore_upload/
	// drive_publish), mapped from the queue artifact's metrics map. They
	// are projected into the canonical run model as owner-measured
	// operations — PipelineGen never re-times a phase the worker already
	// measured. Zero means the worker did not report the phase.
	MaterializeMS  int64 `json:"materialize_ms,omitempty"`
	PlanMS         int64 `json:"plan_ms,omitempty"`
	ProbeMS        int64 `json:"probe_ms,omitempty"`
	HashMS         int64 `json:"hash_ms,omitempty"`
	UploadMS       int64 `json:"objectstore_upload_ms,omitempty"`
	DrivePublishMS int64 `json:"drive_publish_ms,omitempty"`
	// DriveFileID and DriveLink are the Google Drive publication identity of
	// the rendered artifact (populated by the worker's publish phase). Empty
	// when the artifact was not published to Drive.
	DriveFileID   string `json:"drive_file_id,omitempty"`
	DriveLink     string `json:"drive_link,omitempty"`
	DriveFolderID string `json:"drive_folder_id,omitempty"`
	// Metrics is the numeric projection of Chronon's timing sidecar returned
	// by RenderingGen and correlated with this artifact.
	Metrics map[string]float64 `json:"metrics,omitempty"`
	// ChrononTiming* reference the RAW deep-profile timing sidecar
	// (`<output>.timing.json`, including the unbounded per-frame
	// frame_times_ms array) preserved verbatim in the RenderingGen object
	// store under its content address. Only the small reference rides the
	// artifact — the per-frame array is never inlined. Empty when the worker
	// could not preserve the sidecar (fail-open).
	ChrononTimingStorageKey  string `json:"chronon_timing_storage_key,omitempty"`
	ChrononTimingURL         string `json:"chronon_timing_url,omitempty"`
	ChrononTimingSHA256      string `json:"chronon_timing_sha256,omitempty"`
	ChrononTimingSizeBytes   int64  `json:"chronon_timing_size_bytes,omitempty"`
	ChrononTimingContentType string `json:"chronon_timing_content_type,omitempty"`
}

type FinalAudioReference struct {
	AssetID string `json:"audio_asset_id"`
	// Filename is the caller-facing output name used when publishing the
	// certified master. It is derived from the payload title, never a generic
	// final_audio name.
	Filename             string `json:"filename,omitempty"`
	Path                 string `json:"path,omitempty"`
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

// AudioPipelineMetrics is an audio-friendly API projection of canonical
// observability stages and operations. It is not an authority and must not
// acquire independent timers or persistence writers.
//
// Relationship to the legacy domain contract: internal/kernel/script
// .GenerationTimings carries an overlapping set of flat *_ms audio fields used
// only by the migration-only internal/capabilities/scripts/usecase path. That
// struct is a legacy projection; this struct is the authority. Field map:
//
//	GenerationTimings (domain, legacy)  AudioPipelineMetrics (projection)
//	tts_total_ms                          tts_ms
//	audio_mix_ms                          mix_ms
//	audio_encode_ms                       aac_encode_ms
//	audio_probe_ms                        probe_ms
//	audio_hash_ms                         hash_ms
//	audio_pipeline_total_ms               total_ms
//	final_audio_duration_ms               audio_duration_ms
//	audio_encode_passes                   audio_encode_passes (unchanged)
//	tts_calls                             tts_calls (unchanged)
//	timeline_compile_ms                   timeline_compile_ms (unchanged)
//	audio_plan_compile_ms                 audio_plan_compile_ms (unchanged)
//	clip_audio_prepare_ms                 clip_audio_prepare_ms (unchanged)
//
// Projection-only fields with no legacy equivalent: media_fetch_ms, upload_ms,
// audio_rtf, audio_speed, tts_scenes.
//
// Do NOT add new timing fields here as an authority; derive them from the
// canonical RunReport and keep this struct stable for API consumers.
type AudioPipelineMetrics struct {
	TTSMS        int64 `json:"tts_ms"`
	MediaFetchMS int64 `json:"media_fetch_ms"`
	// AudioAssetResolveMS is the owner-measured BGM/SFX asset resolution
	// boundary. It is separate from plan compilation so resolver latency is
	// visible instead of being folded into the audio-plan phase.
	AudioAssetResolveMS int64             `json:"audio_asset_resolve_ms"`
	TimelineCompileMS   int64             `json:"timeline_compile_ms"`
	AudioPlanCompileMS  int64             `json:"audio_plan_compile_ms"`
	ClipAudioPrepareMS  int64             `json:"clip_audio_prepare_ms"`
	MixMS               int64             `json:"mix_ms"`
	AACEncodeMS         int64             `json:"aac_encode_ms"`
	ProbeMS             int64             `json:"probe_ms"`
	HashMS              int64             `json:"hash_ms"`
	UploadMS            int64             `json:"upload_ms"`
	TotalMS             int64             `json:"total_ms"`
	AudioDurationMS     int64             `json:"audio_duration_ms"`
	TTSCalls            int               `json:"tts_calls"`
	AudioRTF            float64           `json:"audio_rtf"`
	AudioSpeed          float64           `json:"audio_speed"`
	AudioEncodePasses   int               `json:"audio_encode_passes"`
	TTSScenes           []TTSSSceneMetric `json:"tts_scenes,omitempty"`
	// VoiceoverRequested counts every (scene, language) text that needed a
	// voiceover. VoiceoverReused counts scene-projection reuse (a voiceover
	// reference already attached before dispatch). VoiceoverGenerated counts
	// the items dispatched to the voiceover pipeline.
	VoiceoverRequested int `json:"voiceover_requested,omitempty"`
	VoiceoverReused    int `json:"voiceover_reused,omitempty"`
	VoiceoverGenerated int `json:"voiceover_generated,omitempty"`
	// VoiceoverDBCacheHits counts the dispatched items served by the
	// cross-run SQLite fingerprint cache (0 TTS calls, 0 uploads, 0
	// finalize). Warm identical runs report Generated == DBCacheHits and
	// TTSCalls == 0.
	VoiceoverDBCacheHits int `json:"voiceover_db_cache_hits,omitempty"`
}

type TTSSSceneMetric struct {
	SceneID          string   `json:"scene_id"`
	Language         Language `json:"language"`
	DurationMS       int64    `json:"duration_ms"`
	Characters       int      `json:"characters"`
	Words            int      `json:"words"`
	OutputDurationMS int64    `json:"output_duration_ms,omitempty"`
}

// ── DocumentsConfig ──────────────────────────────────────────────────

// DocumentsConfig is the explicit contract for Google Doc publishing.
// Per the verdetto, document creation MUST NOT be implicit based on
// whether drive_output_folder happens to be present — it must be
// explicitly requested via this config.
//
// One document per language is created (e.g. "script_en", "script_es").
// The identity is deterministic: (generation_run_id + language) drive
// properties. On retry, UpsertDocument updates the same document.
type DocumentsConfig struct {
	// Enabled explicitly requests Google Doc publishing.
	// When false, no documents are created even if Languages or
	// FolderID are populated. Default false (opt-in).
	Enabled bool `json:"enabled"`

	// Languages lists the languages for which a document should be
	// published. Each language gets its own document (one per language,
	// NOT one bilingual document). Must be non-empty when Enabled is
	// true.
	Languages []Language `json:"languages,omitempty"`

	// FolderID is the target Google Drive folder ID for documents.
	// When empty, documents are created in the default Drive location.
	FolderID string `json:"folder_id,omitempty"`
}
