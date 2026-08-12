package jobregistry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobregistry"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

// JobRegistryExecutionRecorder adapts the execution contract to the durable
// SQLite Job Registry. It is a platform adapter; pipeline policy remains in
// the scripts capability.
type JobRegistryExecutionRecorder struct{ registry capregistry.Registry }

func NewJobRegistryExecutionRecorder(registry capregistry.Registry) *JobRegistryExecutionRecorder {
	return &JobRegistryExecutionRecorder{registry: registry}
}

func (r *JobRegistryExecutionRecorder) valid(exec scriptgen.ExecutionContext) error {
	if r == nil || r.registry == nil {
		return fmt.Errorf("execution recorder: job registry is not configured")
	}
	return exec.Validate()
}

func (r *JobRegistryExecutionRecorder) StartStep(ctx context.Context, exec scriptgen.ExecutionContext, step scriptgen.ExecutionStep) error {
	if err := r.valid(exec); err != nil {
		return err
	}
	return r.registry.RecordStep(ctx, registryStep(exec, step, "RUNNING", ""))
}

func (r *JobRegistryExecutionRecorder) CompleteStep(ctx context.Context, exec scriptgen.ExecutionContext, step scriptgen.ExecutionStep) error {
	if err := r.valid(exec); err != nil {
		return err
	}
	status := step.Status
	if status == "" {
		status = "COMPLETED"
	}
	if status != "COMPLETED" && status != "SKIPPED" {
		return fmt.Errorf("execution recorder: invalid completion status %q", status)
	}
	return r.registry.RecordStep(ctx, registryStep(exec, step, status, step.ErrorMessage))
}

func (r *JobRegistryExecutionRecorder) FailStep(ctx context.Context, exec scriptgen.ExecutionContext, step scriptgen.ExecutionStep, cause error) error {
	if err := r.valid(exec); err != nil {
		return err
	}
	message := step.ErrorMessage
	if cause != nil {
		message = cause.Error()
	}
	return r.registry.RecordStep(ctx, registryStep(exec, step, "FAILED", message))
}

func (r *JobRegistryExecutionRecorder) AttachInputAsset(ctx context.Context, exec scriptgen.ExecutionContext, stepID, assetID string, ordinal int) error {
	return r.attachAsset(ctx, exec, stepID, assetID, ordinal, "INPUT")
}

func (r *JobRegistryExecutionRecorder) AttachOutputAsset(ctx context.Context, exec scriptgen.ExecutionContext, stepID, assetID string, ordinal int) error {
	return r.attachAsset(ctx, exec, stepID, assetID, ordinal, "GENERATED")
}

func (r *JobRegistryExecutionRecorder) attachAsset(ctx context.Context, exec scriptgen.ExecutionContext, stepID, assetID string, ordinal int, relation string) error {
	if err := r.valid(exec); err != nil {
		return err
	}
	if strings.TrimSpace(stepID) == "" || strings.TrimSpace(assetID) == "" {
		return fmt.Errorf("execution recorder: step_id and asset_id are required")
	}
	return r.registry.RelateAsset(ctx, capregistry.AssetRelation{JobID: exec.JobID, AssetID: assetID, Relation: relation, StepID: stepID, Ordinal: ordinal, CreatedAt: nowRegistryString()})
}

func (r *JobRegistryExecutionRecorder) RecordMetric(ctx context.Context, exec scriptgen.ExecutionContext, stepID, name string, value float64, unit string) error {
	if err := r.valid(exec); err != nil {
		return err
	}
	if strings.TrimSpace(stepID) == "" || strings.TrimSpace(name) == "" {
		return fmt.Errorf("execution recorder: step_id and metric name are required")
	}
	return r.registry.RecordMetric(ctx, capregistry.Metric{MetricID: metricID(exec.JobID, stepID, name), JobID: exec.JobID, StepID: stepID, Name: name, Value: value, Unit: unit, CreatedAt: nowRegistryString()})
}

func registryStep(exec scriptgen.ExecutionContext, step scriptgen.ExecutionStep, status, message string) capregistry.Step {
	return capregistry.Step{StepID: step.StepID, JobID: exec.JobID, StepName: step.Name, StepType: step.Type, Status: status, StartedAt: registryTime(step.StartedAt), CompletedAt: registryTime(step.CompletedAt), DurationMS: step.DurationMS, ErrorMessage: message, CreatedAt: registryTime(step.StartedAt), MetricsJSON: fmt.Sprintf(`{"root_job_id":%q,"parent_job_id":%q,"project_id":%q,"video_id":%q,"correlation_id":%q,"attempt":%d}`, exec.RootJobID, exec.ParentJobID, exec.ProjectID, exec.VideoID, exec.CorrelationID, exec.Attempt)}
}

func metricID(jobID, stepID, name string) string {
	sum := sha256.Sum256([]byte(jobID + "\x00" + stepID + "\x00" + name))
	return "script_metric_" + hex.EncodeToString(sum[:])
}
func registryTime(value time.Time) string {
	if value.IsZero() {
		return nowRegistryString()
	}
	return value.UTC().Format(time.RFC3339Nano)
}
func nowRegistryString() string { return time.Now().UTC().Format(time.RFC3339Nano) }

var _ scriptgen.ExecutionRecorder = (*JobRegistryExecutionRecorder)(nil)
