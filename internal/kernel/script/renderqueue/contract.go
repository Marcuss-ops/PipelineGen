// Package renderqueue owns the CANONICAL contract of the central RenderingGen
// render queue: the wire job/asset/artifact shapes, the terminal-state
// vocabulary, the completion and re-arm rules, and the canonical render request
// identity.
//
// It lives in the kernel because TWO layers speak it and neither may depend on
// the other: the scripts capability produces jobs (internal/capabilities/
// scripts) and the HTTP/object-store adapter consumes them
// (internal/platform/renderinggen). Before this package existed the contract
// lived inside the capabilities package, so every platform adapter that
// implemented it imported a business package — a module-level cycle
// (capabilities → platform for the transport, platform → capabilities for the
// types) with no gate in either direction. The kernel is the neutral owner:
// technology-free (no net/http, no database/sql, no capabilities import), so
// both sides import it without importing each other.
//
// The wire identifiers are deliberately unchanged (RenderQueueJob,
// RenderArtifact, ...): the moved types are aliased back into the scripts
// capability, so the 395-file package and its tests keep compiling while the
// single definition is here.
package renderqueue

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	kernelasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// RenderArtifact is the certified artifact produced by the central
// RenderingGen queue. It mirrors the queue's artifact contract (including the
// copy-only certification) so the document renderer and Velox copy assembly
// consume the same immutable reference without probing the file themselves.
type RenderArtifact struct {
	// BatchSegments are independently certified MP4s from one encoder session.
	// They never inherit the mirror's structural facts or Drive identity.
	BatchSegments []OverlayItemRenderReference `json:"batch_segments,omitempty"`

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
	OutputFacts json.RawMessage `json:"output_facts,omitempty"`
	// Provenance is RenderingGen's import attestation result, relayed verbatim
	// so producer item identities survive the queue boundary and durable run
	// checkpoint without duplicating the worker-owned schema.
	Provenance         json.RawMessage `json:"provenance,omitempty"`
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
	// ChrononTelemetry is the BOUNDED telemetry summary ingested from
	// Chronon's timing sidecar (schema chronon3d.render-telemetry-summary.v1)
	// preserved verbatim from RenderingGen. It is the ledger telemetry: the
	// bounded JSON document with exclusive-wall + GPU summary, without the
	// unbounded per-frame array. Nil when the worker produced only the flat
	// summary or predated the field (fail-open, legacy worker).
	ChrononTelemetry json.RawMessage `json:"chronon_telemetry,omitempty"`
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

// ErrRenderRequestConflict is returned when a queue job id already exists but
// its immutable request identity differs from the one being submitted. This is
// a real conflict, never an idempotent replay: the existing job must not be
// reused, re-armed, or accepted as the render for the new content.
var ErrRenderRequestConflict = errors.New("render request identity conflict")

// defaultQueuePollInterval is how long the queue enqueuer waits between
// status polls while the render is in flight. It is only exercised by the
// polling fallback: the primary path is the queue's job-status long poll
// (RenderQueueWaiter), which observes a terminal job at the transition. The
// fallback cadence is kept short so an older queue deployment without the
// wait route does not reintroduce multi-second tail latency.
const DefaultPollInterval = 250 * time.Millisecond

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
	ID                    string                         `json:"id"`
	JobType               string                         `json:"job_type,omitempty"`
	ParentJobID           string                         `json:"parent_job_id,omitempty"`
	ChunkIndex            int                            `json:"chunk_index,omitempty"`
	FrameRange            *RenderFrameRange              `json:"frame_range,omitempty"`
	PreparedRenderPackage *RenderQueuePreparedPackageRef `json:"prepared_render_package,omitempty"`
	OverlaySpec           json.RawMessage                `json:"overlay_spec"`
	Assets                []RenderQueueAsset             `json:"assets"`
	State                 string                         `json:"state"`
	FailReason            string                         `json:"fail_reason,omitempty"`
	Artifact              *RenderArtifact                `json:"artifact,omitempty"`

	// Queue-owned lifecycle timestamps (GET /jobs/{id}). They are the ONLY
	// measurement of where a remote render's wall went BEFORE the renderer
	// touched it: QueuedAt→StartedAt is the admission wait (the queue had no
	// worker capacity yet) and StartedAt→CompletedAt is the worker's service
	// wall. A zero value means the queue did not report the timestamp; it is
	// never fabricated into a measurement.
	QueuedAt    time.Time `json:"queued_at,omitempty"`
	StartedAt   time.Time `json:"started_at,omitempty"`
	CompletedAt time.Time `json:"completed_at,omitempty"`

	// Progress is the owning worker's last render progress (GET /jobs/{id}).
	// It is the ONLY stall signal the producer can see without asking the
	// worker: FramesDone plus LastFrameAt answer "is this render advancing?"
	// (ELIM-06). Nil means the queue has no report yet.
	Progress *RenderQueueProgress `json:"progress,omitempty"`
}

// RenderQueueProgress mirrors the queue's render-progress body.
//
// LastFrameAt is the observation clock: a render whose frames_done did not
// move and whose last_frame_at is older than the stall threshold is stalled,
// regardless of how long it has been running in absolute terms.
type RenderQueueProgress struct {
	FramesDone  int       `json:"frames_done"`
	TotalFrames int       `json:"frames_total,omitempty"`
	LastFrameAt time.Time `json:"last_frame_at,omitempty"`
	Worker      string    `json:"worker,omitempty"`
}

// RenderQueuePreparedPackageRef identifies the immutable preparation shared
// by every render in a chunk family.
type RenderQueuePreparedPackageRef struct {
	PackageID        string `json:"package_id"`
	PlanSHA256       string `json:"plan_sha256"`
	AssetManifestSHA string `json:"asset_manifest_sha256"`
	TotalFrames      int64  `json:"total_frames"`
	CompilerVersion  string `json:"compiler_version"`
	ProfileID        string `json:"profile_id,omitempty"`
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
	WaitStartedAt  time.Time
	WaitFinishedAt time.Time
	// Overlay cache timings belong to this queue attempt. They are kept in
	// nanosecond-resolution durations here and persisted as microseconds so
	// fast local cache hits do not collapse to a misleading zero millisecond.
	CacheLookup  time.Duration
	CacheProbe   time.Duration
	CacheStore   time.Duration
	CacheOutcome string
}

// OverlayItemRenderReference binds one semantic overlay item to the queue job
// and certified artifact that rendered it.
type OverlayItemRenderReference struct {
	ItemID   string          `json:"item_id"`
	JobID    string          `json:"job_id"`
	Status   string          `json:"status"`
	BatchID  string          `json:"batch_id,omitempty"`
	Artifact *RenderArtifact `json:"artifact,omitempty"`
}
