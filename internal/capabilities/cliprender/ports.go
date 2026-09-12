package cliprender

// ports.go defines the narrow, technology-independent ports the parallel
// preparation phase consumes. Every adapter is wired at the composition root
// (internal/app) from concrete platform/application implementations; the
// capability never imports infrastructure, Drive, Whisper, or SQLite.
//
// Verdetto invariant (mirror of internal/capabilities/scripts/ports.go): no
// port returns a technology-specific type — every return value is a
// capability-owned type from this package.

import (
	"context"
)

// AssetResolver resolves a canonical asset_id to its registry identity.
// The concrete adapter reads the canonical asset registry (SQLite) and maps
// the row to an AssetRef. Fail-closed: an unknown asset_id is a typed error,
// never a silent empty ref.
type AssetResolver interface {
	ResolveAsset(ctx context.Context, assetID string) (*AssetRef, error)
}

// AssetMaterializer ensures an asset's bytes are available locally and
// returns the verified local artifact (path + sha256). It is idempotent:
// an already-local copy is returned without a download (FromCache=true).
// Fail-closed: an asset with neither a usable local copy nor a Drive source
// is a typed error, never a silent no-op path.
type AssetMaterializer interface {
	Materialize(ctx context.Context, ref AssetRef) (*MaterializedAsset, error)
}

// TranscriptResolver owns the canonical transcript mechanics: reuse the
// existing READY canonical text track (Lookup) or generate one from the
// materialized source audio (Generate). The capability owns the policy
// (reuse vs generate vs reuse_or_generate); the resolver owns the mechanics.
//
// Lookup returns (result, true, nil) when a READY track exists, (nil, false,
// nil) when none exists, and (nil, false, err) on a repository failure.
type TranscriptResolver interface {
	Lookup(ctx context.Context, in TranscriptInput) (*TranscriptResult, bool, error)
	Generate(ctx context.Context, in TranscriptInput, source *MaterializedAsset) (*TranscriptResult, error)
}

// ContractResolver resolves the output contract selected by the request into
// the fully-specified VeloxEditing contract. The canonical implementation is
// pure (contract.go); the port exists so tests and future contracts can
// inject alternatives.
type ContractResolver interface {
	Resolve(ctx context.Context, req *RenderRequest) (*ResolvedContract, error)
}

// RenderOutcome is the typed result of executing a sealed ClipRenderPlanV1
// through the RenderingGen/Chronon boundary. Every media fact (duration,
// geometry, copy policy, subtitle stage and encode timing) comes from the
// certified Chronon artifact; the concrete adapter never re-derives them.
type RenderOutcome struct {
	OutputPath string
	SizeBytes  int64
	// SHA256 is the CERTIFIED content digest of OutputPath. The rendering
	// boundary already streams the artifact through SHA-256 while downloading
	// it (verifying the queue's expected digest), so this is the certified
	// digest of the exact bytes on disk — never a second hash pass. Empty only
	// when a boundary could not certify the bytes; publication fails closed on
	// an uncertified artifact instead of silently re-reading it.
	SHA256      string
	DurationSec float64
	Width       uint32
	Height      uint32
	FPSNum      uint32
	FPSDen      uint32
	Backend     RenderBackend
	// FFmpegMS is retained as a read-only compatibility projection of the
	// canonical Metrics report; adapters must not calculate it independently.
	FFmpegMS          int64
	AudioCopyEligible *bool
	AudioEncodePasses *int
	SubtitleRasterCPU *bool
	// GPU byte counters are Chronon-owned (the queue artifact metrics); the
	// Rust zero-copy counters (gpu_copy_bytes / video_zero_copy) were removed
	// with the PATH B CUDA hybrid.
	GPUUploadBytes          *uint64
	GPUReadbackBytes        *uint64
	EncoderStagingCopyBytes *uint64
	NV12ToRGBAFrames        *uint64
	RGBAToNV12Frames        *uint64
	CUDACompositeFrames     *uint64
	GPUUtilizationAvg       *float64
	GPUUtilizationPeak      *float64
	NVENCUtilizationAvg     *float64
	NVDECUtilizationAvg     *float64
	VRAMUsedPeakMB          *uint64
	DecodeMS                *int64
	FilterGraphMS           *int64
	SubtitleRasterMS        *int64
	WatermarkRasterMS       *int64
	FrameConversionMS       *int64
	EncodeMS                *int64
	AudioMuxMS              *int64

	// Metrics is the sole canonical V2 execution report (metrics.go). All
	// backends (Chronon and FFmpeg) must populate this contract; legacy
	// scalar fields are read-only compatibility projections. The adapter fills
	// selection facts and derived aggregates. Phases without real
	// instrumentation stay NOT_INSTRUMENTED — never a fake zero.
	Metrics *RenderMetricsV2

	// ChrononTiming* carry the reference to the raw deep-profile timing
	// sidecar (the verbatim `<output>.timing.json`, including the unbounded
	// per-frame array) that RenderingGen preserved in its object store. Only
	// the content-addressed reference travels with the outcome — the array
	// itself is never inlined. Empty when the sidecar was not preserved.
	ChrononTimingStorageKey  string
	ChrononTimingURL         string
	ChrononTimingSHA256      string
	ChrononTimingSizeBytes   int64
	ChrononTimingContentType string
}

// RenderExecutor executes the sealed ClipRenderPlanV1 in a single Chronon
// render pass through RenderingGen. The plan is fully resolved before this port is
// invoked — the executor makes zero business selections. Fail-closed: the
// output must exist and be non-empty on success; a missing or drifted
// artifact is a typed error, never a silent no-op.
type RenderExecutor interface {
	Render(ctx context.Context, plan ClipRenderPlanV1) (*RenderOutcome, error)
}

// AsyncRenderExecutor is the SPLIT form of RenderExecutor: the same render
// boundary exposed as its two halves so the Master slot is released while
// RenderingGen renders (Wave B).
//
//   - Submit performs everything up to and including the durable enqueue of
//     the remote render (plan mapping, asset prefetch, submit). It must return
//     as soon as the remote job is accepted — it must NOT wait for it.
//   - Settle performs the post-submit half: wait for the terminal state,
//     require the certified Chronon artifact, download it (hashing in the same
//     pass) and project the render outcome.
//
// The split is resumable by construction: the remote job id is the sealed
// plan's deterministic RunID, so a Settle that runs after a process restart
// addresses the same remote render without any process-local state.
//
// A boundary may implement BOTH RenderExecutor and AsyncRenderExecutor: the
// worker uses the split form when it is available and the blocking Render when
// it is not, so no existing deployment breaks on this cut.
type AsyncRenderExecutor interface {
	Submit(ctx context.Context, plan ClipRenderPlanV1) error
	Settle(ctx context.Context, plan ClipRenderPlanV1) (*RenderOutcome, error)
}

// OutputProbe is the capability-owned projection of the rendered output's
// media facts, collected by the OutputProber port AFTER render_clip. The
// probe reads the actual bytes on disk — contract validation never trusts
// what the render boundary claimed to encode. Every field is exact for
// assembly-ready gate.
type OutputProbe struct {
	Container        string
	HasVideo         bool
	VideoCodec       string
	VideoProfile     string
	VideoLevel       string
	PixelFormat      string
	Width            int
	Height           int
	FPS              float64 // legacy float projection for logs
	FPSNum           int
	FPSDen           int
	VideoTimeBaseNum int
	VideoTimeBaseDen int
	AudioTimeBaseNum int
	AudioTimeBaseDen int
	SARNum           int
	SARDen           int
	ColorRange       string
	ColorSpace       string
	ColorTransfer    string
	ColorPrimaries   string
	FieldOrder       string
	KeyframeInterval int
	HasAudio         bool
	AudioCodec       string
	AudioProfile     string
	SampleRate       int
	Channels         int
	ChannelLayout    string
	AudioBitrate     string
	VideoStreams     int
	AudioStreams     int
	StreamOrder      string
	StartPTS         int64
}

// OutputProber probes the rendered output file. The concrete adapter uses
// the canonical Rust probe boundary; the capability owns the comparison
// against the resolved contract (ValidateContract). Fail-closed: a missing
// or unreadable output is a typed error, never a silent empty probe.
type OutputProber interface {
	ProbeOutput(ctx context.Context, path string) (*OutputProbe, error)
}

// RenderPublishInput is the fully-resolved input for the publish + commit
// phase. Every value comes from the worker (sealed plan, render outcome,
// resolved contract, transcript, sidecar artifact); the publisher never
// resolves anything itself.
type RenderPublishInput struct {
	RunID         string
	SourceAssetID string
	SourceTitle   string
	OutputPath    string
	Outcome       *RenderOutcome
	Contract      *ResolvedContract
	Transcript    *TranscriptResult
	// Subtitles is the compiled ASS artifact. Drive publication is gated on
	// its Mode: burned subtitles are baked into the video frames and are
	// NEVER uploaded; only an explicitly sidecar-mode artifact is uploaded
	// as an .ass sidecar next to the clip.
	Subtitles     *SubtitleArtifact
	DriveFolderID string // fully-resolved leaf folder (the worker resolved subfolder_name; the publisher never creates folders)

	// CertifiedSHA256/CertifiedSizeBytes certify the EXACT bytes published at
	// OutputPath. The worker forwards the digest the producing boundary
	// already computed (RenderOutcome.SHA256 for a plain render, the overlay
	// compositor's digest when an overlay was composited) so publication never
	// performs its own full-file hash pass. Fail-closed: an empty digest or a
	// non-positive size is a typed error — the publisher never silently
	// re-reads the artifact to reconstruct a digest the caller did not certify.
	CertifiedSHA256    string
	CertifiedSizeBytes int64
}

// PublicationMetrics carries the publisher's OWN measured publication
// sub-phase walls. The publisher is the single chronometer owner for its
// phases; the worker projects these into RenderMetricsV2 (publication_total_ms
// / artifact_publish_ms / drive_upload_ms) and never re-times publication
// when this report is present. HashMS/TaxonomyResolveMS/AssetCommitMS run
// sequentially (their sum is the local artifact work); VideoUploadMS and
// SidecarUploadMS run concurrently (the drive upload wall is their max,
// never their sum).
type PublicationMetrics struct {
	HashMS            int64
	VideoUploadMS     int64
	SidecarUploadMS   int64
	TaxonomyResolveMS int64
	AssetCommitMS     int64
	TotalMS           int64
}

// RenderPublishResult is the typed outcome of publishing + committing the
// derived asset. AssetID is the canonical media_assets.id; DriveLink is the
// canonical Drive web link (never reconstructed by the worker). Publish is
// the publisher-owned measurement report projected into the canonical V2
// execution report.
type RenderPublishResult struct {
	AssetID     string
	DriveFileID string
	DriveLink   string
	// DrivePending means the artifact is committed and the Drive upload was
	// durably handed to the outbox. It is intentionally distinct from an
	// upload failure: the render job may complete while the external delivery
	// continues in the background.
	DrivePending  bool
	SizeBytes     int64
	SidecarFileID string
	SidecarLink   string
	Publish       *PublicationMetrics
}

// EventClipRenderDriveDeliveryRequested is emitted atomically with the
// rendered media asset when clip.render uses asynchronous Drive delivery.
// The outbox consumer uploads the durable local artifact and then reconciles
// the Drive location on the canonical asset row.
const EventClipRenderDriveDeliveryRequested = "clip.render.drive_delivery.requested.v1"

// ClipRenderDriveDeliveryRequest is the durable outbox payload for a rendered
// clip's external Drive projection. It contains no credentials and is
// idempotent by AssetID + ContentHash + FolderID.
type ClipRenderDriveDeliveryRequest struct {
	SchemaVersion string `json:"schema_version"`
	AssetID       string `json:"asset_id"`
	RunID         string `json:"run_id"`
	SourceAssetID string `json:"source_asset_id"`
	LocalPath     string `json:"local_path"`
	Filename      string `json:"filename"`
	FolderID      string `json:"folder_id"`
	ContentHash   string `json:"content_hash"`
	SizeBytes     int64  `json:"size_bytes"`
}

// RenderPublisher publishes the validated output to Drive through the
// canonical delivery publisher and commits it as a derived media asset
// inside ONE SQLite transaction (mirror of the canonical final-audio
// publisher). Fail-closed: an unwired publisher, a missing output, or a
// failed commit is a typed error — the job never reports success without
// the derived asset durably committed.
type RenderPublisher interface {
	Publish(ctx context.Context, in RenderPublishInput) (*RenderPublishResult, error)
}

// DestinationFolderResolveInput is the fully-resolved input for the
// DestinationFolderResolver: the Drive root the caller selected plus the
// optional human subfolder name (typically the script/batch title). The
// resolver never re-derives either — it maps root + name to the leaf folder.
type DestinationFolderResolveInput struct {
	RootFolderID  string
	SubfolderName string
}

// DestinationFolderResolver resolves (create-or-reuse) the Drive leaf folder
// a clip.render batch publishes into: RootFolderID/<SafeName(SubfolderName)>.
// The canonical adapter routes through delivery.Publisher.ResolveFolder — the
// same publisher that performs the final upload — so folder creation stays in
// ONE canonical owner (never a second Drive reach-through inside cliprender).
//
// The worker calls this ONCE per job when the request carries
// destination.subfolder_name. All clips of one script/batch carry the same
// name, so sequential or concurrent jobs converge on a single shared leaf
// folder (EnsureFolder is idempotent + singleflight-deduped in-process). The
// publisher stays dumb: it receives the fully-resolved leaf folder ID and
// never creates folders itself. Fail-closed: a resolver returning an empty
// folder ID is a typed error, never a silent root fallback.
type DestinationFolderResolver interface {
	ResolveDestinationFolder(ctx context.Context, in DestinationFolderResolveInput) (string, error)
}

// OverlaySegment is the materialized overlay artifact the final video
// composites over the source. LocalPath is the local, content-addressed
// file; SHA256 is its verified digest. RenderKey is the same content
// key the OverlayRefSpec declares, so the resolved segment can be proven
// to be the exact artifact that render_job_id produced.
type OverlaySegment struct {
	RenderJobID string
	RenderKey   string
	LocalPath   string
	SHA256      string
	SizeBytes   int64
}

// OverlayResolveInput is the fully-resolved input for the overlay segment
// resolution: the lineage the request declares (render_job_id + render_key).
// The resolver never re-derives anything — it maps the declared identity to
// the materialized artifact.
type OverlayResolveInput struct {
	RenderJobID string
	RenderKey   string
}

// OverlaySegmentResolver resolves the overlay.render artifact (the rendered
// overlay segment) from the lineage the clip.render request declares. This
// is the "render_job_id → artifact" hop of the compositing chain. Fail-
// closed: an unresolvable segment (unknown job, missing artifact, hash
// mismatch) is a typed error — the worker never composites a phantom
// segment.
type OverlaySegmentResolver interface {
	Resolve(ctx context.Context, in OverlayResolveInput) (*OverlaySegment, error)
}

// NOTE: the overlay COMPOSITOR port was DEMOLISHED with the single-pass
// overlay cutover. A declared overlay is no longer blended by a post-render
// pass (which encoded the whole clip a second time); the resolved segment is
// sealed into ClipRenderPlanV1.Overlay and composited inside the same Chronon
// render as a timed video layer. OverlaySegmentResolver is the only remaining
// overlay port, and scripts/ci/check_clip_render_cutover.sh fails on any new
// FFmpeg overlay compositor caller.
