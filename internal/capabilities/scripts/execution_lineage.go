package scriptgeneration

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func executionStepID(exec ExecutionContext, name string) string {
	attempt := exec.Attempt
	if attempt <= 0 {
		attempt = 1
	}
	return fmt.Sprintf("%s:script:attempt-%d:%s", exec.JobID, attempt, strings.ToLower(strings.ReplaceAll(name, " ", "_")))
}

func (r *Runner) setRecorder(recorder ExecutionRecorder) {
	if recorder == nil {
		r.recorder = noopExecutionRecorder{}
		return
	}
	r.recorder = recorder
}

func (r *Runner) startExecutionStep(ctx context.Context, exec ExecutionContext, name, stepType string) (ExecutionStep, error) {
	step := ExecutionStep{StepID: executionStepID(exec, name), Name: name, Type: stepType, Status: "RUNNING", StartedAt: time.Now().UTC()}
	if err := r.recorder.StartStep(ctx, exec, step); err != nil {
		return step, fmt.Errorf("record start step %q: %w", name, err)
	}
	return step, nil
}

func (r *Runner) skipExecutionStep(ctx context.Context, exec ExecutionContext, step ExecutionStep) error {
	step.Status = "SKIPPED"
	step.CompletedAt = time.Now().UTC()
	step.DurationMS = step.CompletedAt.Sub(step.StartedAt).Milliseconds()
	if err := r.recorder.CompleteStep(ctx, exec, step); err != nil {
		return fmt.Errorf("record skipped step %q: %w", step.Name, err)
	}
	return nil
}

func (r *Runner) completeExecutionStep(ctx context.Context, exec ExecutionContext, step ExecutionStep) error {
	step.Status = "COMPLETED"
	step.CompletedAt = time.Now().UTC()
	step.DurationMS = step.CompletedAt.Sub(step.StartedAt).Milliseconds()
	if err := r.recorder.CompleteStep(ctx, exec, step); err != nil {
		return fmt.Errorf("record complete step %q: %w", step.Name, err)
	}
	return nil
}

func (r *Runner) failExecutionStep(ctx context.Context, exec ExecutionContext, step ExecutionStep, cause error) error {
	step.Status = "FAILED"
	step.CompletedAt = time.Now().UTC()
	step.DurationMS = step.CompletedAt.Sub(step.StartedAt).Milliseconds()
	if cause != nil {
		step.ErrorMessage = cause.Error()
	}
	if err := r.recorder.FailStep(ctx, exec, step, cause); err != nil {
		return fmt.Errorf("record failed step %q: %w", step.Name, err)
	}
	return nil
}

func (r *Runner) attachInputAsset(ctx context.Context, exec ExecutionContext, stepID, assetID string, ordinal int) error {
	if strings.TrimSpace(assetID) == "" {
		return nil
	}
	if err := r.recorder.AttachInputAsset(ctx, exec, stepID, assetID, ordinal); err != nil {
		return fmt.Errorf("record input asset %q: %w", assetID, err)
	}
	return nil
}

func (r *Runner) attachOutputAsset(ctx context.Context, exec ExecutionContext, stepID, assetID string, ordinal int) error {
	if strings.TrimSpace(assetID) == "" {
		return nil
	}
	if err := r.recorder.AttachOutputAsset(ctx, exec, stepID, assetID, ordinal); err != nil {
		return fmt.Errorf("record output asset %q: %w", assetID, err)
	}
	return nil
}

func (r *Runner) recordExecutionMetric(ctx context.Context, exec ExecutionContext, stepID, name string, value float64, unit string) error {
	if err := r.recorder.RecordMetric(ctx, exec, stepID, name, value, unit); err != nil {
		return fmt.Errorf("record metric %q: %w", name, err)
	}
	return nil
}

// recordArtifactOperation records one artifact operation with its full
// correlation key. It is fail-closed like every other recorder call: a
// recorder error fails the run, never a silent skip — the trace is part of the
// contract, not best-effort observability.
func (r *Runner) recordArtifactOperation(ctx context.Context, exec ExecutionContext, op ArtifactOperation) error {
	if strings.TrimSpace(op.OperationID) == "" || strings.TrimSpace(op.Kind) == "" {
		return fmt.Errorf("artifact operation requires operation_id and kind")
	}
	if err := r.recorder.RecordOperation(ctx, exec, op); err != nil {
		return fmt.Errorf("record %s operation %q: %w", op.Kind, op.OperationID, err)
	}
	return nil
}

// artifactOperationID builds the stable per-attempt operation identifier from
// the operation kind and its disambiguating qualifiers (scene, language,
// artifact subject). It is a correlation key, not a display string: the
// attempt suffix makes a retry's operation distinct from the original while
// keeping the (kind, scene, language) lineage joinable.
func artifactOperationID(attempt int, qualifiers ...string) string {
	if attempt <= 0 {
		attempt = 1
	}
	parts := append([]string{}, qualifiers...)
	parts = append(parts, fmt.Sprintf("attempt-%d", attempt))
	return strings.Join(parts, ":")
}

// CertifyExecutionLineage verifies the latest state of every required step
// and the required input/output edges for one completed execution. Repeated
// start/complete callbacks for the same step are normal; the final callback
// wins by StepID. This prevents a stale RUNNING callback from masking a
// terminal FAILED/COMPLETED state.
func CertifyExecutionLineage(steps []ExecutionStep, inputAssets, outputAssets []string, requiredSteps []string) error {
	latest := make(map[string]ExecutionStep, len(steps))
	for _, step := range steps {
		if strings.TrimSpace(step.StepID) == "" || strings.TrimSpace(step.Name) == "" {
			return fmt.Errorf("lineage incomplete: step identity is required")
		}
		if _, exists := latest[step.StepID]; exists && step.StepID == "" {
			return fmt.Errorf("lineage incomplete: duplicate empty step identity")
		}
		latest[step.StepID] = step
	}
	seenNames := make(map[string]bool, len(latest))
	for _, step := range latest {
		if step.Status != "COMPLETED" {
			return fmt.Errorf("lineage incomplete: step %q ended in %q", step.Name, step.Status)
		}
		if step.CompletedAt.IsZero() || step.StartedAt.IsZero() {
			return fmt.Errorf("lineage incomplete: step %q has incomplete timestamps", step.Name)
		}
		seenNames[step.Name] = true
	}
	missing := make([]string, 0)
	for _, required := range requiredSteps {
		if !seenNames[required] {
			missing = append(missing, required)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("lineage incomplete: missing steps %v", missing)
	}
	if len(inputAssets) == 0 || len(outputAssets) == 0 {
		return fmt.Errorf("lineage incomplete: input and output asset edges are required")
	}
	for _, assetID := range append(append([]string{}, inputAssets...), outputAssets...) {
		if strings.TrimSpace(assetID) == "" {
			return fmt.Errorf("lineage incomplete: empty asset edge")
		}
	}
	return nil
}
