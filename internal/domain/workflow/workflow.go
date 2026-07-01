// Package workflow defines the canonical domain types for workflow
// orchestration (Fase 0 della Spina Dorsale, July 2026).
//
// The workflow package is the single source of truth for:
//   - Transition rules: what step comes after what, based on job outcome.
//   - NextStep descriptors: the job type + payload for the next unit of work.
//   - WorkflowDefinition: the immutable registry entry that maps a
//     (JobType, Outcome) pair to zero or more NextSteps.
//
// Ownership boundary:
//   - The WorkflowCoordinator (internal/application/workflow/) reads
//     JobCompletedEvents and consults a WorkflowDefinitionRegistry to
//     decide which jobs to enqueue next.
//   - Job handlers (internal/jobs/) do NOT contain next-step logic —
//     they only emit JobCompletedEvent.
//   - The registry itself is a pure-data collection of immutable
//     definitions; it does not run jobs or interact with the broker.
//
// Canonical reference: Piano d'Azione § Fase 1.
package workflow

// ── NextStep ─────────────────────────────────────────────────────────

// NextStep describes a single downstream job to be created when a
// workflow step completes with a given outcome.
//
// A nil or empty NextStep slice means the workflow terminates at this
// step (no further work).
type NextStep struct {
	// JobType is the canonical job.Type discriminator (e.g.
	// "images.generate", "voiceover.generate", "document.generate").
	JobType string `json:"job_type"`

	// Payload carries the opaque, job-type-specific payload map.
	// The WorkflowCoordinator passes this map directly to the job
	// broker's Enqueue call as the job Payload.
	//
	// Key contract: every NextStep producer MUST include at minimum
	// "correlation_id" (string) so downstream steps are traceable.
	Payload map[string]any `json:"payload"`
}

// ── JobCompletedEvent ────────────────────────────────────────────────

// JobCompletedEvent is the canonical event emitted by the job kernel
// when a job reaches a terminal state (Succeeded or Failed).
//
// The WorkflowCoordinator subscribes to this event and matches it
// against a WorkflowDefinition to decide the next step.
type JobCompletedEvent struct {
	// JobID is the unique identifier of the completed job.
	JobID string `json:"job_id"`

	// JobType is the canonical discriminator (e.g. "script.generate").
	JobType string `json:"job_type"`

	// CorrelationID links all jobs in the same workflow invocation.
	CorrelationID string `json:"correlation_id"`

	// Outcome is the terminal status: "succeeded" or "failed".
	// Use the typed constants below to avoid string-typo bugs.
	Outcome Outcome `json:"outcome"`

	// ResultRef is an optional opaque reference to the job's result
	// payload, used by downstream steps to hydrate their input.
	ResultRef string `json:"result_ref,omitempty"`
}

// Outcome is the terminal result of a job. Two values exist:
// succeeded (the unit of work completed) and failed
// (permanent failure after all retries exhausted).
type Outcome string

const (
	OutcomeSucceeded Outcome = "succeeded"
	OutcomeFailed    Outcome = "failed"
)

// ── WorkflowDefinition ───────────────────────────────────────────────

// WorkflowDefinition is an immutable registry entry that maps a
// (JobType, Outcome) pair → []NextStep.
//
// Example registry shape:
//
//	{
//	  JobType:     "script.generate",
//	  Outcome:     OutcomeSucceeded,
//	  NextSteps:   [
//	    {JobType: "images.generate",  Payload: map[string]any{"correlation_id": "..."}},
//	    {JobType: "voiceover.generate", Payload: map[string]any{"correlation_id": "..."}},
//	  ],
//	}
//
// The WorkflowCoordinator looks up the definition for (jobType, outcome)
// and enqueues each NextStep via the job broker.
type WorkflowDefinition struct {
	// JobType is the upstream job type this definition fires after.
	JobType string `json:"job_type"`

	// Outcome is the terminal status that triggers these next steps.
	Outcome Outcome `json:"outcome"`

	// NextSteps is the ordered list of downstream jobs to enqueue.
	// An empty/nil slice means the workflow terminates.
	NextSteps []NextStep `json:"next_steps,omitempty"`
}

// ── WorkflowTransition ───────────────────────────────────────────────

// WorkflowTransition is the runtime materialisation of a
// WorkflowDefinition: the coordinator matches a JobCompletedEvent
// against the registry, resolves the matching definition, and
// emits a WorkflowTransition.
//
// This struct is the "execution plan" — the coordinator creates one
// per completed job, then enqueues each NextStep.
type WorkflowTransition struct {
	// WorkflowID is the correlation_id from the triggering event.
	WorkflowID string `json:"workflow_id"`

	// FromStep is the job type that just completed.
	FromStep string `json:"from_step"`

	// TriggeredBy is the job ID of the completed job.
	TriggeredBy string `json:"triggered_by"`

	// ToSteps is the resolved list of next steps to enqueue.
	// May be empty (terminal workflow).
	ToSteps []NextStep `json:"to_steps,omitempty"`
}
