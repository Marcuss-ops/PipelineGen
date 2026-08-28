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
// through the Rust render_clip boundary (feature spec §6/§9). Every media
// fact (duration, geometry, copy policy, CPU subtitle stage, native encode
// wall time) comes from the Rust response metadata; the concrete adapter
// never re-derives them.
type RenderOutcome struct {
	OutputPath  string
	SizeBytes   int64
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
	GPUCopyBytes      *uint64
	DecodeMS          *int64
	FilterGraphMS     *int64
	SubtitleRasterMS  *int64
	WatermarkRasterMS *int64
	FrameConversionMS *int64
	EncodeMS          *int64
	AudioMuxMS        *int64

	// Metrics is the sole canonical V2 execution report (metrics.go). All
	// backends (CUDA, Chronon and FFmpeg) must populate this contract; legacy
	// scalar fields are read-only compatibility projections. The adapter fills
	// selection facts and derived aggregates. Phases without real
	// instrumentation stay NOT_INSTRUMENTED — never a fake zero.
	Metrics *RenderMetricsV2
}

// RenderExecutor executes the sealed ClipRenderPlanV1 in a single render
// pass on the Rust boundary. The plan is fully resolved before this port is
// invoked — the executor makes zero business selections. Fail-closed: the
// output must exist and be non-empty on success; a missing or drifted
// artifact is a typed error, never a silent no-op.
type RenderExecutor interface {
	Render(ctx context.Context, plan ClipRenderPlanV1) (*RenderOutcome, error)
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
	OutputPath    string
	Outcome       *RenderOutcome
	Contract      *ResolvedContract
	Transcript    *TranscriptResult
	Subtitles     *SubtitleArtifact // sidecar mode: uploaded alongside the clip
	DriveFolderID string
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
	AssetID       string
	DriveFileID   string
	DriveLink     string
	SizeBytes     int64
	SidecarFileID string
	SidecarLink   string
	Publish       *PublicationMetrics
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

// OverlayCompositeInput is the fully-resolved input for the compositing
// pass. Every value comes from the worker (source clip, resolved segment,
// declared window, output path); the compositor never resolves anything
// itself. Width/Height are legacy; Contract is the assembly-ready contract
// (FPS/timebase/color/GOP) that the compositor MUST honor exactly.
type OverlayCompositeInput struct {
	RunID      string
	SourcePath string // the rendered source clip (rendered-clip.mp4)
	Segment    *OverlaySegment
	StartUS    int64 // declared window on the final timeline
	EndUS      int64
	OutputPath string
	Width      int // target geometry (contract) — deprecated: use Contract
	Height     int
	Contract   *ResolvedContract // assembly-ready contract (FPS/timebase/color exact)
}

// OverlayCompositeResult is the typed outcome of the compositing pass.
type OverlayCompositeResult struct {
	OutputPath  string
	SHA256      string
	SizeBytes   int64
	CompositeMS int64
}

// OverlayCompositor blends the rendered overlay segment onto the source
// clip at the declared [start_us, end_us) window, producing the final video
// that contains the overlay in its pixels. Fail-closed: a missing segment,
// an invalid window, or a failed blend is a typed error — the final video
// is never reported as containing an overlay it does not actually carry.
type OverlayCompositor interface {
	Composite(ctx context.Context, in OverlayCompositeInput) (*OverlayCompositeResult, error)
}
