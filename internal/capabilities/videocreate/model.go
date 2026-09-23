// Package videocreate owns the durable video.create workflow: the ONE
// parent job that takes a typed video request (topic / language /
// duration / media sources) to a verified, published final video.
//
// ── Architecture contract (godlike/06 one owner per fact) ─────────────
//
// This package is an ORCHESTRATOR, never a second PipelineGen inside
// PipelineGen. It owns the stage machine and the durable resume
// authority, and it reaches every capability through the canonical
// surfaces only:
//
//	children     → the canonical job registry (script.generate,
//	               youtube_clip.extract, media.stock, voiceover.generate,
//	               clip.render, assembly.prepare/finalize). Never
//	               exec.Command("yt-dlp") / exec.Command("ffmpeg"):
//	               media-binary execution is owned by
//	               internal/platform/media/rustexec and reached through
//	               the MediaProber/AudioMaster ports.
//	media search → the canonical SearchAggregator port (the same
//	               /api/media/search backend), never an HTTP call to
//	               itself and never a re-implementation of ranking.
//	durable state→ internal/capabilities/execution/steps (the canonical
//	               resumable step store). The WorkflowState document is
//	               a PROJECTION of those rows, never a second authority.
//	digests      → internal/kernel/digest (SHA-256 SSOT).
//	assembly     → the canonical assembly.prepare/finalize contract
//	               (internal/kernel/assembly), never a private concat.
//
// Stage outputs exchange DURABLE IDENTITIES (asset ids, content SHA-256,
// Drive file ids, durations, media types) — never local filesystem paths
// as the inter-stage contract. A port may carry a local materialization
// path as an execution detail (the media plane works on files); that path
// is never part of the durable workflow state or the job result.
package videocreate

import "errors"

// WorkflowVersion is the durable state document version. A bump is a
// wire-visible change: resume refuses a state document it cannot read.
const WorkflowVersion = 1

// Stage is the operator/caller-facing workflow stage name (the exact
// vocabulary the remote Calendar projects on its progress card).
type Stage string

const (
	StageScripting    Stage = "SCRIPTING"
	StageMediaSearch  Stage = "MEDIA_SEARCH"
	StageMediaAcquire Stage = "MEDIA_ACQUIRE"
	StageVoiceover    Stage = "VOICEOVER"
	StageAudioMaster  Stage = "AUDIO_MASTER"
	StageOverlayPlan  Stage = "OVERLAY_PLAN"
	StageRendering    Stage = "RENDERING"
	StageAssembling   Stage = "ASSEMBLING"
	StageAudioFinal   Stage = "AUDIO_FINALIZE"
	StageVerifying    Stage = "VERIFYING"
	StageFinalizing   Stage = "FINALIZING"
)

// StepSpec is one durable workflow step: the canonical bridge between
// the resumable step store (step_key), the caller-facing stage name and
// the progress band the remote Calendar renders.
//
// StepKey uses the steps.Store lexical-ordering convention
// ("01_script", ...) so FirstNonCompleted resumes in pipeline order.
type StepSpec struct {
	StepKey   string
	Stage     Stage
	Title     string
	BandStart int
	BandEnd   int
	// Optional marks steps that a payload can render unnecessary
	// (voiceover=false → 04_voiceover is SKIPPED, overlays=false →
	// 06_overlay_plan is SKIPPED). A skipped step still records a
	// durable completion so resume never re-decides it.
	Optional bool
}

// WorkflowSteps is the canonical ordered stage ladder of video.create
// (the plan's receive→script→scene→media→download→cut→voiceover→audio
// master→overlay→render→assembly→mux→ffprobe→SHA-256→publish chain,
// materialised as eleven resumable steps).
//
// Progress bands are the canonical §20 mapping
// (SCRIPTING 5-20 … FINALIZING 99-100); the two pre-render audio steps
// share the VOICEOVER window so the RENDERING band starts at the exact
// documented 55.
var WorkflowSteps = []StepSpec{
	{StepKey: "01_script", Stage: StageScripting, Title: "script + scene plan", BandStart: 5, BandEnd: 20},
	{StepKey: "02_media_search", Stage: StageMediaSearch, Title: "media discovery", BandStart: 20, BandEnd: 30},
	{StepKey: "03_media_acquire", Stage: StageMediaAcquire, Title: "download + cut + normalize + register", BandStart: 30, BandEnd: 45},
	{StepKey: "04_voiceover", Stage: StageVoiceover, Title: "voiceover", BandStart: 45, BandEnd: 50, Optional: true},
	{StepKey: "05_audio_master", Stage: StageAudioMaster, Title: "audio master", BandStart: 50, BandEnd: 53, Optional: true},
	{StepKey: "06_overlay_plan", Stage: StageOverlayPlan, Title: "overlay plan", BandStart: 53, BandEnd: 55, Optional: true},
	{StepKey: "07_render", Stage: StageRendering, Title: "render (RenderingGen → Chronon)", BandStart: 55, BandEnd: 85},
	{StepKey: "08_assemble", Stage: StageAssembling, Title: "canonical assembly", BandStart: 85, BandEnd: 92},
	{StepKey: "09_audio_mux", Stage: StageAudioFinal, Title: "final audio mux", BandStart: 92, BandEnd: 96},
	{StepKey: "10_verify", Stage: StageVerifying, Title: "ffprobe + SHA-256", BandStart: 96, BandEnd: 99},
	{StepKey: "11_publish", Stage: StageFinalizing, Title: "Drive + media registry", BandStart: 99, BandEnd: 100},
}

// StepByStage returns the canonical step spec for a stage name.
func StepByStage(stage Stage) (StepSpec, bool) {
	for _, spec := range WorkflowSteps {
		if spec.Stage == stage {
			return spec, true
		}
	}
	return StepSpec{}, false
}

// StepByKey returns the canonical step spec for a durable step key.
func StepByKey(stepKey string) (StepSpec, bool) {
	for _, spec := range WorkflowSteps {
		if spec.StepKey == stepKey {
			return spec, true
		}
	}
	return StepSpec{}, false
}

// ── Typed workflow errors (godlike/07 typed-error contract) ───────────

var (
	// ErrInvalidPayload fails a request that is out of contract
	// (missing topic, unknown media source, unknown/infrastructure
	// fields). It is TERMINAL — retrying the same payload cannot help.
	ErrInvalidPayload = errors.New("videocreate: invalid payload")

	// ErrWorkflowFailed is the umbrella for a stage failure that ends
	// the workflow. The stage's own error is wrapped underneath.
	ErrWorkflowFailed = errors.New("videocreate: workflow failed")

	// ErrChildHandlerUnavailable means a required child capability has no
	// live consumer. The parent fails before doing expensive work rather than
	// enqueueing a child that can never run.
	ErrChildHandlerUnavailable = errors.New("videocreate: required child handler unavailable")

	// ErrFinalVideoAudioMissing is the fail-closed verification gate
	// from the runbook: an MP4 without an audio stream is NEVER a
	// success (the historical silent-mux failure mode). The job turns
	// FAILED, not DONE.
	ErrFinalVideoAudioMissing = errors.New("videocreate: FINAL_VIDEO_AUDIO_MISSING")

	// ErrVerificationFailed covers every other final-MP4 verification
	// failure (missing file, zero size, no video stream, invalid
	// codec/fps/dimensions).
	ErrVerificationFailed = errors.New("videocreate: FINAL_VIDEO_VERIFICATION_FAILED")

	// ErrPublishFailed covers the artifact-publication stage (Drive
	// upload / media identity) failing closed.
	ErrPublishFailed = errors.New("videocreate: FINAL_VIDEO_PUBLISH_FAILED")

	// ErrStateCorrupt fails resume on a durable state document that
	// cannot drive the workflow (unknown step key, wrong version).
	ErrStateCorrupt = errors.New("videocreate: workflow state corrupt")
)
