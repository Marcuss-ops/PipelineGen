// Package scripts — scene_orchestrator.go
//
// Azione 10 (P1, CUTOVER-COMPLETE-WITH-ARTIFACTS, July 2026):
// typed 1-method port for emitting child image.generate jobs from
// script.generate workflows.
//
// The SceneImageJobEmitter interface is the canonical seam for
// emitting scene-image child jobs. The concrete Emitter delegates
// to Dispatcher.Enqueue (AGENTS.md Pattern 9) — the typed entry
// point that routes through the CompiledJobRegistry for payload
// encoding + queue/timeout/retry metadata.
//
// godlike/06 SSOT: this file is the SINGLE canonical owner of the
// "scene-image child job emitted from script.generate" fact.
//
// godlike/07 typed-error contract: EmitSceneImageJob returns
// (jobID, error) typed; emit failures are reachable via errors.Is
// against the dispatched sentinels (ErrEnqueuerNotWired, etc.).
// The compile-time assertion var _ SceneImageJobEmitter = (*Emitter)(nil)
// pins the contract so any future drift is a build failure.
package curation

import (
	"context"
	"errors"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/workflow"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── SceneImageJobEmitter port (Pattern 0) ──────────────────────────────

// SceneImageJobEmitter is the typed port for emitting a single
// images.generate child job from a script.generate workflow.
// Implementations delegate to the canonical Dispatcher.Enqueue
// (AGENTS.md Pattern 9) for typed-payload routing through the
// CompiledJobRegistry.
//
// The port is intentionally narrow — one method, one responsibility.
// Future extensions (batch emit, cancel child, list children) should
// be added as separate ports per godlike/06 one-owner-per-fact.
type SceneImageJobEmitter interface {
	// EmitSceneImageJob emits a single images.generate child job
	// for the given scene image command. Returns the canonical
	// job ID on success, or a typed error on failure.
	//
	// The returned jobID is the canonical job-broker identity
	// (job.ID) that callers can use for polling, cancellation,
	// and correlation.
	EmitSceneImageJob(ctx context.Context, cmd EmitSceneImageCommand) (string, error)
}

// ── EmitSceneImageCommand ──────────────────────────────────────────────

// EmitSceneImageCommand is the typed DTO for emitting a scene image
// job. Carries the script.generate parent context so the child job
// can be correlated back to its parent workflow.
type EmitSceneImageCommand struct {
	// ParentJobID is the canonical job-broker ID of the parent
	// script.generate job. Required: empty → ErrEmitMissingParentJobID.
	ParentJobID string

	// ScriptID is the canonical script identity (script.id row).
	// Carried as metadata on the child job for correlation.
	ScriptID string

	// SceneIndex is the 0-based index of the scene within the
	// parent script. Carried as metadata on the child job so
	// the scene-image handler can route the result back to the
	// correct scene slot.
	SceneIndex int

	// Prompt is the image generation prompt text. Required:
	// empty → ErrEmitMissingPrompt.
	Prompt string

	// Style is the optional generation style preset name.
	// Carried as metadata; the handler resolves it against
	// the style registry.
	Style string

	// Width is the output image width in pixels. Zero means
	// the handler's default (typically 1024).
	Width int

	// Height is the output image height in pixels. Zero means
	// the handler's default (typically 1024).
	Height int

	// CorrelationID is the optional correlation ID for
	// distributed tracing. If empty, the Emitter derives one
	// from ParentJobID + SceneIndex.
	CorrelationID string
}

// ── Sentinels ──────────────────────────────────────────────────────────

// ErrEmitMissingParentJobID is returned when EmitSceneImageCommand.ParentJobID
// is empty. The parent job identity is required for correlation.
var ErrEmitMissingParentJobID = errors.New("scene orchestrator: ParentJobID is required")

// ErrEmitMissingPrompt is returned when EmitSceneImageCommand.Prompt
// is empty. The prompt is the core input for image generation.
var ErrEmitMissingPrompt = errors.New("scene orchestrator: Prompt is required")

// Compile-time assertion: *Emitter satisfies SceneImageJobEmitter.
// Pins the contract at build time — any future drift in the EmitSceneImageJob
// signature is a build failure, not a runtime panic (godlike/07).
var _ SceneImageJobEmitter = (*Emitter)(nil)

// ── Emitter (concrete implementation) ──────────────────────────────────

// Emitter is the concrete SceneImageJobEmitter that delegates to
// the canonical Dispatcher.Enqueue (AGENTS.md Pattern 9) for
// typed-payload routing through the CompiledJobRegistry.
type Emitter struct {
	dispatcher DispatcherShim
}

// DispatcherShim is the narrow interface Emitter needs from the
// application-layer jobs.Dispatcher. Exposing only Enqueue keeps
// the Emitter decoupled from handler registration, freezing, and
// other Dispatcher concerns.
//
// The application-layer *jobs.Dispatcher satisfies this interface
// structurally — no adapter wrapper needed. The compile-time
// assertion below pins the contract.
type DispatcherShim interface {
	Enqueue(ctx context.Context, jobType string, payload any) (*job.Job, error)
}

// NewEmitter constructs a SceneImageJobEmitter backed by the
// canonical Dispatcher. A nil dispatcher is retained as an invalid instance
// so the first emit returns a typed error instead of silently dropping a job.
func NewEmitter(dispatcher DispatcherShim) *Emitter {
	return &Emitter{dispatcher: dispatcher}
}

// EmitSceneImageJob validates the command and delegates to
// Dispatcher.Enqueue for typed-payload routing. The emitted
// job type is canonical domain/images.JobGenerate ("images.generate").
//
// Returns the canonical job-broker job ID on success, or a typed
// error on failure. Emit failures are reachable via errors.Is
// against the dispatched sentinels (ErrEnqueuerNotWired, etc.)
// at the caller's seam.
//
// godlike/07 no-fake-availability: nil receiver returns a typed
// error (never panics, never silently succeeds).
func (e *Emitter) EmitSceneImageJob(ctx context.Context, cmd EmitSceneImageCommand) (string, error) {
	// Nil-receiver guard (godlike/07 fail-closed).
	if e == nil {
		return "", fmt.Errorf("scene orchestrator: Emitter is nil (composition bug — must wire via NewEmitter)")
	}
	if e.dispatcher == nil {
		return "", fmt.Errorf("scene orchestrator: dispatcher is not configured")
	}

	// Validate required fields.
	if cmd.ParentJobID == "" {
		return "", ErrEmitMissingParentJobID
	}
	if cmd.Prompt == "" {
		return "", ErrEmitMissingPrompt
	}

	// Derive correlation ID if not provided.
	correlationID := cmd.CorrelationID
	if correlationID == "" {
		correlationID = fmt.Sprintf("%s:scene:%d", cmd.ParentJobID, cmd.SceneIndex)
	}

	// Build the typed payload envelope. The payload shape mirrors
	// the existing images.generate job payload convention.
	payload := SceneImageJobPayload{
		ParentJobID:   cmd.ParentJobID,
		ScriptID:      cmd.ScriptID,
		SceneIndex:    cmd.SceneIndex,
		Prompt:        cmd.Prompt,
		Style:         cmd.Style,
		Width:         cmd.Width,
		Height:        cmd.Height,
		CorrelationID: correlationID,
	}

	// Delegate to the canonical Dispatcher.Enqueue.
	// The Dispatcher routes through CompiledJobRegistry →
	// PayloadCodec.EncodePayload → EnqueuePort.Enqueue.
	j, err := e.dispatcher.Enqueue(ctx, images.JobGenerate, payload)
	if err != nil {
		return "", fmt.Errorf("scene orchestrator: emit images.generate for scene %d (parent %s): %w",
			cmd.SceneIndex, cmd.ParentJobID, err)
	}

	return j.ID, nil
}

// SceneImageJobPayload is the typed payload envelope for an
// images.generate child job emitted from a script.generate workflow.
// Marshaled by the Dispatcher's PayloadCodec at Enqueue time;
// unmarshaled by the image generation handler at execution time.
type SceneImageJobPayload struct {
	ParentJobID   string `json:"parent_job_id"`
	ScriptID      string `json:"script_id"`
	SceneIndex    int    `json:"scene_index"`
	Prompt        string `json:"prompt"`
	Style         string `json:"style,omitempty"`
	Width         int    `json:"width,omitempty"`
	Height        int    `json:"height,omitempty"`
	CorrelationID string `json:"correlation_id,omitempty"`
}
