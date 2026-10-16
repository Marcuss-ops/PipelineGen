// Package client is the public HTTP client for the central RenderingGen queue
// service. Producer services (PipelineGen and any future orchestrator) import
// this package to submit render jobs and wait for the certified artifact,
// instead of hand-rolling the HTTP contract themselves.
//
// The wire contract mirrors the queue HTTP server:
//
//	POST /jobs            submit a job (409 -> ErrJobExists)
//	GET  /jobs/{id}       current state + artifact
//	GET  /jobs/{id}/wait  long-poll until the job is terminal (producer)
//	GET  /jobs/depth      queue depth/stats
//	GET  /health          liveness
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// ErrJobExists is returned by Submit when the job ID was already submitted.
// Callers treat it as idempotent success for replay/retry scenarios.
var ErrJobExists = errors.New("job already exists")

// ErrNotFound is returned by Get when the job does not exist.
var ErrNotFound = errors.New("job not found")

// State is the lifecycle state of a job. It is the canonical definition: the
// queue service (queue/internal/model), the worker and every producer ALIAS
// this type instead of re-declaring the vocabulary, so a state can never mean
// two different things on the two sides of the wire. The SQL CHECK constraint
// is pinned to AllStates() by queue/internal/model/state_schema_test.go.
type State string

const (
	StatePending    State = "pending"
	StateRunning    State = "running"
	StateCompleted  State = "completed"
	StateFailed     State = "failed"
	StateRendered   State = "rendered"
	StateCancelled  State = "cancelled"
	StateFinalizing State = "finalizing"
)

// AllStates returns the complete canonical state vocabulary in lifecycle
// order (not terminal-order). It is the single list the queue's SQL CHECK
// constraint and every label/validation helper must agree with.
func AllStates() []State {
	return []State{
		StatePending, StateRunning, StateFinalizing,
		StateCompleted, StateFailed, StateCancelled, StateRendered,
	}
}

// JobType constants are the canonical job-type vocabulary carried in the
// renderinggen.job.v1 envelope.
const (
	PublicationObjectStoreOnly     = "object_store_only"
	PublicationObjectStoreAndDrive = "object_store_and_drive"

	JobTypeRenderSegment = "render_segment"

	JobTypeOverlayPrepare = "overlay.prepare"
	JobTypeOverlayRender  = "overlay.render"
	// JobTypeOverlayImport validates and records an MP4 produced by a trusted
	// upstream renderer without invoking Chronon. Its request is versioned
	// independently from the queue envelope and its provenance is persisted
	// with the resulting artifact.
	JobTypeOverlayImport = "overlay.import"
)

// JobSchemaV1 identifies the renderinggen.job.v1 envelope.
const JobSchemaV1 = "renderinggen.job"

// JobSchemaVersionV1 is the version of the renderinggen.job.v1 envelope.
const JobSchemaVersionV1 = 1

// ValidateJobEnvelope checks the renderinggen.job.v1 envelope fields WHEN
// PRESENT at the ingress boundary (fail-at-ingress for malformed data): a
// non-empty schema must be renderinggen.job, a non-zero version must be 1, a
// present render_plan must be valid JSON, and every asset entry must carry a
// hash and a logical path. Absent fields are allowed: the queue also accepts
// minimal jobs (see the server batch/claim tests), and the worker-side
// processor/validator.go enforces the full envelope after claim. It does not
// check ID or publication metadata (ValidateJobID/ValidateJobMetadata own
// those).
func ValidateJobEnvelope(job Job) error {
	if job.Schema != "" && job.Schema != JobSchemaV1 {
		return fmt.Errorf("unsupported job schema %q (want %q)", job.Schema, JobSchemaV1)
	}
	if job.Version != 0 && job.Version != JobSchemaVersionV1 {
		return fmt.Errorf("unsupported job schema version %d (want %d)", job.Version, JobSchemaVersionV1)
	}
	if len(job.RenderPlan) > 0 && !json.Valid(job.RenderPlan) {
		return fmt.Errorf("render_plan is not valid JSON")
	}
	for i, a := range job.Assets {
		if a.Hash == "" {
			return fmt.Errorf("assets[%d]: hash is required", i)
		}
		if a.LogicalPath == "" {
			return fmt.Errorf("assets[%d]: logical_path is required", i)
		}
	}
	return nil
}

// ValidateJobID checks the shared queue/filesystem identity boundary. IDs are
// restricted to safe ASCII filename characters; producer scopes use colon or
// dash separators rather than filesystem path separators.
func ValidateJobMetadata(job Job) error {
	switch job.PublicationPolicy {
	case "", PublicationObjectStoreOnly, PublicationObjectStoreAndDrive:
	default:
		return fmt.Errorf("unsupported publication_policy %q", job.PublicationPolicy)
	}
	if math.IsNaN(job.DaemonAdmissionWaitMS) || math.IsInf(job.DaemonAdmissionWaitMS, 0) || job.DaemonAdmissionWaitMS < 0 {
		return fmt.Errorf("daemon_admission_wait_ms must be a finite non-negative number")
	}
	return nil
}

// ValidateJobID checks the shared queue/filesystem identity boundary. An id
// must survive being used as ONE filename component (the worker's workspace
// directory, its per-job logs, its artifact keys), so the rule bans what is
// dangerous there — path separators, control characters, traversal components,
// symbols — rather than what is merely non-ASCII. Producer scopes use colon or
// dash separators, never a filesystem path separator.
//
// Letters, digits and combining marks are accepted in any script: an id is
// derived from authored text, and an Italian phrase legitimately carries
// "comunità". Rejecting the accented letter failed an otherwise valid render
// job at workspace materialization, after the plan had been compiled. Marks are
// admitted with their base letter; NFC and NFD spellings still name DIFFERENT
// jobs, exactly as two spellings of any other id do.
func ValidateJobID(id string) error {
	if id == "." || id == ".." {
		return fmt.Errorf("job id %q is a traversal component", id)
	}
	if len(id) == 0 || len(id) > 256 {
		return fmt.Errorf("job id must contain 1..256 bytes")
	}
	for i := 0; i < len(id); {
		c := id[i]
		if c < utf8.RuneSelf {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
				(c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' || c == ':') {
				return fmt.Errorf("job id %q contains unsupported character %q", id, c)
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(id[i:])
		if r == utf8.RuneError && size <= 1 {
			return fmt.Errorf("job id %q is not valid UTF-8", id)
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsMark(r) {
			return fmt.Errorf("job id %q contains unsupported character %q", id, r)
		}
		i += size
	}
	return nil
}

// CertifiedProfileVeloxH2641080p30V1 is the profile_id of the ONE certified
// copy-ready overlay artifact downstream assemblers select on (see
// queue/ops/render_artifacts_contract_audit.sql, section 3). It is owned by
// the wire contract rather than the worker's media registry because the value
// travels on the artifact boundary: queue-readable code and every consumer
// must agree on it, and the queue module cannot import the worker. The worker's
// media.ProfileVeloxH2641080p30V1 aliases this constant instead of restating
// the literal, and the queue tests reference it instead of re-typing it.
const CertifiedProfileVeloxH2641080p30V1 = "velox-h264-1080p30-v1"

// AssetRef points at an asset in the central artifact store by content hash
// and the logical path it must be materialized at in the job workspace.
type AssetRef struct {
	Hash        string `json:"hash"`
	LogicalPath string `json:"logical_path"`
	// SourceURL is an optional durable download location. LogicalPath remains
	// the local workspace destination and must never be overloaded with a URL.
	SourceURL string `json:"source_url,omitempty"`
}

// OutputFacts is the COMPLETE structural certification of the rendered
// artifact: the ffprobe facts every consumer's output contract validates.
//
// It is the certification of record. A consumer that cannot observe a
// dimension itself (PipelineGen's Rust probe reports no video timebase, SAR,
// colour, GOP interval, channel layout or codec profile) consumes the
// certified value instead of silently skipping the check. It is nested as ONE
// JSON object so the fact set can grow without a database column per fact.
//
// Nil means the boundary did not certify the full fact set (a legacy worker);
// consumers must then fall back to whatever they can observe themselves.
type OutputFacts struct {
	Container        string `json:"container,omitempty"`
	HasVideo         bool   `json:"has_video,omitempty"`
	VideoStreams     int    `json:"video_streams,omitempty"`
	VideoCodec       string `json:"video_codec,omitempty"`
	VideoProfile     string `json:"video_profile,omitempty"`
	VideoLevel       string `json:"video_level,omitempty"`
	PixelFormat      string `json:"pixel_format,omitempty"`
	Width            int    `json:"width,omitempty"`
	Height           int    `json:"height,omitempty"`
	FPSNum           int    `json:"fps_num,omitempty"`
	FPSDen           int    `json:"fps_den,omitempty"`
	VideoTimeBaseNum int    `json:"video_timebase_num,omitempty"`
	VideoTimeBaseDen int    `json:"video_timebase_den,omitempty"`
	AudioTimeBaseNum int    `json:"audio_timebase_num,omitempty"`
	AudioTimeBaseDen int    `json:"audio_timebase_den,omitempty"`
	SARNum           int    `json:"sar_num,omitempty"`
	SARDen           int    `json:"sar_den,omitempty"`
	ColorRange       string `json:"color_range,omitempty"`
	ColorSpace       string `json:"color_space,omitempty"`
	ColorTransfer    string `json:"color_transfer,omitempty"`
	ColorPrimaries   string `json:"color_primaries,omitempty"`
	FieldOrder       string `json:"field_order,omitempty"`
	KeyframeInterval int    `json:"keyframe_interval,omitempty"`
	StartPTS         int64  `json:"start_pts,omitempty"`
	HasAudio         bool   `json:"has_audio,omitempty"`
	AudioStreams     int    `json:"audio_streams,omitempty"`
	AudioCodec       string `json:"audio_codec,omitempty"`
	AudioProfile     string `json:"audio_profile,omitempty"`
	SampleRate       int    `json:"sample_rate,omitempty"`
	Channels         int    `json:"channels,omitempty"`
	ChannelLayout    string `json:"channel_layout,omitempty"`
	AudioBitrate     string `json:"audio_bitrate,omitempty"`
}

// Artifact is the metadata of a rendered artifact, including the copy-only
// certification (codec, profile, GOP/keyframe flags) VeloxEditing relies on.
// ArtifactProvenance records how artifact bytes entered the certified artifact
// ledger. Imported artifacts deliberately identify the upstream producer and
// the worker-side verification gates instead of claiming a Chronon render.
type ArtifactProvenance struct {
	SchemaVersion      string          `json:"schema_version"`
	Protocol           string          `json:"protocol"`
	Producer           string          `json:"producer"`
	ImportJobID        string          `json:"import_job_id"`
	SourceSHA256       string          `json:"source_sha256"`
	SourceSizeBytes    int64           `json:"source_size_bytes"`
	IdentityVerified   bool            `json:"identity_verified"`
	StructureVerified  bool            `json:"structure_verified"`
	FullDecodeVerified bool            `json:"full_decode_verified"`
	ProducerTelemetry  json.RawMessage `json:"producer_telemetry,omitempty"`
}

// Artifact is the metadata of a rendered or imported artifact, including the copy-only
// certification (codec, profile, GOP/keyframe flags) VeloxEditing relies on.
type Artifact struct {
	ID                 string             `json:"id,omitempty"`
	Kind               string             `json:"kind,omitempty"`
	StorageKey         string             `json:"storage_key,omitempty"`
	ArtifactURL        string             `json:"artifact_url,omitempty"`
	ArtifactHash       string             `json:"artifact_hash,omitempty"`
	ContentType        string             `json:"content_type,omitempty"`
	SizeBytes          int64              `json:"size_bytes,omitempty"`
	Width              int                `json:"width,omitempty"`
	Height             int                `json:"height,omitempty"`
	FPSNum             int                `json:"fps_num,omitempty"`
	FPSDen             int                `json:"fps_den,omitempty"`
	FrameCount         int                `json:"frame_count,omitempty"`
	DurationUS         int64              `json:"duration_us,omitempty"`
	ProfileID          string             `json:"profile_id,omitempty"`
	CopyEligible       bool               `json:"copy_eligible,omitempty"`
	Codec              string             `json:"codec,omitempty"`
	CodecProfile       string             `json:"codec_profile,omitempty"`
	ClosedGOP          bool               `json:"closed_gop,omitempty"`
	FirstFrameKeyframe bool               `json:"first_frame_keyframe,omitempty"`
	Backend            string             `json:"backend,omitempty"`
	ChrononVersion     string             `json:"chronon_version,omitempty"`
	Metrics            map[string]float64 `json:"metrics,omitempty"`
	ChrononTelemetry   json.RawMessage    `json:"chronon_telemetry,omitempty"`
	// ChrononTiming* are the RawTimingArtifactRef: the RAW deep-profile timing
	// sidecar (`<output>.timing.json`, including the unbounded per-frame
	// frame_times_ms array) is preserved as an OPAQUE artifact in the object
	// store under its content address — bytes → hash → store → reference. The
	// bounded ChrononTelemetry (schema chronon3d.render-telemetry-summary.v1)
	// is the ledger telemetry; these references keep the full per-frame
	// profile fetchable for post-mortem without ever inlining the array into
	// the queue document. Absent when the sidecar file did not exist or could
	// not be preserved (fail-open).
	ChrononTimingStorageKey  string `json:"chronon_timing_storage_key,omitempty"`
	ChrononTimingURL         string `json:"chronon_timing_url,omitempty"`
	ChrononTimingSHA256      string `json:"chronon_timing_sha256,omitempty"`
	ChrononTimingSizeBytes   int64  `json:"chronon_timing_size_bytes,omitempty"`
	ChrononTimingContentType string `json:"chronon_timing_content_type,omitempty"`
	DriveFileID              string `json:"drive_file_id,omitempty"`
	DriveLink                string `json:"drive_link,omitempty"`
	Container                string `json:"container,omitempty"`
	PixelFormat              string `json:"pixel_format,omitempty"`
	AudioStreams             int    `json:"audio_streams,omitempty"`
	// OutputFacts is the complete structural certification (see OutputFacts).
	// Nil when the boundary did not certify the full fact set.
	OutputFacts *OutputFacts `json:"output_facts,omitempty"`
	// Provenance is present for non-Chronon artifacts. It is persisted as
	// JSONB in the authoritative queue artifact ledger and relayed to producers.
	Provenance *ArtifactProvenance `json:"provenance,omitempty"`
}

// Job is the unit of work exchanged with the queue: one render SEGMENT. On
// submit only ID, Schema, Version, RenderPlan and Assets matter; on Get the
// server also fills in State, the attempt count, timestamps and (once
// completed) the Artifact; on claim the server adds the Lease duration.
//
// It is the SINGLE job envelope for every direction and every layer (the
// queue service, the server, the worker adapter and the producers all alias
// it); there is no second claim-response or persistence declaration of the
// same fields.
type FrameRange struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

type Job struct {
	ID                    string      `json:"id"`
	Schema                string      `json:"schema,omitempty"`
	Version               int         `json:"version,omitempty"`
	IdempotencyKey        string      `json:"idempotency_key,omitempty"`
	JobType               string      `json:"job_type,omitempty"`
	PublicationPolicy     string      `json:"publication_policy,omitempty"`
	DaemonAdmissionWaitMS float64     `json:"daemon_admission_wait_ms,omitempty"`
	ParentJobID           string      `json:"parent_job_id,omitempty"`
	ChunkIndex            int         `json:"chunk_index,omitempty"`
	FrameRange            *FrameRange `json:"frame_range,omitempty"`

	// NotBefore defers the job: when set to a future time the queue stores it
	// but NO worker can claim it until the time is due. Omitted or a past time
	// makes the job immediately claimable, so existing producers are
	// unaffected. The claim query enforces it, so deferral cannot be bypassed
	// by a worker that ignores the field.
	NotBefore *time.Time `json:"not_before,omitempty"`

	RenderPlan json.RawMessage `json:"render_plan"`
	Assets     []AssetRef      `json:"assets"`

	State       State     `json:"state"`
	Worker      string    `json:"worker,omitempty"`
	Attempts    int       `json:"attempts"`
	CreatedAt   time.Time `json:"created_at"`
	QueuedAt    time.Time `json:"queued_at,omitempty"`
	StartedAt   time.Time `json:"started_at,omitempty"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
	LeaseUntil  time.Time `json:"lease_until,omitempty"`
	FailReason  string    `json:"fail_reason,omitempty"`

	Artifact *Artifact `json:"artifact,omitempty"`

	// Progress is the last render progress reported by the owning worker
	// (nil until the first report). It is part of the GET /jobs/{id} body so a
	// stalled render is observable without asking the worker.
	Progress *Progress `json:"progress,omitempty"`

	// Lease is the claim lease duration, serialized as integer nanoseconds. It
	// is populated only on a claim response and is always absent from submit
	// and GET bodies (omitted when zero).
	Lease time.Duration `json:"lease,omitempty"`
}

// Stats is a snapshot of the queue, used for autoscaling and monitoring.
type Stats struct {
	Pending   int `json:"pending"`
	Running   int `json:"running"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
	Depth     int `json:"depth"`
	// OldestPendingSeconds is the age of the oldest still-pending job, in
	// seconds, measured from its queued_at. A rising value with a flat Depth
	// is the backlog-age signal an autoscaler cannot derive from Depth alone:
	// the queue tail is stagnant (claim capacity gone or a head job blocking
	// FIFO) while the depth number looks healthy. Zero when the queue is
	// empty and, like every field here, zero on a failed snapshot (Ok=false),
	// so consumers already ignoring collapsed snapshots ignore it too.
	OldestPendingSeconds float64 `json:"oldest_pending_seconds,omitempty"`
	// Ok reports whether the snapshot came from a successful store query. A
	// failed snapshot is all-zeros with Ok=false, so a consumer can tell
	// "queue is empty" from "store unavailable". The server has always emitted
	// it; the client used to drop it, which made an outage look like an idle
	// queue to every autoscaler that read Depth.
	Ok bool `json:"ok"`
}

// Client speaks the central queue's HTTP API.
type Client struct {
	baseURL string
	http    *http.Client
}

// New creates a queue client for the given RenderingGen queue endpoint.
func New(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		// No blanket per-request deadline: the claim-wait long poll must be
		// allowed to outlive any fixed timeout (the client-side request context
		// already budgets maxWait+10s, and the server caps waits at 25 s). A
		// historical 30 s client timeout silently capped that budget at
		// min(30s, maxWait+10s). Connect/TLS/response-header phases stay
		// individually bounded so a hung server fails fast.
		http: newQueueHTTPClient(),
	}
}

func newQueueHTTPClient() *http.Client {
	return &http.Client{Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}}
}

// Submit enqueues a job. A 409 (job already exists) is translated to
// ErrJobExists so callers can treat replays as idempotent.
func (c *Client) Submit(ctx context.Context, job Job) error {
	if err := ValidateJobEnvelope(job); err != nil {
		return err
	}
	body, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("queue submit marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/jobs", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("queue submit request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("queue submit do: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusCreated, http.StatusOK:
		return nil
	case http.StatusConflict:
		return fmt.Errorf("%w: job %s", ErrJobExists, job.ID)
	default:
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("queue submit: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
}

// SubmitBatch atomically enqueues an assembly anchor and its render children.
// The queue server validates and persists the whole family in one transaction;
// callers must not emulate this with N independent Submit calls.
func (c *Client) SubmitBatch(ctx context.Context, jobs []Job) error {
	if len(jobs) == 0 {
		return errors.New("queue submit batch: jobs are required")
	}
	for i, job := range jobs {
		if err := ValidateJobEnvelope(job); err != nil {
			return fmt.Errorf("queue submit batch: jobs[%d]: %w", i, err)
		}
	}
	body, err := json.Marshal(struct {
		Jobs []Job `json:"jobs"`
	}{Jobs: jobs})
	if err != nil {
		return fmt.Errorf("queue submit batch marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/jobs/batch", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("queue submit batch request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("queue submit batch do: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusCreated, http.StatusOK:
		return nil
	case http.StatusConflict:
		return fmt.Errorf("%w: batch", ErrJobExists)
	default:
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("queue submit batch: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
}

// Cancel asks the queue to move a job that has not reached a terminal state
// to cancelled. Cancelled is terminal: the job is never claimable again and
// lease expiry never requeues it, so cancelling a job guarantees it can never
// trigger a new Chronon render across lease/claim cycles. Cancelling an
// already-cancelled job is a no-op (nil); a 404 surfaces as ErrNotFound and a
// completed/failed job cannot be cancelled (error).
func (c *Client) Cancel(ctx context.Context, id string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/jobs/"+url.PathEscape(id)+"/cancel", nil)
	if err != nil {
		return fmt.Errorf("queue cancel request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("queue cancel do: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNoContent:
		return nil
	case http.StatusNotFound:
		return fmt.Errorf("%w: job %s", ErrNotFound, id)
	default:
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("queue cancel: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
}

// RecoverableParents returns assembly anchors that still need finalization.
func (c *Client) RecoverableParents(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/jobs/finalization/recoverable", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("queue recoverable parents: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var ids []string
	if err := json.NewDecoder(resp.Body).Decode(&ids); err != nil {
		return nil, err
	}
	return ids, nil
}

// Children returns a parent's child chunks in chunk order.
func (c *Client) Children(ctx context.Context, parentID string) ([]Job, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/jobs/"+url.PathEscape(parentID)+"/children", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("queue children: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var jobs []Job
	if err := json.NewDecoder(resp.Body).Decode(&jobs); err != nil {
		return nil, err
	}
	return jobs, nil
}

// Get returns the current state of a job, including its artifact once done.
func (c *Client) Get(ctx context.Context, id string) (Job, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/jobs/"+url.PathEscape(id), nil)
	if err != nil {
		return Job{}, fmt.Errorf("queue get request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return Job{}, fmt.Errorf("queue get do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return Job{}, fmt.Errorf("%w: job %s", ErrNotFound, id)
	}
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return Job{}, fmt.Errorf("queue get: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var job Job
	if err := json.NewDecoder(resp.Body).Decode(&job); err != nil {
		return Job{}, fmt.Errorf("queue get decode: %w", err)
	}
	return job, nil
}

// Depth returns a snapshot of the queue state.
func (c *Client) Depth(ctx context.Context) (Stats, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/jobs/depth", nil)
	if err != nil {
		return Stats{}, fmt.Errorf("queue depth request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return Stats{}, fmt.Errorf("queue depth do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return Stats{}, fmt.Errorf("queue depth: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var stats Stats
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		return Stats{}, fmt.Errorf("queue depth decode: %w", err)
	}
	return stats, nil
}

// Health reports whether the queue service is reachable and healthy.
func (c *Client) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		return fmt.Errorf("queue health request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("queue health do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("queue health: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}
