package videocreate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── Handler: the video.create entry point ─────────────────────────────
//
// NewHandler returns the canonical kernel handler shape
// (job.Handler: (ctx, *job.Job, *job.JobExecutionTools) → (job.Result,
// error)) so it binds to the job registry with ONE RegisterHandler call
// and needs NO dedicated HTTP endpoint: the M2M surface is
// POST /api/v1/jobs with type=video.create (§2).
//
// Fail-closed contract:
//
//   - the payload decodes with DisallowUnknownFields: an
//     infrastructure field (worker_ip, renderinggen_url,
//     chronon_socket, a filesystem path) in the payload is a TERMINAL
//     ErrInvalidPayload, never a silently ignored key;
//   - resume happens against the canonical step store before any stage
//     runs;
//   - the result is emitted only after BuildVideoCreateResult's typed
//     validation passes.

// NewHandler wires the workflow's handler. Deps must be complete
// (godlike/05 fail-closed: a workflow missing its step store or job
// registry must not start).
func NewHandler(deps Deps) (job.Handler, error) {
	if err := deps.Validate(); err != nil {
		return nil, fmt.Errorf("videocreate.NewHandler: %w", err)
	}
	coordinator := &Coordinator{Store: deps.Steps}
	return job.Handler(func(ctx context.Context, j *job.Job, tools *job.JobExecutionTools) (job.Result, error) {
		return handle(ctx, j, tools, deps, coordinator)
	}), nil
}

func handle(ctx context.Context, j *job.Job, tools *job.JobExecutionTools, deps Deps, coordinator *Coordinator) (job.Result, error) {
	if j == nil {
		return nil, fmt.Errorf("%w: nil job", ErrInvalidPayload)
	}
	payload, err := decodePayload(j.Payload)
	if err != nil {
		return nil, err
	}
	if err := validateChildHandlers(payload, deps.Children); err != nil {
		return nil, err
	}
	rawPayload, _ := json.Marshal(payload)
	rows, err := deps.Steps.ListByJob(ctx, j.ID)
	if err != nil {
		return nil, fmt.Errorf("%w: load workflow state: %v", ErrStateCorrupt, err)
	}
	state, err := StateFromSteps(rows)
	if err != nil {
		return nil, err
	}
	run := newRun(j, payload, deps, state, tools)
	run.RequestHash = digest.SHA256Bytes(rawPayload)
	if err := os.MkdirAll(run.Workspace, 0o755); err != nil {
		return nil, fmt.Errorf("%w: workspace: %v", ErrWorkflowFailed, err)
	}
	if err := RehydrateFacts(ctx, run); err != nil {
		return nil, err
	}
	if err := coordinator.Run(ctx, run); err != nil {
		return nil, err
	}
	result, err := BuildVideoCreateResult(run)
	if err != nil {
		return nil, err
	}
	return handlerResult(run, result)
}

// validateChildHandlers makes availability fail closed before the parent
// starts media search, downloads or rendering. A registered job policy is not
// evidence that its child capability has a live consumer.
func validateChildHandlers(payload appjobs.VideoCreatePayload, children ChildJobs) error {
	required := []string{appjobs.TypeScriptGenerate, job.TypeClipRender, appjobs.TypeAssemblyPrepare, appjobs.TypeAssemblyFinalize}
	for _, source := range payload.MediaSources {
		_, jobType := acquireFamily(source)
		required = append(required, jobType)
	}
	if payload.Voiceover {
		required = append(required, job.TypeVoiceoverGenerate)
	}
	seen := make(map[string]struct{}, len(required))
	for _, jobType := range required {
		if _, ok := seen[jobType]; ok {
			continue
		}
		seen[jobType] = struct{}{}
		if !children.HasHandler(jobType) {
			return fmt.Errorf("%w: %s", ErrChildHandlerUnavailable, jobType)
		}
	}
	return nil
}

// decodePayload is the strict §3 boundary: unknown fields fail closed
// (infrastructure facts must never ride the payload), and the typed
// Validate covers the closed vocabularies.
func decodePayload(raw json.RawMessage) (appjobs.VideoCreatePayload, error) {
	var payload appjobs.VideoCreatePayload
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return payload, fmt.Errorf("%w: %v", ErrInvalidPayload, err)
	}
	if err := payload.Validate(); err != nil {
		return payload, fmt.Errorf("%w: %v", ErrInvalidPayload, err)
	}
	return payload, nil
}
