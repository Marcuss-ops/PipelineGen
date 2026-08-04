// Package jobs — result_dto.go (FASE 2, July 2026).
//
// Typed DTOs that replace the map[string]any usage in the parent aggregator
// and the fan-out handler's result serialization. The types carry JSON tags
// that match the existing wire shape exactly so the broker boundary
// (json.RawMessage ↔ map[string]any) is unchanged — only the aggregator's
// internal logic switches from map access to typed field access.
//
// VoiceoverParentResult is the parent job's result shape (written by
// toFanoutResultMap and read by the aggregator's aggregateOne).
//
// VoiceoverChildResult is the child job's result shape (written by
// GenerateItemJobHandler.HandleJob and read by the aggregator's
// P0.1 false-success gate).
//
// VoiceoverAggregateResult is the output of the aggregation loop:
// what the aggregator has computed and is about to persist via
// FinalizeAggregateParent.
package jobs

import (
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// VoiceoverParentResult is the typed parent job result. Field names
// and JSON tags match toFanoutResultMap's map shape exactly so
// json.Unmarshal(job.Result) into this struct is a drop-in replacement
// for map[string]any access.
type VoiceoverParentResult struct {
	OK                 bool                         `json:"ok"`
	ParentJobID        string                       `json:"parent_job_id"`
	RequestID          string                       `json:"request_id"`
	TotalOutputs       int                          `json:"total_outputs"`
	ExpectedChildren   int                          `json:"expected_children"`
	EnqueuedCount      int                          `json:"enqueued_count"`
	FailedEnqueueCount int                          `json:"failed_enqueue_count"`
	ChildJobIDs        []string                     `json:"child_job_ids"`
	PerLanguage        []string                     `json:"per_language"`
	StageProgress      map[string]job.StageProgress `json:"stage_progress,omitempty"`
	ParentState        string                       `json:"parent_state"`
	// AggregatorVersion is the StateMachine version at the last
	// aggregator tick (set by aggregateOne, read by finalizeParent
	// for version-based CAS). Zero when the fan-out handler wrote
	// the result (the handler doesn't own the state machine).
	AggregatorVersion int `json:"_aggregator_version,omitempty"`
}

// ParentState returns the parent_state field as the typed voiceover.ParentState.
func (r *VoiceoverParentResult) ParentStateTyped() voiceover.ParentState {
	return voiceover.ParentState(r.ParentState)
}

// IsAwaitingAggregation reports whether the parent is in a non-terminal
// application-level state that the aggregator should process.
func (r *VoiceoverParentResult) IsAwaitingAggregation() bool {
	switch voiceover.ParentState(r.ParentState) {
	case voiceover.ParentWaitingChildren, voiceover.ParentPartialSuccess:
		return true
	}
	return false
}

// VoiceoverChildResult is the typed child job result the aggregator
// reads to apply the P0.1 false-success gate.
//
// OK is *bool (not bool) to distinguish "absent" (nil, fall back to
// broker status) from "present and false" (P0.1 gate override to FAILED).
// The legacy result shape had no "ok" field at all; the pre-P0.1 map
// access `if hasOK && !ok` required the field to be present AND false.
// A plain `bool` would treat absent-as-false and break the legacy
// edge case (TestParentHandlesChildResultWithoutOKField).
type VoiceoverChildResult struct {
	OK          *bool  `json:"ok,omitempty"`
	Status      string `json:"status"`
	Language    string `json:"language"`
	JobID       string `json:"job_id"`
	ParentJobID string `json:"parent_job_id"`
	RequestID   string `json:"request_id"`
	Error       string `json:"error,omitempty"`
}

// VoiceoverChildPayload is the typed child job payload the aggregator
// reads to extract the Required flag (FASE 2: wire the fanout to set
// Required explicitly).
type VoiceoverChildPayload struct {
	Required bool `json:"required"`
}

// VoiceoverAggregateResult is the typed output of the aggregation loop.
// Built from the domain StateMachine (Transition + Compute) and passed
// to finalizeParent for FinalizeAggregateParent persistence.
type VoiceoverAggregateResult struct {
	// ParentState is the voiceover-level enum (succeeded / partial_success / failed).
	ParentState voiceover.ParentState

	// TotalChildren is the number of children the aggregator observed.
	TotalChildren int

	// SucceededCount is the number of children that reached SUCCEEDED
	// (by the domain StateMachine classification, post P0.1 gate).
	SucceededCount int

	// FailedCount is the number of children that reached FAILED
	// (or were overridden by the P0.1 gate).
	FailedCount int

	// RequiredFailedCount is the number of REQUIRED children that
	// failed. When > 0, the domain StateMachine short-circuits to
	// FailedTerminal in Transition() rule ①.
	RequiredFailedCount int

	// StateMachineVersion is the domain StateMachine.Version() after
	// all child events + Compute(). Used as the expectedVersion for
	// version-based CAS in FinalizeAggregateParent.
	StateMachineVersion int

	// ChildIDs is the list of child job IDs the aggregator observed.
	ChildIDs      []string
	StageProgress map[string]job.StageProgress
}
