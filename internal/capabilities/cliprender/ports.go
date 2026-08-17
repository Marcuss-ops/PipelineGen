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
	OutputPath        string
	SizeBytes         int64
	DurationSec       float64
	Width             uint32
	Height            uint32
	FPS               uint32
	FFmpegMS          int64
	AudioCopyEligible *bool
	AudioEncodePasses *int
	SubtitleRasterCPU *bool
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
// what the render boundary claimed to encode.
type OutputProbe struct {
	HasVideo    bool
	VideoCodec  string
	PixelFormat string
	Width       int
	Height      int
	FPS         float64
	HasAudio    bool
	AudioCodec  string
	SampleRate  int
	Channels    int
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

// RenderPublishResult is the typed outcome of publishing + committing the
// derived asset. AssetID is the canonical media_assets.id; DriveLink is the
// canonical Drive web link (never reconstructed by the worker).
type RenderPublishResult struct {
	AssetID       string
	DriveFileID   string
	DriveLink     string
	SizeBytes     int64
	SidecarFileID string
	SidecarLink   string
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
