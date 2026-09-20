// Package scriptgeneration — model_render.go: the render and audio projection
// types of the pure domain model (ZERO I/O: the standard library plus the
// kernel's pure contract types, never an adapter), plus the CONTRACT of the
// central RenderingGen queue those types travel to and from.
//
// RenderReference, RenderArtifact, FinalAudioReference, AudioPipelineMetrics,
// TTSSSceneMetric and DocumentsConfig — the shapes the durable runner hands to
// the document/assembly stages. The primitive value types live in
// model_values.go and the aggregates in model.go.
//
// The queue section further down owns what the queue IS (the wire job shape, its
// state says, and the capabilities a client may implement); render_queue.go owns
// the enqueuer that speaks it. The two were one file until 2026-09-15: the
// contract, the vocabulary and the shared completion/re-arm rules grew past the
// strict per-file cap while they were being converged onto one owner.
//
// Extracted 2026-09-12 from model.go to keep every file under
// max_lines_per_file_strict=600 (godlike/08 forward-prevention cap).
package scriptgeneration

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	kernelasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// RenderReference identifies a completed RenderingGen queue job (the future
// Chronon overlay render path) and carries the certified artifact the
// downstream document/assembly steps consume. It is retained for that path
// and is NOT part of the removed video render pipeline. The Artifact field is
// nil while the job is still queued or running.
type RenderReference struct {
	JobID    string          `json:"job_id"`
	Status   string          `json:"status"`
	Artifact *RenderArtifact `json:"artifact,omitempty"`
	// Items carries the per-overlay render lineage when production renders
	// each semantic item as its own short video. Artifact remains the first
	// item for backward-compatible document/publication consumers.
	Items []OverlayItemRenderReference `json:"items,omitempty"`
}

// OverlayItemRenderReference binds one semantic overlay item to the queue job
// and certified artifact that rendered it.
type OverlayItemRenderReference struct {
	ItemID   string          `json:"item_id"`
	JobID    string          `json:"job_id"`
	Status   string          `json:"status"`
	Artifact *RenderArtifact `json:"artifact,omitempty"`
}

// RenderArtifact is the certified artifact produced by the central
// RenderingGen queue. It mirrors the queue's artifact contract (including the
// copy-only certification) so the document renderer and Velox copy assembly
// consume the same immutable reference without probing the file themselves.
type RenderArtifact struct {
	ID           string `json:"id,omitempty"`
	Kind         string `json:"kind,omitempty"`
	StorageKey   string `json:"storage_key,omitempty"`
	URL          string `json:"url,omitempty"`
	SHA256       string `json:"sha256,omitempty"`
	MimeType     string `json:"mime_type,omitempty"`
	SizeBytes    int64  `json:"size_bytes,omitempty"`
	Width        int    `json:"width,omitempty"`
	Height       int    `json:"height,omitempty"`
	FPSNum       int    `json:"fps_num,omitempty"`
	FPSDen       int    `json:"fps_den,omitempty"`
	FrameCount   int    `json:"frame_count,omitempty"`
	DurationUS   int64  `json:"duration_us,omitempty"`
	ProfileID    string `json:"profile_id,omitempty"`
	CopyEligible bool   `json:"copy_eligible,omitempty"`
	Codec        string `json:"codec,omitempty"`
	CodecProfile string `json:"codec_profile,omitempty"`
	// Container, PixelFormat and AudioStreams are the remaining structural
	// facts RenderingGen already certifies on its artifact wire. They used to
	// be dropped by this projection, which meant the clip.render contract gate
	// could never check them (the local Rust probe reports no codec profile,
	// and the certified boundary is the only owner of the container family).
	Container    string `json:"container,omitempty"`
	PixelFormat  string `json:"pixel_format,omitempty"`
	AudioStreams int    `json:"audio_streams,omitempty"`
	// OutputFacts carries RenderingGen's COMPLETE structural certification of
	// the artifact verbatim (the queue's output_facts object). It is kept as
	// raw JSON on purpose: the fact set is owned by the rendering boundary, so
	// this projection relays it without re-declaring (and drifting from) its
	// shape. A consumer that needs typed access unmarshals it into its own
	// capability-local struct. Nil when the worker certified only the flat
	// summary.
	OutputFacts        json.RawMessage `json:"output_facts,omitempty"`
	ClosedGOP          bool            `json:"closed_gop,omitempty"`
	FirstFrameKeyframe bool            `json:"first_frame_keyframe,omitempty"`
	Backend            string          `json:"backend,omitempty"`
	ChrononVersion     string          `json:"chronon_version,omitempty"`
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

// ── Central RenderingGen queue contract ─────────────────────────────────
// The wire job shape (POST /jobs, GET /jobs/{id}) and the capabilities a queue
// client may implement. render_queue.go owns the enqueuer that speaks it.

// ErrJobExists is returned by RenderQueueClient.Submit when a job with the
// same ID was already enqueued. The queue enqueuer treats this as success and
// proceeds to wait on the existing job, making retries idempotent.
var ErrJobExists = errors.New("render job already exists")

// defaultQueuePollInterval is how long the queue enqueuer waits between
// status polls while the render is in flight. It is only exercised by the
// polling fallback: the primary path is the queue's job-status long poll
// (RenderQueueWaiter), which observes a terminal job at the transition. The
// fallback cadence is kept short so an older queue deployment without the
// wait route does not reintroduce multi-second tail latency.
const defaultQueuePollInterval = 250 * time.Millisecond

// RenderQueueAsset points at an input asset the central queue worker must
// fetch. SHA256 is the object-store lookup key (the content address of the
// file).
//
// It is the WIRE PROJECTION of kernel/asset.Ref: Ref() projects it onto the
// canonical identity and NewRenderQueueAsset builds it from one, so the
// canonical type is the single place where "which asset / which bytes" is
// spelled. The identity fields live here (rather than as an embedded
// kernel/asset.Ref) only because the queue's field name for the digest is
// `hash` and this projection must stay byte-compatible with the deployed
// RenderingGen queue; embedding would rename the wire field to `sha256` in the
// same change that removed the location field, which is exactly the kind of
// two-in-one wire break the repo's expand/cutover rule forbids.
type RenderQueueAsset struct {
	// SHA256 is the content address. The JSON name stays `hash`: it is the
	// deployed RenderingGen queue contract (queueclient.AssetRef), and renaming
	// it is a coordinated cross-repo cutover, not a local cleanup.
	SHA256    string `json:"hash"`
	URL       string `json:"url,omitempty"`
	SourceURL string `json:"source_url,omitempty"`
	// LocalPath is producer-side only. The adapter stages it into the object
	// store and omits it from the RenderingGen wire asset reference.
	//
	// It is the LAST location field on this DTO and it is deliberately still
	// here: the behaviour it carries (staging producer-verified bytes into the
	// object store without re-downloading them) is pinned by tests and its
	// owner is the canonical AssetMaterializer, which is the next step of the
	// media-identity programme. Deleting it here would delete a pinned feature
	// and silently turn every staged asset into a download, so it is left in
	// place, documented, and scheduled — never smuggled and never forgotten.
	LocalPath string `json:"-"`
}

// Ref projects the asset onto the canonical, location-free identity. AssetID is
// the logical path the worker resolves the asset under, because that is the
// only logical identity this wire projection carries.
func (a RenderQueueAsset) Ref() kernelasset.Ref {
	return kernelasset.Ref{AssetID: a.URL, SHA256: a.SHA256}.Canonical()
}

// NewRenderQueueAsset builds the wire projection from the canonical identity.
// Producers build from kernel/asset.Ref so the identity has exactly one
// spelling; logicalPath is the path the worker resolves the asset under, and
// sourceURL is the fetchable origin used for worker self-healing.
func NewRenderQueueAsset(ref kernelasset.Ref, logicalPath, sourceURL string) RenderQueueAsset {
	canonical := ref.Canonical()
	return RenderQueueAsset{SHA256: canonical.SHA256, URL: logicalPath, SourceURL: sourceURL}
}

// RenderQueueJob is the queue-side view of a submitted render job. It is the
// wire contract with the central RenderingGen queue (POST /jobs and
// GET /jobs/{id}). JobType is the canonical overlay job type
// (overlay.prepare / overlay.render) the queue worker dispatches on.
type RenderQueueJob struct {
	ID          string             `json:"id"`
	JobType     string             `json:"job_type,omitempty"`
	ParentJobID string             `json:"parent_job_id,omitempty"`
	ChunkIndex  int                `json:"chunk_index,omitempty"`
	FrameRange  *RenderFrameRange  `json:"frame_range,omitempty"`
	OverlaySpec json.RawMessage    `json:"overlay_spec"`
	Assets      []RenderQueueAsset `json:"assets"`
	State       string             `json:"state"`
	FailReason  string             `json:"fail_reason,omitempty"`
	Artifact    *RenderArtifact    `json:"artifact,omitempty"`

	// Queue-owned lifecycle timestamps (GET /jobs/{id}). They are the ONLY
	// measurement of where a remote render's wall went BEFORE the renderer
	// touched it: QueuedAt→StartedAt is the admission wait (the queue had no
	// worker capacity yet) and StartedAt→CompletedAt is the worker's service
	// wall. A zero value means the queue did not report the timestamp; it is
	// never fabricated into a measurement.
	QueuedAt    time.Time `json:"queued_at,omitempty"`
	StartedAt   time.Time `json:"started_at,omitempty"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
}

// RenderFrameRange is the half-open [Start, End) range carried by a chunk
// family. It mirrors the queue wire contract without coupling this capability
// package to the RenderingGen transport package.
type RenderFrameRange struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

// RenderQueueClient is the narrow port for the central RenderingGen queue.
// The capability stays independent of HTTP; the concrete client lives in
// internal/platform/renderinggen.
type RenderQueueClient interface {
	// Submit enqueues a job. It returns ErrJobExists when a job with the
	// same ID is already present (idempotent replay).
	Submit(ctx context.Context, job RenderQueueJob) error
	// Get returns the current state of a job, including its artifact once
	// the job completes.
	Get(ctx context.Context, id string) (RenderQueueJob, error)
}

// RenderQueueBatchSubmitter is the optional atomic anchor+children submit
// capability. Chunked production submission fails closed when a deployment
// does not provide it; ordinary render callers remain source-compatible.
type RenderQueueBatchSubmitter interface {
	SubmitBatch(context.Context, []RenderQueueJob) error
}

// RenderQueueChildrenReader is the optional read surface used to make a
// replay of an already-created chunk family idempotent.
type RenderQueueChildrenReader interface {
	Children(context.Context, string) ([]RenderQueueJob, error)
}

// RenderQueueWaiter is the optional event-driven completion capability. A
// queue client that implements it lets the enqueuer observe a terminal render
// at the state transition instead of sampling the job on a cadence: the
// RenderingGen queue exposes GET /jobs/{id}/wait and the adapter blocks on it.
// Clients that do not implement it (older deployments, test doubles) keep the
// polling loop, so the capability is additive and never required.
type RenderQueueWaiter interface {
	// WaitTerminal blocks until the job reaches a terminal state (completed,
	// failed or cancelled) or ctx ends, and returns the last observed job.
	WaitTerminal(ctx context.Context, id string) (RenderQueueJob, error)
}

// RenderQueueRetrier is the optional reset-to-pending capability. It exists for
// ONE decision: a submission that collides with an existing job in FAILED state
// (idempotent replay of a failed render) must be re-armed, never silently
// treated as an idempotent success — the caller would otherwise wait on a job
// that can never produce an artifact. Declaring this port here (instead of an
// anonymous interface at the call site, as it was) keeps the capability shape
// next to the two it belongs with and makes it injectable by test doubles.
type RenderQueueRetrier interface {
	// Retry resets a failed job to pending so the queue re-runs it.
	Retry(ctx context.Context, id string) error
}

// RenderCompletionMetrics separates the worker-reported Chronon duration from
// the client-side wait used to observe the queue. PollingSleep is the time
// deliberately spent sleeping between status requests, so it is the direct
// measurable impact of the polling cadence (and not Chronon work).
type RenderCompletionMetrics struct {
	CompletionWait time.Duration
	PollingSleep   time.Duration
	PollInterval   time.Duration
	PollCount      int
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
