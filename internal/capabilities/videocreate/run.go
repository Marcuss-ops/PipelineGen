package videocreate

import (
	"context"
	"encoding/json"
	"path/filepath"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── Run: one workflow execution ───────────────────────────────────────
//
// A Run carries three strictly separated data classes:
//
//  1. the typed REQUEST (VideoCreatePayload) — immutable, owned by the
//     caller;
//  2. the durable STATE (WorkflowState) — the resumable step-store
//     projection, the ONLY authority on what is already done;
//  3. the transient FACTS (Facts) — execution details (local
//     materializations of children outputs) that are REHYDRATED from
//     the child-job ledger after a restart and never persisted as the
//     inter-stage contract (§11: stages exchange durable identities).
//
// The split is what makes "server restart at render 40%" resume at
// render instead of script: state decides WHERE to resume, the child
// ledger restores WHAT the finished stages produced.

// AcquiredClip is one acquired media clip: the durable identity plus
// its transient local materialization for the render step.
type AcquiredClip struct {
	Ref        StageArtifactRef
	LocalPath  string
	Source     string
	SceneIndex int
}

// VoiceoverFact is the voiceover stage output facts.
type VoiceoverFact struct {
	Ref        StageArtifactRef
	LocalPath  string
	SampleRate int
	Channels   int
	Codec      string
}

// OverlayPlanFact is the overlay plan record (§19-adjacent: overlays
// enter the RENDER plan; the cover/thumbnail is NOT a render-lane phase
// and stays owned by the caller side).
type OverlayPlanFact struct {
	Style         string   `json:"style,omitempty"`
	TextOverlays  int      `json:"text_overlays"`
	ImageOverlays int      `json:"image_overlays"`
	FrameAssetIDs []string `json:"frame_asset_ids,omitempty"`
}

// RenderedClip is one rendered scene segment with its copy
// certification facts for the canonical assembler.
type RenderedClip struct {
	Segment   AssembleSegment
	LocalPath string
	SceneID   string
}

// Facts is the transient execution state, rehydrated from the child
// ledger + step outputs after a restart (recovery.go).
type Facts struct {
	ScriptAssetID string
	Scenes        []string
	TextSegments  []string
	AudioPlanJSON json.RawMessage
	Candidates    []MediaCandidate
	Acquired      []AcquiredClip
	Voiceover     *VoiceoverFact
	FinalAudio    *MasteredAudio
	OverlayPlan   *OverlayPlanFact
	Rendered      []RenderedClip
	Assembled     *AssembleResult
	Muxed         *MuxedVideo
	Verified      *VerifiedFacts
	Published     *PublishedArtifact
}

// Run is one video.create execution.
type Run struct {
	Job     *job.Job
	Request appjobs.VideoCreatePayload
	RootKey string
	// RequestHash fingerprints the decoded payload (the step-store
	// InputFingerprint salt: same job+payload → same rows, replay
	// cannot fork the durable history).
	RequestHash string
	// Workspace is the persistent per-job workspace root
	// (<Deps.Workspace>/<jobID>).
	Workspace string
	State     WorkflowState
	Facts     Facts
	Deps      Deps
	// Progress reports (percent, message) to the broker's progress
	// channel (nil-safe; wired from JobExecutionTools.Progress).
	Progress func(int, string)
	// Events reports typed workflow events to the broker timeline
	// (nil-safe; wired from JobExecutionTools.Event).
	Events func(eventType, message string, data map[string]any)
}

// newRun assembles one run from the handler inputs. tools may be nil
// (tests run without broker callbacks).
func newRun(j *job.Job, req appjobs.VideoCreatePayload, deps Deps, state WorkflowState, tools *job.JobExecutionTools) *Run {
	run := &Run{
		Job:       j,
		Request:   req,
		RootKey:   RootKey(j.IdempotencyKey, j.ID),
		Workspace: filepath.Join(deps.Workspace, j.ID),
		State:     state,
		Facts:     Facts{},
		Deps:      deps,
	}
	if tools != nil {
		run.Progress = tools.Progress
		run.Events = tools.Event
	}
	return run
}

// workPath returns a stable per-job workspace path (persistent across
// restarts — see Deps.Workspace).
func (r *Run) workPath(name string) string {
	return filepath.Join(r.Workspace, name)
}

// ChildIdentity returns the parent link pair carried by every child
// (§8: durable parent/child correlation without process memory).
func (r *Run) ChildIdentity() (parentJobID, parentRunID string) {
	return r.Job.ID, r.Job.CorrelationID
}

// enqueue fans out one child with its derived idempotency key and
// scoped correlation id, and records the child id in the stage output
// ledger.
func (r *Run) enqueue(ctx context.Context, spec StepSpec, childKey, jobType string, payload any) (string, error) {
	parentJobID, parentRunID := r.ChildIdentity()
	raw, err := marshalChild(payload, parentJobID, parentRunID)
	if err != nil {
		return "", err
	}
	childID, err := r.Deps.Children.EnqueueChild(ctx, ChildJobRequest{
		JobType:        jobType,
		IdempotencyKey: childKey,
		CorrelationID:  ChildCorrelationID(r.Job.CorrelationID, jobType, childKey),
		Payload:        raw,
		Project:        r.Job.Project,
		VideoName:      r.Job.VideoName,
	})
	if err != nil {
		return "", err
	}
	r.event("child_enqueued", spec, map[string]any{
		"child_job_id":    childID,
		"job_type":        jobType,
		"idempotency_key": childKey,
	})
	return childID, nil
}

// await waits for one child and returns it (must be terminal).
func (r *Run) await(ctx context.Context, spec StepSpec, childID string) (*job.Job, error) {
	child, err := r.Deps.Children.WaitTerminal(ctx, childID)
	if err != nil {
		return nil, err
	}
	r.event("child_terminal", spec, map[string]any{
		"child_job_id": childID,
		"status":       string(child.Status),
	})
	return child, nil
}

func (r *Run) event(kind string, spec StepSpec, data map[string]any) {
	if r.Events == nil {
		return
	}
	if data == nil {
		data = map[string]any{}
	}
	data["step_key"] = spec.StepKey
	data["stage"] = string(spec.Stage)
	r.Events("video.create."+kind, kind, data)
}

// childResult unmarshals a terminal child's result payload into the
// stage-side projection (children.go contract).
func childResult(child *job.Job, out any) error {
	if len(child.Result) == 0 {
		return nil
	}
	if err := json.Unmarshal(child.Result, out); err != nil {
		return err
	}
	return nil
}
