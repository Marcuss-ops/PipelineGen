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
	"errors"
	"time"
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
// (reuse vs explicit generate); the resolver owns the mechanics.
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
	// OutputPath is the OPTIONAL local materialization of the artifact. It is
	// empty on the canonical path: the render boundary certifies the artifact
	// and returns the durable locator below, and only a consumer that really
	// needs bytes on disk materializes it (RenderArtifactMaterializer).
	OutputPath string
	SizeBytes  int64
	// StorageKey, ArtifactURL and ContentType are the certified DURABLE LOCATOR
	// of the rendered artifact in RenderingGen's object store. They are the
	// canonical artifact identity: the render path stages no local copy and the
	// Drive outbox streams object-store → Drive from this locator. Empty only
	// for a legacy/test boundary that materialized OutputPath itself.
	StorageKey  string
	ArtifactURL string
	ContentType string
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
	// Container, VideoCodec, VideoProfile, PixelFormat and AudioStreams are the
	// structural facts RenderingGen certified on the artifact it produced. They
	// are the certification owner for the dimensions the local Rust probe
	// cannot report (codec profile above all) and the value the local probe is
	// cross-checked against before publication. Empty/zero when the certified
	// boundary did not report them (a legacy or test renderer).
	Container    string
	VideoCodec   string
	VideoProfile string
	PixelFormat  string
	AudioStreams int
	// Facts is the COMPLETE structural certification RenderingGen probed on
	// the artifact it produced (see OutputFacts). It is the certification
	// owner for every output-contract dimension the local Rust probe cannot
	// observe — timebase, SAR, colour, field order, GOP interval, the full
	// audio block. Nil when the boundary certified only the flat summary (a
	// legacy worker); consumers then fall back to the flat fields above.
	Facts   *OutputFacts
	Backend RenderBackend
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

	// ChrononTimingSource, ChrononTimingReadBytes and ChrononTimingFetchMS
	// report which sidecar the hot path read (“summary” or “raw”) and what it
	// cost. They are producer-observed diagnostics, not persisted cache fields.
	ChrononTimingSource    string
	ChrononTimingReadBytes int64
	ChrononTimingFetchMS   int64
}

// RenderExecutor is the canonical clip.render boundary, exposed as its TWO
// HALVES so the caller's worker slot is released while RenderingGen renders.
// The plan is fully resolved before this port is invoked — the executor makes
// zero business selections.
//
//   - Submit performs everything up to and including the durable enqueue of
//     the remote render (plan mapping, asset prefetch, submit). It returns as
//     soon as the remote job is accepted — it must NOT wait for it.
//   - Settle performs the post-submit half: wait for the terminal state,
//     require the certified Chronon artifact, download it (hashing in the same
//     pass) and project the render outcome. Fail-closed: the output must exist
//     and be non-empty on success; a missing or drifted artifact is a typed
//     error, never a silent no-op.
//
// The split is resumable by construction: the remote job id is the sealed
// plan's deterministic RunID, so a Settle that runs after a process restart
// addresses the same remote render without any process-local state.
//
// The historical BLOCKING `Render(ctx, plan)` form (Submit immediately
// followed by Settle in one call) was DELETED in the 2026-09-13 clip.render
// audit (P2). Its last production caller was the localization adapter, which
// now drives the two halves explicitly, and keeping it let a boundary that
// cannot release its slot stay wired by accident. There is exactly one way to
// drive a render now.
type RenderExecutor interface {
	Submit(ctx context.Context, plan ClipRenderPlanV1) error
	Settle(ctx context.Context, plan ClipRenderPlanV1) (*RenderOutcome, error)
}

// RenderJobIDResolver is an optional extension for executors that submit a
// plan as a durable family. The ordinary plan RunID remains the plain-render
// address; a chunked executor returns its assembly anchor address here.
type RenderJobIDResolver interface {
	RenderJobID(plan ClipRenderPlanV1) string
}

// RenderExecutorWithJobID lets a restartable settle continuation wait on the
// executor's durable remote address when it differs from plan.RunID.
type RenderExecutorWithJobID interface {
	SettleWithJobID(ctx context.Context, plan ClipRenderPlanV1, renderJobID string) (*RenderOutcome, error)
}

// ErrRenderPending marks a settle attempt that ran out of its WAIT BUDGET while
// the remote render is still running. It is a WAIT, not a failure: the settle
// continuation hands its attempt back with job.DeferredAfter (a deferral spends
// no retry budget) and asks again, so a lane is not parked on a render that may
// take minutes or hours.
//
// It is deliberately distinct from every render failure: a render that FAILED,
// a queue that is unreachable, and an artifact that failed certification all
// still propagate as errors. Only "not terminal yet" is this sentinel.
var ErrRenderPending = errors.New("clip.render: remote render is not terminal yet")

// ErrRenderStalled marks a remote render that stopped reporting progress and
// was cancelled by the settle watchdog. It is a FAILURE, not a wait: retrying
// the same render identity cannot help, and the child's GPU lane has already
// been released. The executor wraps the typed stall report (child id, last
// frame, age) so the job error is actionable without the logs.
var ErrRenderStalled = errors.New("clip.render: remote render stalled without progress")

// RenderExecutorDeferring is the OPTIONAL capability a render boundary
// implements when its settle can be BOUNDED instead of blocking.
//
// The caller states the budget, because the caller owns the policy (how long one
// attempt may hold a lane, and whether it may keep asking at all); the executor
// owns the transport and reports ErrRenderPending when the budget expires.
//
//   - budget <= 0 waits with the executor's full timeout: exactly the blocking
//     contract of SettleWithJobID, which is why a boundary that does not
//     implement this interface keeps working unchanged.
//   - the returned outcome is non-nil only for a CERTIFIED terminal render; a
//     pending wait returns (nil, ErrRenderPending) and the caller defers.
type RenderExecutorDeferring interface {
	SettleWithin(ctx context.Context, plan ClipRenderPlanV1, renderJobID string, budget time.Duration) (*RenderOutcome, error)
}

// RenderArtifactMaterializer is the OPTIONAL capability a render boundary
// implements when it can fetch the certified artifact it locates in the object
// store on demand. It exists because `Settle` is locator-first: the canonical
// path never writes the video bytes to disk, and only a consumer that genuinely
// needs a local file (localization, any future overlay that cannot be composed
// remotely) materializes. Fail-closed: a materializer that cannot fetch the
// exact certified bytes is a typed error, never a partial file.
type RenderArtifactMaterializer interface {
	// Materialize downloads the artifact located by outcome into destPath,
	// verifies size + certified digest while streaming, and returns the
	// verified local path.
	Materialize(ctx context.Context, outcome *RenderOutcome, destPath string) (string, error)
}

// OutputProbe is the capability-owned projection of the rendered output's
// media facts, projected from the artifact RenderingGen CERTIFIED
// (OutputProbeFromCertified). It is the contract gate's input; the render
// boundary is the single certification owner, and the redundant local byte
// probe was deleted in the 2026-09-13 audit. Every field is exact for the
// assembly-ready gate.
type OutputProbe struct {
	Container         string
	HasVideo          bool
	VideoCodec        string
	VideoProfile      string
	VideoLevel        string
	CodecConfigSHA256 string
	PixelFormat       string
	Width             int
	Height            int
	FPS               float64 // legacy float projection for logs
	FPSNum            int
	FPSDen            int
	VideoTimeBaseNum  int
	VideoTimeBaseDen  int
	AudioTimeBaseNum  int
	AudioTimeBaseDen  int
	SARNum            int
	SARDen            int
	ColorRange        string
	ColorSpace        string
	ColorTransfer     string
	ColorPrimaries    string
	FieldOrder        string
	KeyframeInterval  int
	HasAudio          bool
	AudioCodec        string
	AudioProfile      string
	SampleRate        int
	Channels          int
	ChannelLayout     string
	AudioBitrate      string
	VideoStreams      int
	AudioStreams      int
	StartPTS          int64
}

// RenderPublishInput is the fully-resolved input for the publish + commit
// phase. Every value comes from the worker (sealed plan, render outcome,
// resolved contract, transcript, sidecar artifact); the publisher never
// resolves anything itself.
type RenderPublishInput struct {
	RunID         string
	SourceAssetID string
	SourceTitle   string
	// OutputPath is the OPTIONAL local materialization. Empty on the canonical
	// locator-first path.
	OutputPath string
	// ArtifactStorageKey/ArtifactURL/ArtifactContentType are the certified
	// durable locator committed with the asset and handed to the Drive outbox so
	// delivery streams object-store → Drive without a local copy.
	ArtifactStorageKey  string
	ArtifactURL         string
	ArtifactContentType string
	Outcome             *RenderOutcome
	Contract            *ResolvedContract
	Transcript          *TranscriptResult
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
