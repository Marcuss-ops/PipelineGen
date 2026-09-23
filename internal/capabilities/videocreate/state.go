package videocreate

import (
	"encoding/json"
	"fmt"
	"strings"

	steps "github.com/Marcuss-ops/PipelineGen/internal/capabilities/execution/steps"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
)

// Location value-object aliases (one owner: internal/capabilities/jobs).
// Identity structs embed these so a content address and a location never
// share one struct declaration (media-identity gate).
type (
	DriveRef = appjobs.DriveRef
	LocalRef = appjobs.LocalRef
)

// ── Durable workflow state (the §7 state machine) ─────────────────────
//
// The AUTHORITY is the canonical resumable step store
// (internal/capabilities/execution/steps: one row per
// (job, step_key, input_fingerprint), terminal-completion immutable,
// FirstNonCompleted drives resume). WorkflowState is the typed
// projection of those rows in exactly the shape the workflow needs:
//
//	{ "workflow_version": 1, "current_stage": "RENDERING",
//	  "stages": { "07_render": { "status": "RUNNING",
//	                             "jobs": ["job_render_1"] }, ... } }
//
// There is deliberately NO second persistence for this document: a
// state map that only lives in memory (or in a second table) is how a
// restart silently re-runs a completed stage and duplicates work.

// StageStatus is the projected status of one workflow step.
type StageStatus string

const (
	StageWaiting   StageStatus = "WAITING"
	StageRunning   StageStatus = "RUNNING"
	StageSucceeded StageStatus = "SUCCEEDED"
	StageFailed    StageStatus = "FAILED"
	// StageSkipped marks an optional step the payload rendered
	// unnecessary (e.g. voiceover=false). It is a durable completion
	// like SUCCEEDED — resume never re-decides it.
	StageSkipped StageStatus = "SKIPPED"
)

// StageArtifactRef is the durable identity of one stage output: no
// local paths, only facts that survive a restart and identify the
// bytes (media registry identity + content hash + Drive identity).
type StageArtifactRef struct {
	AssetID    string `json:"asset_id"`
	Kind       string `json:"kind,omitempty"`
	ContentSHA string `json:"content_sha256,omitempty"`
	// DriveRef carries the Drive location (wire key drive_file_id).
	DriveRef
	DurationMS int64  `json:"duration_ms,omitempty"`
	MediaType  string `json:"media_type,omitempty"`
	// Source/SourceURL name the acquisition family ("youtube" vs
	// "stock") and the pre-acquisition identity. Durable identity facts:
	// they let resume rebuild the acquisition ledger per family.
	Source    string `json:"source,omitempty"`
	SourceURL string `json:"source_url,omitempty"`
}

// StageOutput is what a completed step records as its durable result
// (steps.Store result_json). It is the inter-stage contract: child job
// ledger + artifact identities + the stage's typed facts.
type StageOutput struct {
	// ChildJobs is the durable broker ledger of the children this
	// step fanned out to (replay audit: "did we already run this?").
	ChildJobs []string `json:"child_jobs,omitempty"`
	// Artifacts are the durable identities the step produced. For
	// "03_media_acquire" the pairing contract holds: ChildJobs[i]
	// produced Artifacts[i] (that is what splits youtube vs stock).
	Artifacts []StageArtifactRef `json:"artifacts,omitempty"`
	// Selection is the "02_media_search" outcome: the ranked
	// candidates assigned to scenes, durable so acquisition can resume
	// from exactly the selection the search step made.
	Selection []MediaCandidate `json:"selection,omitempty"`
	// ScriptAssetID is set by 01_script (the script artifact identity).
	ScriptAssetID string `json:"script_asset_id,omitempty"`
	// TextSegments are the overlay text segments of the script (the
	// §14 render plan input).
	TextSegments []string `json:"text_segments,omitempty"`
	// AudioPlan is the compiled audio plan the children produced
	// (§13: the workflow reuses it verbatim, never re-derives it).
	AudioPlan json.RawMessage `json:"audio_plan,omitempty"`
	// Voiceovers are the voiceover materializations the stage produced
	// (04's per-item results, or the §9 per-scene voiceovers of 01) —
	// the durable input set 05 mixes from.
	Voiceovers []VoiceoverAssetRef `json:"voiceovers,omitempty"`
	// AudioMaster is the certified canonical final-audio master
	// (05_audio_master output, or the §9 reused master).
	AudioMaster *MasteredAudio `json:"audio_master,omitempty"`
	// OverlayPlan is the 06_overlay_plan record (render-plan input +
	// the §19-B thumbnail frame identities).
	OverlayPlan *OverlayPlanFact `json:"overlay_plan,omitempty"`
	// Assembled is the 08_assemble canonical assembly identity.
	Assembled *AssembleResult `json:"assembled,omitempty"`
	// Verified / Published are the 10/11 durable final facts.
	Verified  *VerifiedFacts     `json:"verified,omitempty"`
	Published *PublishedArtifact `json:"published,omitempty"`
	// Scenes is the scene plan produced by 01_script (scene ids in
	// timeline order). Every later per-scene fan-out derives from it.
	Scenes []string `json:"scenes,omitempty"`
	// FinalAudio records the canonical final-audio master facts
	// (05_audio_master): asset id + certified copy-eligibility facts.
	FinalAudio *StageArtifactRef `json:"final_audio,omitempty"`
	// Skipped is true for an optional step the payload made
	// unnecessary. The distinction from "ran and produced nothing" is
	// load-bearing for resume and for the result's completed_stages.
	Skipped bool `json:"skipped,omitempty"`
}

// StageRecord is one step's projection row.
type StageRecord struct {
	Status  StageStatus `json:"status"`
	StepKey string      `json:"step_key"`
	Jobs    []string    `json:"jobs,omitempty"`
	Error   string      `json:"error,omitempty"`
	Output  StageOutput `json:"output,omitempty"`
	_       struct{}    `json:"-"`
}

// WorkflowState is the §7 durable state document, projected from the
// canonical step rows. current_stage is derived (first non-SUCCEEDED
// step), never stored twice.
type WorkflowState struct {
	WorkflowVersion int                     `json:"workflow_version"`
	CurrentStage    string                  `json:"current_stage"`
	Stages          map[string]*StageRecord `json:"stages"`
}

// NewWorkflowState returns the empty (all WAITING) projection of the
// canonical ladder.
func NewWorkflowState() WorkflowState {
	st := WorkflowState{
		WorkflowVersion: WorkflowVersion,
		Stages:          make(map[string]*StageRecord, len(WorkflowSteps)),
	}
	for _, spec := range WorkflowSteps {
		st.Stages[spec.StepKey] = &StageRecord{Status: StageWaiting, StepKey: spec.StepKey}
	}
	st.CurrentStage = string(WorkflowSteps[0].Stage)
	return st
}

// StateFromSteps projects the canonical step rows into the workflow
// state document. Unknown step keys fail closed (ErrStateCorrupt): a
// row the ladder does not describe is a version drift, and acting on a
// half-understood history is exactly the silent-duplication failure
// mode the durable store exists to prevent.
func StateFromSteps(rows []steps.StepState) (WorkflowState, error) {
	st := NewWorkflowState()
	for _, row := range rows {
		rec, ok := st.Stages[row.StepKey]
		if !ok {
			return WorkflowState{}, fmt.Errorf("%w: unknown step key %q", ErrStateCorrupt, row.StepKey)
		}
		var out StageOutput
		if len(row.Result) > 0 {
			if err := json.Unmarshal(row.Result, &out); err != nil {
				return WorkflowState{}, fmt.Errorf("%w: step %s result: %v", ErrStateCorrupt, row.StepKey, err)
			}
		}
		rec.Output = out
		rec.Jobs = out.ChildJobs
		rec.Error = row.LastError
		switch row.Status {
		case steps.StatusCompleted:
			if out.Skipped {
				rec.Status = StageSkipped
			} else {
				rec.Status = StageSucceeded
			}
		case steps.StatusFailed:
			rec.Status = StageFailed
		case steps.StatusRunning, steps.StatusPending:
			rec.Status = StageRunning
		default:
			return WorkflowState{}, fmt.Errorf("%w: step %s has unknown status %q", ErrStateCorrupt, row.StepKey, row.Status)
		}
	}
	st.Refresh()
	return st, nil
}

// Refresh recomputes current_stage from the projection (first step that
// is not terminal-succeeded/skipped).
func (s *WorkflowState) Refresh() {
	s.WorkflowVersion = WorkflowVersion
	for _, spec := range WorkflowSteps {
		rec := s.Stages[spec.StepKey]
		if rec == nil || (rec.Status != StageSucceeded && rec.Status != StageSkipped) {
			s.CurrentStage = string(spec.Stage)
			return
		}
	}
	s.CurrentStage = string(StageFinalizing)
}

// Completed reports whether the step is durably done (SUCCEEDED or
// SKIPPED) — the ONLY states resume may pass over.
func (s *WorkflowState) Completed(stepKey string) bool {
	rec := s.Stages[stepKey]
	return rec != nil && (rec.Status == StageSucceeded || rec.Status == StageSkipped)
}

// CompletedStepKeys returns the durably completed step keys in ladder
// order (the result's completed_stages).
func (s *WorkflowState) CompletedStepKeys() []string {
	out := make([]string, 0, len(WorkflowSteps))
	for _, spec := range WorkflowSteps {
		if s.Completed(spec.StepKey) {
			out = append(out, spec.StepKey)
		}
	}
	return out
}

// ChildLedger aggregates every child job id the workflow fanned out,
// in ladder order. It is the durable answer to "what did this run
// actually execute?" and the input to the VideoCreateResult children.
func (s *WorkflowState) ChildLedger() (scripts string, youtube, stock []string, voiceover string, render, assembly []string) {
	get := func(key string) []string {
		rec := s.Stages[key]
		if rec == nil {
			return nil
		}
		return rec.Jobs
	}
	if jobs := get("01_script"); len(jobs) > 0 {
		scripts = jobs[0]
	}
	// "03_media_acquire" splits per family via the StageOutput pairing
	// contract (ChildJobs[i] produced Artifacts[i], whose Source names
	// the family).
	if rec := s.Stages["03_media_acquire"]; rec != nil {
		for i, id := range rec.Jobs {
			source := ""
			if i < len(rec.Output.Artifacts) {
				source = rec.Output.Artifacts[i].Source
			}
			if source == "youtube" {
				youtube = append(youtube, id)
			} else {
				stock = append(stock, id)
			}
		}
	}
	if jobs := get("04_voiceover"); len(jobs) > 0 {
		voiceover = jobs[0]
	}
	render = get("07_render")
	// "08_assemble" ChildJobs contract: EMPTY — assembly runs on the
	// VeloxEditing media plane (assemble_copy), not on child jobs.
	assembly = get("08_assemble")
	return scripts, youtube, stock, voiceover, render, assembly
}

// Validate fails closed on a state document that cannot drive resume.
func (s WorkflowState) Validate() error {
	if s.WorkflowVersion != WorkflowVersion {
		return fmt.Errorf("%w: workflow_version=%d, want %d", ErrStateCorrupt, s.WorkflowVersion, WorkflowVersion)
	}
	for key := range s.Stages {
		if _, ok := StepByKey(key); !ok {
			return fmt.Errorf("%w: unknown stage key %q", ErrStateCorrupt, key)
		}
	}
	return nil
}

// Encode serialises the state document (diagnostics / remote
// projection). The document is derived, so this is read-only surface.
func (s WorkflowState) Encode() (json.RawMessage, error) {
	s.Refresh()
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("videocreate: encode workflow state: %w", err)
	}
	return raw, nil
}

// StageRecordFor is the small accessor the coordinator uses after each
// transition (nil-safe).
func (s *WorkflowState) StageRecordFor(stepKey string) *StageRecord {
	if s == nil {
		return nil
	}
	return s.Stages[stepKey]
}

// Describe renders a one-line operator summary ("RENDERING
// (07_render, RUNNING)") for logs and progress events.
func (s *WorkflowState) Describe() string {
	if s == nil {
		return "videocreate: (no state)"
	}
	var b strings.Builder
	b.WriteString(string(s.CurrentStage))
	for _, spec := range WorkflowSteps {
		rec := s.Stages[spec.StepKey]
		if rec == nil || rec.Status == StageWaiting {
			continue
		}
		b.WriteString(" ")
		b.WriteString(spec.StepKey)
		b.WriteString("=")
		b.WriteString(string(rec.Status))
	}
	return b.String()
}
