package jobs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobregistry"
	kernjob "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"go.uber.org/zap"
)

// MetricRefresher is satisfied by the concrete jobs.Repository.
type MetricRefresher interface {
	RefreshMetrics(ctx context.Context) error
}

func StartMetricsRefresher(ctx context.Context, repo MetricRefresher, interval time.Duration, log *zap.Logger) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	go func() {
		if err := repo.RefreshMetrics(ctx); err != nil {
			log.Warn("metrics refresh failed (immediate tick)", zap.Error(err))
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := repo.RefreshMetrics(ctx); err != nil {
					log.Warn("metrics refresh failed", zap.Error(err))
				}
			}
		}
	}()
}

const jobRegistryProjectionTimeout = 2 * time.Second

type JobRegistryRecorder struct {
	registry capregistry.Registry
	log      *zap.Logger
	host     string
}

func NewJobRegistryRecorder(registry capregistry.Registry, log *zap.Logger) *JobRegistryRecorder {
	if log == nil {
		log = zap.NewNop()
	}
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		host = "unknown"
	}
	return &JobRegistryRecorder{registry: registry, log: log, host: host}
}
func (r *JobRegistryRecorder) enabled() bool { return r != nil && r.registry != nil }
func (r *JobRegistryRecorder) projectionContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(ctx, jobRegistryProjectionTimeout)
}

func (r *JobRegistryRecorder) Start(ctx context.Context, j *kernjob.Job, workerID, attemptID string) string {
	if !r.enabled() || j == nil {
		return ""
	}
	ctx, cancel := r.projectionContext(ctx)
	defer cancel()
	started := time.Now().UTC()
	if j.StartedAt != nil && !j.StartedAt.IsZero() {
		started = j.StartedAt.UTC()
	}
	// Anchor the persisted execution window to the run's start, not the
	// claim: duration_ms is RunReport.WallTimeMs (measured from run start),
	// so completed_at − started_at must not include the claim→run gap.
	if run := kernobs.FromContext(ctx); run != nil {
		if rep := run.Report(); rep != nil && !rep.StartedAt.IsZero() {
			started = rep.StartedAt.UTC()
		}
	}
	stepID := executionStepID(j.ID, attemptID, j.Revision)
	payload := rawJSON(j.Payload)
	if err := r.registry.RecordJob(ctx, capregistry.Job{JobID: j.ID, JobType: j.Type, Status: nonEmpty(string(j.Status), "RUNNING"), CorrelationID: j.CorrelationID, ProjectID: j.Project, VideoID: j.VideoName, ParentJobID: parentJobID(j.Payload), RootJobID: rootJobID(j.Payload), PayloadJSON: payload, PayloadHash: payloadHash(payload), ResultJSON: rawJSON(j.Result), GitSHA: payloadString(j.Payload, "git_sha"), AppVersion: payloadString(j.Payload, "app_version"), WorkerID: workerID, Host: r.host, CreatedAt: formatTime(j.CreatedAt), StartedAt: formatTime(started)}); err != nil {
		r.warn("record job start", j.ID, err)
	}
	if err := r.registry.RecordStep(ctx, capregistry.Step{StepID: stepID, JobID: j.ID, StepName: "worker.execution", StepType: "worker", Status: "RUNNING", StartedAt: started.Format(time.RFC3339Nano), CreatedAt: started.Format(time.RFC3339Nano)}); err != nil {
		r.warn("record worker step start", j.ID, err)
	}
	r.RecordInputs(ctx, j.ID, j.Payload)
	r.event(ctx, j.ID, "JOB_CLAIMED", map[string]any{"job_type": j.Type, "worker_id": workerID, "attempt_id": attemptID, "payload": json.RawMessage(payload)})
	return stepID
}
func (r *JobRegistryRecorder) Downloaded(ctx context.Context, jobID, assetID string, ordinal int) {
	if !r.enabled() || strings.TrimSpace(jobID) == "" || strings.TrimSpace(assetID) == "" {
		return
	}
	ctx, cancel := r.projectionContext(ctx)
	defer cancel()
	r.relate(ctx, capregistry.AssetRelation{JobID: jobID, AssetID: assetID, Relation: "DOWNLOADED", Ordinal: ordinal})
}
func (r *JobRegistryRecorder) RecordInputs(ctx context.Context, jobID string, payload []byte) {
	if !r.enabled() || strings.TrimSpace(jobID) == "" {
		return
	}
	ctx, cancel := r.projectionContext(ctx)
	defer cancel()
	for i, assetID := range inputAssetIDs(payload) {
		r.relate(ctx, capregistry.AssetRelation{JobID: jobID, AssetID: assetID, Relation: "INPUT", Ordinal: i})
	}
}
func (r *JobRegistryRecorder) RecordOutputs(ctx context.Context, jobID string, result []byte) {
	if !r.enabled() || strings.TrimSpace(jobID) == "" {
		return
	}
	ctx, cancel := r.projectionContext(ctx)
	defer cancel()
	for i, assetID := range outputAssetIDs(result) {
		r.relate(ctx, capregistry.AssetRelation{JobID: jobID, AssetID: assetID, Relation: "GENERATED", Ordinal: i})
	}
}

// RecordCanonicalOutputs persists the asset IDs returned by the
// transactional completion/finalization path. These IDs are authoritative
// and must not be inferred from a handler result or an artifact ID.
func (r *JobRegistryRecorder) RecordCanonicalOutputs(ctx context.Context, jobID, relation string, assetIDs []string) {
	if !r.enabled() || strings.TrimSpace(jobID) == "" {
		return
	}
	if strings.TrimSpace(relation) == "" {
		relation = "GENERATED"
	}
	ctx, cancel := r.projectionContext(ctx)
	defer cancel()
	ordinal := 0
	for _, assetID := range assetIDs {
		if strings.TrimSpace(assetID) == "" {
			continue
		}
		r.relate(ctx, capregistry.AssetRelation{JobID: jobID, AssetID: assetID, Relation: relation, Ordinal: ordinal})
		ordinal++
	}
}

// OutputRelationForJobType maps canonical output lineage to the job
// capability that produced it. Render jobs are tracked separately from
// generated source/media artifacts for reconciliation and statistics.
func OutputRelationForJobType(jobType string) string {
	if strings.Contains(strings.ToLower(jobType), "render") {
		return "RENDERED"
	}
	return "GENERATED"
}

func (r *JobRegistryRecorder) Finish(ctx context.Context, j *kernjob.Job, stepID, workerID, attemptID, status string, result []byte, errValue error, report *kernobs.RunReport) {
	if !r.enabled() || j == nil {
		return
	}
	// Terminal writes must survive handler timeout, lease cancellation, or
	// worker shutdown. The Job Registry is telemetry/provenance, so it uses
	// an independent bounded context rather than inheriting a canceled job
	// context.
	if ctx == nil || ctx.Err() != nil {
		ctx = context.Background()
	}
	ctx, cancel := r.projectionContext(ctx)
	defer cancel()
	finished := time.Now().UTC()
	started := finished
	if j.StartedAt != nil && !j.StartedAt.IsZero() {
		started = j.StartedAt.UTC()
	}
	// Anchor started_at to the run's start: duration_ms is RunReport.WallTimeMs,
	// measured from run start, so the claim→run dispatch gap (~1s) must not
	// inflate completed_at − started_at.
	if report != nil && !report.StartedAt.IsZero() {
		started = report.StartedAt.UTC()
	}
	duration := finished.Sub(started).Milliseconds()
	if report != nil && report.WallTimeMs > 0 {
		duration = report.WallTimeMs
	}
	if duration < 0 {
		duration = 0
	}
	message := ""
	if errValue != nil {
		message = errValue.Error()
	}
	resultJSON := rawJSON(result)
	completed := finished.Format(time.RFC3339Nano)
	if err := r.registry.UpdateJob(ctx, capregistry.Job{JobID: j.ID, JobType: j.Type, Status: nonEmpty(status, "FAILED"), CorrelationID: j.CorrelationID, ProjectID: j.Project, VideoID: j.VideoName, ParentJobID: parentJobID(j.Payload), RootJobID: rootJobID(j.Payload), PayloadJSON: rawJSON(j.Payload), PayloadHash: payloadHash(rawJSON(j.Payload)), ResultJSON: resultJSON, ErrorMessage: message, GitSHA: payloadString(j.Payload, "git_sha"), AppVersion: payloadString(j.Payload, "app_version"), WorkerID: workerID, Host: r.host, CreatedAt: formatTime(j.CreatedAt), StartedAt: formatTime(started), CompletedAt: completed, DurationMS: duration}); err != nil {
		r.warn("record job terminal state", j.ID, err)
	}
	if stepID == "" {
		stepID = executionStepID(j.ID, attemptID, j.Revision)
	}
	if err := r.registry.RecordStep(ctx, capregistry.Step{StepID: stepID, JobID: j.ID, StepName: "worker.execution", StepType: "worker", Status: statusForStep(status), StartedAt: formatTime(started), CompletedAt: completed, DurationMS: duration, MetricsJSON: reportJSON(report), CreatedAt: completed, ErrorMessage: message}); err != nil {
		r.warn("record worker step terminal state", j.ID, err)
	}
	if report != nil {
		r.recordReport(ctx, j.ID, stepID, report)
	}
	r.RecordOutputs(ctx, j.ID, result)
	r.event(ctx, j.ID, terminalEvent(status), map[string]any{"job_type": j.Type, "worker_id": workerID, "attempt_id": attemptID, "status": status, "duration_ms": duration, "result": json.RawMessage(resultJSON)})
}
func (r *JobRegistryRecorder) recordReport(ctx context.Context, jobID, stepID string, report *kernobs.RunReport) {
	ctx, cancel := r.projectionContext(ctx)
	defer cancel()
	for i, stage := range report.Stages {
		stageID := fmt.Sprintf("%s:stage:%d", stepID, i)
		if err := r.registry.RecordStep(ctx, capregistry.Step{StepID: stageID, JobID: jobID, StepName: stage.Name, StepType: "stage", Status: statusForStep(stage.Status), StartedAt: formatTime(stage.StartedAt), CompletedAt: formatTime(stage.FinishedAt), DurationMS: stage.DurationMs, InputCount: stage.ItemsInput, OutputCount: stage.ItemsCompleted, InputBytes: stage.BytesProcessed, MetricsJSON: reportJSON(stage), CreatedAt: formatTime(stage.StartedAt), ErrorCode: stage.ErrorCode}); err != nil {
			r.warn("record runtime stage", jobID, err)
		}
		r.metric(ctx, capregistry.Metric{MetricID: metricID(jobID, stageID, "duration_ms"), JobID: jobID, StepID: stageID, Name: "duration_ms", Unit: "ms", Value: float64(stage.DurationMs)})
	}
	for i, operation := range report.Operations {
		name := nonEmpty(operation.Operation, "operation")
		metricStepID := fmt.Sprintf("%s:operation:%d", stepID, i)
		r.metric(ctx, capregistry.Metric{MetricID: metricID(jobID, metricStepID, name+".duration_ms"), JobID: jobID, StepID: stepID, Name: name + ".duration_ms", Unit: "ms", Value: float64(operation.DurationMs)})
		r.metric(ctx, capregistry.Metric{MetricID: metricID(jobID, metricStepID, name+".items"), JobID: jobID, StepID: stepID, Name: name + ".items", Unit: "count", Value: float64(operation.Items)})
		r.metric(ctx, capregistry.Metric{MetricID: metricID(jobID, metricStepID, name+".bytes"), JobID: jobID, StepID: stepID, Name: name + ".bytes", Unit: "bytes", Value: float64(operation.Bytes)})
	}
	// Run-level durations are milliseconds; counters are dimensionless. The
	// two families are recorded separately so a wall/queue duration never
	// masquerades as a count in downstream aggregations.
	for name, value := range map[string]float64{"wall_time_ms": float64(report.WallTimeMs), "queue_wait_ms": float64(report.QueueWaitMs)} {
		r.metric(ctx, capregistry.Metric{MetricID: metricID(jobID, stepID, name), JobID: jobID, StepID: stepID, Name: name, Unit: "ms", Value: value})
	}
	for name, value := range map[string]float64{"cache_hits": float64(report.Counters.CacheHits), "cache_misses": float64(report.Counters.CacheMisses)} {
		r.metric(ctx, capregistry.Metric{MetricID: metricID(jobID, stepID, name), JobID: jobID, StepID: stepID, Name: name, Unit: "count", Value: value})
	}
}
func (r *JobRegistryRecorder) RecordProgress(ctx context.Context, jobID string, progress int, message string) {
	if !r.enabled() || strings.TrimSpace(jobID) == "" {
		return
	}
	ctx, cancel := r.projectionContext(ctx)
	defer cancel()
	stepID := fmt.Sprintf("%s:progress:%d", jobID, progress)
	if err := r.registry.RecordStep(ctx, capregistry.Step{StepID: stepID, JobID: jobID, StepName: "progress", StepType: "worker", Status: "COMPLETED", OutputCount: int64(progress), MetricsJSON: reportJSON(map[string]any{"message": message, "progress": progress}), CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}); err != nil {
		r.warn("record worker progress step", jobID, err)
	}
	r.metric(ctx, capregistry.Metric{MetricID: metricID(jobID, stepID, "progress"), JobID: jobID, StepID: stepID, Name: "progress", Unit: "percent", Value: float64(progress)})
}
func (r *JobRegistryRecorder) RecordEvent(ctx context.Context, jobID, eventType, message string, data map[string]any) {
	if r.enabled() {
		r.event(ctx, jobID, eventType, map[string]any{"message": message, "data": data})
	}
}
func (r *JobRegistryRecorder) relate(ctx context.Context, relation capregistry.AssetRelation) {
	if err := r.registry.RelateAsset(ctx, relation); err != nil {
		r.warn("record job asset lineage", relation.JobID, err)
	}
}
func (r *JobRegistryRecorder) metric(ctx context.Context, metric capregistry.Metric) {
	ctx, cancel := r.projectionContext(ctx)
	defer cancel()
	if err := r.registry.RecordMetric(ctx, metric); err != nil {
		r.warn("record job metric", metric.JobID, err)
	}
}
func (r *JobRegistryRecorder) event(ctx context.Context, jobID, eventType string, payload map[string]any) {
	ctx, cancel := r.projectionContext(ctx)
	defer cancel()
	body, err := json.Marshal(payload)
	if err != nil {
		r.warn("marshal job registry event", jobID, err)
		return
	}
	if _, err := r.registry.AppendEvent(ctx, capregistry.Event{JobID: jobID, EventType: eventType, PayloadJSON: string(body), CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}); err != nil {
		r.warn("append job registry event", jobID, err)
	}
}
func (r *JobRegistryRecorder) warn(operation, jobID string, err error) {
	if r.log != nil {
		r.log.Warn("job registry projection failed", zap.String("operation", operation), zap.String("job_id", jobID), zap.Error(err))
	}
}

func inputAssetIDs(raw []byte) []string {
	var v struct {
		AssetID     string `json:"asset_id"`
		InputAssets []struct {
			AssetID string `json:"asset_id"`
		} `json:"input_assets"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	ids := make([]string, 0, len(v.InputAssets)+1)
	if v.AssetID != "" {
		ids = append(ids, v.AssetID)
	}
	for _, item := range v.InputAssets {
		if item.AssetID != "" && !contains(ids, item.AssetID) {
			ids = append(ids, item.AssetID)
		}
	}
	return ids
}
func outputAssetIDs(raw []byte) []string {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	ids := make([]string, 0)
	var walk func(any, string)
	walk = func(node any, key string) {
		switch v := node.(type) {
		case map[string]any:
			for k, child := range v {
				if (k == "asset_id" || k == "remote_asset_id") && key != "input_assets" {
					if id, ok := child.(string); ok && id != "" && !contains(ids, id) {
						ids = append(ids, id)
					}
				}
				walk(child, k)
			}
		case []any:
			for _, child := range v {
				walk(child, key)
			}
		}
	}
	walk(value, "")
	return ids
}
func executionStepID(jobID, attemptID string, revision int) string {
	if strings.TrimSpace(attemptID) == "" {
		attemptID = fmt.Sprintf("revision-%d", revision)
	}
	return jobID + ":worker:" + attemptID
}
func metricID(jobID, stepID, name string) string {
	sum := sha256.Sum256([]byte(jobID + "\x00" + stepID + "\x00" + name))
	return "metric_" + hex.EncodeToString(sum[:])
}
func payloadHash(raw string) string {
	// Canonical empty guard: jobregistry.hashPayload maps an empty or
	// whitespace-only payload to "{}" before hashing so the persisted hash is
	// stable across both producers (SSOT edge case).
	if strings.TrimSpace(raw) == "" {
		raw = "{}"
	}
	var value any
	if json.Unmarshal([]byte(raw), &value) == nil {
		if b, err := json.Marshal(value); err == nil {
			raw = string(b)
		}
	}
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
func rawJSON(raw []byte) string {
	if len(raw) == 0 || !json.Valid(raw) {
		return "{}"
	}
	return string(raw)
}
func reportJSON(value any) string {
	b, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(b)
}
func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
func parentJobID(raw []byte) string { return payloadString(raw, "parent_job_id") }
func rootJobID(raw []byte) string   { return payloadString(raw, "root_job_id") }
func payloadString(raw []byte, key string) string {
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	result, _ := value[key].(string)
	return result
}
func statusForStep(status string) string {
	switch strings.ToUpper(status) {
	case "SUCCEEDED", "COMPLETED", "SUCCESS":
		return "COMPLETED"
	case "RUNNING":
		return "RUNNING"
	default:
		return "FAILED"
	}
}
func terminalEvent(status string) string {
	switch strings.ToUpper(status) {
	case "SUCCEEDED", "COMPLETED", "SUCCESS":
		return "JOB_COMPLETED"
	case "RETRY_WAIT", "QUEUED", "RETRYING":
		return "JOB_RETRY_SCHEDULED"
	case "DEAD_LETTER", "DEAD_LETTERED", "DLQ":
		return "JOB_DEAD_LETTERED"
	default:
		return "JOB_FAILED"
	}
}
func nonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
