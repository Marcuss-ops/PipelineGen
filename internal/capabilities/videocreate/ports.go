package videocreate

import (
	"context"
	"encoding/json"
	"fmt"

	audio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	steps "github.com/Marcuss-ops/PipelineGen/internal/capabilities/execution/steps"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

// ── Ports (AGENTS.md Pattern 0: dependency inversion) ─────────────────
//
// Every dependency of the workflow is a capability-owned port. The
// composition root binds the canonical implementations (the job
// registry, the SearchAggregator, the rustexec media plane, the
// delivery publisher); tests bind hermetic fakes. The workflow itself
// therefore never spawns media binaries, never calls itself over HTTP
// and never writes SQL.

// ChildJobRequest is one child fan-out instruction.
type ChildJobRequest struct {
	JobType        string
	IdempotencyKey string
	CorrelationID  string
	Payload        json.RawMessage
	Project        string
	VideoName      string
}

// ChildJobs is the canonical job-registry surface the workflow fans
// out through. EnqueueChild is IDEMPOTENT on IdempotencyKey (the broker
// answers a duplicate with the existing child id); WaitTerminal blocks
// until the child is terminal or the context ends.
type ChildJobs interface {
	EnqueueChild(ctx context.Context, req ChildJobRequest) (childJobID string, err error)
	WaitTerminal(ctx context.Context, childJobID string) (*job.Job, error)
	// HasHandler is the composition-time/runtime preflight used to reject a
	// workflow before it starts expensive stages when a required child lane
	// (notably assembly) has no live consumer.
	HasHandler(jobType string) bool
}

// MediaSearchRequest is one media-discovery query (§10).
type MediaSearchRequest struct {
	Topic    string
	Language string
	Sources  []string
	Limit    int
}

// MediaCandidate is one discovery hit carrying only durable identity
// facts (§11: asset_id / source / url / duration / media type — never a
// local path as the inter-stage contract).
type MediaCandidate struct {
	AssetID    string
	Source     string
	SourceURL  string
	Title      string
	MediaType  string
	DurationMS int64
	Score      float64
	// SourceVideoID + StartSec/EndSec are the canonical YouTube clip
	// window recovered from the registry asset id (kernel/asset/detail
	// format `yt_{videoID}_{startSec}_{endSec}_{policyVer}`). When
	// SourceVideoID is non-empty the acquisition child re-extracts the
	// EXACT window (explicit segments) instead of re-deriving one.
	SourceVideoID string
	StartSec      int
	EndSec        int
}

// TranscriptReady is the render precondition seam: 07_render requests
// transcript.mode=reuse, which fails closed unless the canonical text-track
// store holds a READY transcript in the requested language. Translations are
// materialized ASYNCHRONOUSLY after media acquisition (asset.text.materialize
// fan-out), so the workflow must wait for that READY track before fanning out
// its render children — never re-derive or force-decode a transcript itself.
type TranscriptReady interface {
	WaitTranscriptReady(ctx context.Context, assetID, language string) error
}

// MediaSearch is the canonical aggregator port (the same backend
// /api/media/search serves). The workflow CONSUMES the existing search
// and ranking; it never re-implements either.
type MediaSearch interface {
	Search(ctx context.Context, req MediaSearchRequest) ([]MediaCandidate, error)
}

// MasterRequest compiles the canonical final-audio master (§13:
// voiceover + BGM + SFX → render_audio_plan → canonical_final_audio).
// Plan/Assets are the audio compiler's own output types — the workflow
// reuses the audio pipeline's compiled plan verbatim instead of
// re-deriving mix decisions.
type MasterRequest struct {
	Plan       audio.CompiledAudioPlan
	Assets     audio.ResolvedAudioAssets
	OutputPath string
}

// MasteredAudio is the certified canonical final-audio master: the
// audio plane's copy-eligibility facts (AAC-LC, sample rate, channels,
// duration, hashes) plus its local materialization for the mux step.
type MasteredAudio struct {
	Asset audio.FinalAudioAsset
	Path  string
}

// MuxRequest is the §16 final mux (mux_audio_copy: -c:v copy -c:a copy,
// never a re-encode).
type MuxRequest struct {
	VideoPath  string
	FinalAudio MasteredAudio
	OutputPath string
}

// MuxedVideo is the muxed final MP4 materialization.
type MuxedVideo struct {
	Path string
}

// AudioMaster is the canonical media-plane audio surface (VeloxEditing:
// rustexec render_audio_plan + mux_audio_copy). FFmpeg is NEVER spawned
// by this workflow: this port is the single sanctioned execution seam.
type AudioMaster interface {
	Master(ctx context.Context, req MasterRequest) (MasteredAudio, error)
	Mux(ctx context.Context, req MuxRequest) (MuxedVideo, error)
}

// ProbeFacts are the ffprobe-equivalent facts the §17 verification gate
// consumes. Owned here (not borrowed from a platform type) so the
// verification contract is decoupled from the executor that fills it.
type ProbeFacts struct {
	Path             string
	SizeBytes        int64
	DurationMS       int64
	Width            int
	Height           int
	FPS              float64
	VideoCodec       string
	AudioCodec       string
	SampleRate       int
	Channels         int
	VideoStreamCount int
	AudioStreamCount int
}

// MediaProber is the canonical probe port (rustexec VideoProcessor.Probe
// — the media capability is the ONLY owner of ffprobe).
type MediaProber interface {
	Probe(ctx context.Context, path string) (ProbeFacts, error)
}

// AssembleSegment is one copy-certified scene segment on the
// VeloxEditing assembly boundary (§15), in timeline order.
type AssembleSegment struct {
	AssetID    string `json:"asset_id"`
	SHA256     string `json:"content_sha256,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	// CopyCertified is the segment's copy-safety certification (closed
	// GOP, first-frame keyframe): the assembler is copy-only and never
	// re-encodes, so an uncertified segment is a hard failure.
	CopyCertified bool   `json:"copy_certified"`
	ContractID    string `json:"contract_id,omitempty"`
	// Contract is the segment's reported output-contract block — the
	// cross-input identity the assembly gate enforces before any
	// packet-copy concatenation.
	Contract RenderContractFacts `json:"contract,omitempty"`
	// LocalRef carries the worker-local materialization (wire key
	// local_path; execution detail the media plane works on).
	LocalRef
}

// AssembleScene is one timeline position of the assembled video.
type AssembleScene struct {
	SceneID string `json:"scene_id"`
	AssetID string `json:"asset_id"`
}

// AssembleRequest is one canonical assembly dispatch (§15): the
// timeline-ordered, copy-certified scene segments plus their scene
// timeline.
type AssembleRequest struct {
	AssemblyID string
	ParentJob  *job.Job
	Segments   []AssembleSegment
	Timeline   []AssembleScene
	OutputPath string
}

// AssembleResult is the assembled video identity (the VeloxEditing
// media plane's artifact). ChildJobIDs is always empty: this boundary is
// the media plane itself (assemble_copy), never a fan-out of child jobs.
type AssembleResult struct {
	ArtifactID  string
	Path        string
	SHA256      string
	SizeBytes   int64
	DurationMS  int64
	ChildJobIDs []string
}

// Assembler is the CANONICAL production assembly boundary: the
// VeloxEditing media plane (pipelinegen-muscles assemble_copy /
// video.assemble.copy.v1) — packet-copy concatenation of copy-certified
// scene segments in timeline order with zero decode, zero encode and
// zero compositing. The Rust side re-probes every input and enforces
// the shared copy-safety contract fail-closed (per-input certification
// + cross-input stream identity); there is deliberately NO re-encode
// fallback. This is NOT RenderingGen's ParentFinalizer → daemon
// ASSEMBLE_SEGMENTS lane (that assembles the chunks of ONE render job).
type Assembler interface {
	Assemble(ctx context.Context, req AssembleRequest) (AssembleResult, error)
}

// PublishRequest is the §18 artifact publication (the normal artifact
// spine: Drive upload → media identity → content hash).
type PublishRequest struct {
	// LocalRef carries the worker-local materialization to publish
	// (wire key local_path; execution detail).
	LocalRef
	Filename              string
	MIMEType              string
	Kind                  string
	SHA256                string
	SizeBytes             int64
	DurationMS            int64
	AssetID               string
	Project               string
	DeliveryDestinationID string
}

// PublishedArtifact is the durable published identity returned to the
// caller contract (final_video.media_url / drive_file_id).
type PublishedArtifact struct {
	AssetID string
	// DriveRef carries the Drive location (wire key drive_file_id).
	DriveRef
	MediaURL    string
	DownloadURL string
	SHA256      string
	SizeBytes   int64
}

// ArtifactPublisher is the artifact-publishing seam (the existing
// delivery publisher + media identity resolution). The workflow stores
// only what this returns — never a local path — in its result.
type ArtifactPublisher interface {
	Publish(ctx context.Context, req PublishRequest) (PublishedArtifact, error)
}

// Deps is the workflow's dependency bundle (ports only; the
// composition root binds canonical implementations). Steps is the
// canonical resumable step store (internal/capabilities/execution/
// steps.Store). Log is optional; every other field is mandatory and
// checked at construction (godlike/05 fail-closed: a workflow missing
// its job registry must not start).
type Deps struct {
	Steps     steps.Store
	Children  ChildJobs
	Search    MediaSearch
	Audio     AudioMaster
	Probe     MediaProber
	Assembler Assembler
	Publish   ArtifactPublisher
	// Texts sequences 07_render behind the requested-language transcript
	// readiness (async translation materialization). Wired in production;
	// nil skips the wait (the render child stays the fail-closed gate).
	Texts TranscriptReady
	// Workspace is the PERSISTENT per-job workspace root. Media-plane
	// materializations live under <Workspace>/<jobID>/ so a restart
	// finds the completed mux/assembly again (/tmp would silently
	// lose them). Mandatory.
	Workspace string
	Log       *zap.Logger
}

// Validate fails closed on an incomplete dependency bundle (godlike/05:
// a workflow missing its job registry or step store must not start).
func (d Deps) Validate() error {
	for name, missing := range map[string]bool{
		"Steps":     d.Steps == nil,
		"Children":  d.Children == nil,
		"Search":    d.Search == nil,
		"Audio":     d.Audio == nil,
		"Probe":     d.Probe == nil,
		"Assembler": d.Assembler == nil,
		"Publish":   d.Publish == nil,
		"Workspace": d.Workspace == "",
	} {
		if missing {
			return fmt.Errorf("videocreate: Deps.%s is not wired", name)
		}
	}
	return nil
}
