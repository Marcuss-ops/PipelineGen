// Package scripts — root facade (PR-G BACKFILL, July 2026).
//
// The root package is the canonical 1-facade entry point for the
// WAVE-21 scripts capability split. Production types previously
// defined here have been migrated to the 6 capability-bounded
// subpackages (curation, generation, persistence, postprocess,
// source, usecases) per architecture/ownership.generated.yaml
// §application_scripts.
//
// This file re-exports the symbols that external callers
// (composition root, API handlers) reference directly from the
// root package. During CUTOVER, callers will migrate to importing
// the individual subpackages.
//
// See architecture/catalog.yaml WAVE-21-PR-G-SCRIPTS-SUBPKG
// for the migration plan.
package scripts

import (
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/curation"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/generation"
)

// Re-exports from generation (canonical owner of job registration + workflow state).
var (
	// JobGenerate is the canonical job type string for script generation.
	JobGenerate = generation.JobGenerate
)

type (
	// JobGenerateHandlerFunc re-exports the job handler func type alias.
	JobGenerateHandlerFunc = generation.JobGenerateHandlerFunc

	// WorkflowState is the canonical 6-state machine for script.generate workflows.
	WorkflowState = generation.WorkflowState

	// IllegalWorkflowTransitionError re-exports the typed error-data envelope.
	IllegalWorkflowTransitionError = generation.IllegalWorkflowTransitionError
)

// CanonicalWorkflowStateValues re-exports from generation.
var CanonicalWorkflowStateValues = generation.CanonicalWorkflowStateValues

// State constants re-exported from generation.
const (
	StateScriptReady        = generation.StateScriptReady
	StateImagesPending      = generation.StateImagesPending
	StateImagesGenerated    = generation.StateImagesGenerated
	StateDocumentCreated    = generation.StateDocumentCreated
	StateWorkflowFailed     = generation.StateWorkflowFailed
	StateWorkflowDeadLettered = generation.StateWorkflowDeadLettered
)

// MustRegister is the canonical job-definition registration entry point.
var MustRegister = generation.MustRegister

// ErrIllegalWorkflowStateTransition re-exported from generation.
var ErrIllegalWorkflowStateTransition = generation.ErrIllegalWorkflowStateTransition

// SceneImageJobEmitter re-exported from curation.
type (
	SceneImageJobEmitter = curation.SceneImageJobEmitter
	Emitter              = curation.Emitter
	DispatcherShim       = curation.DispatcherShim
	EmitSceneImageCommand = curation.EmitSceneImageCommand
	SceneImageJobPayload  = curation.SceneImageJobPayload
)

// Re-exports from curation.
var (
	ErrEmitMissingParentJobID         = curation.ErrEmitMissingParentJobID
	ErrEmitMissingPrompt              = curation.ErrEmitMissingPrompt
	NewEmitter                        = curation.NewEmitter
	NewIllegalWorkflowTransitionError = generation.NewIllegalWorkflowTransitionError
)
