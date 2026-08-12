package performance

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	capperformance "github.com/Marcuss-ops/PipelineGen/internal/capabilities/performance"
)

type Registry struct{ db *sql.DB }

func New(db *sql.DB) (*Registry, error) {
	if db == nil {
		return nil, errors.New("performance registry: nil database")
	}
	return &Registry{db: db}, nil
}

var _ capperformance.Registry = (*Registry)(nil)

func (r *Registry) RecordRun(ctx context.Context, run capperformance.Run) error {
	if run.RunID == "" || run.Status == "" || run.StartedAt == "" {
		return errors.New("performance registry: run identity, status and started_at are required")
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO performance_runs (run_id,job_id,root_job_id,video_id,workload_id,workload_version,git_sha,worker_id,host_id,status,wall_ms,cpu_user_ms,cpu_system_ms,peak_rss_bytes,disk_read_bytes,disk_write_bytes,network_rx_bytes,network_tx_bytes,metadata_json,started_at,completed_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(run_id) DO UPDATE SET status=excluded.status,wall_ms=excluded.wall_ms,cpu_user_ms=excluded.cpu_user_ms,cpu_system_ms=excluded.cpu_system_ms,peak_rss_bytes=excluded.peak_rss_bytes,disk_read_bytes=excluded.disk_read_bytes,disk_write_bytes=excluded.disk_write_bytes,network_rx_bytes=excluded.network_rx_bytes,network_tx_bytes=excluded.network_tx_bytes,metadata_json=excluded.metadata_json,completed_at=excluded.completed_at`, run.RunID, run.JobID, run.RootJobID, run.VideoID, run.WorkloadID, run.WorkloadVersion, run.GitSHA, run.WorkerID, run.HostID, run.Status, run.WallMS, run.CPUUserMS, run.CPUSystemMS, run.PeakRSSBytes, run.DiskReadBytes, run.DiskWriteBytes, run.NetworkRXBytes, run.NetworkTXBytes, nonEmpty(run.MetadataJSON, "{}"), run.StartedAt, nullIfEmpty(run.CompletedAt))
	if err != nil {
		return fmt.Errorf("record performance run %q: %w", run.RunID, err)
	}
	return nil
}

func (r *Registry) RecordStep(ctx context.Context, step capperformance.Step) error {
	if step.StepID == "" || step.RunID == "" || step.Name == "" || step.Status == "" {
		return errors.New("performance registry: step identity, name and status are required")
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO performance_steps (step_id,run_id,job_id,name,status,duration_ms,input_count,output_count,input_bytes,output_bytes,cache_hits,cache_misses,metadata_json,started_at,completed_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(step_id) DO UPDATE SET status=excluded.status,duration_ms=excluded.duration_ms,output_count=excluded.output_count,output_bytes=excluded.output_bytes,cache_hits=excluded.cache_hits,cache_misses=excluded.cache_misses,metadata_json=excluded.metadata_json,completed_at=excluded.completed_at`, step.StepID, step.RunID, step.JobID, step.Name, step.Status, step.DurationMS, step.InputCount, step.OutputCount, step.InputBytes, step.OutputBytes, step.CacheHits, step.CacheMisses, nonEmpty(step.MetadataJSON, "{}"), step.StartedAt, nullIfEmpty(step.CompletedAt))
	if err != nil {
		return fmt.Errorf("record performance step %q: %w", step.StepID, err)
	}
	return nil
}

func (r *Registry) RecordArtifact(ctx context.Context, a capperformance.Artifact) error {
	if a.ArtifactID == "" || a.RunID == "" || a.Kind == "" || a.SHA256 == "" || a.CreatedAt == "" {
		return errors.New("performance registry: artifact identity and sha256 are required")
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO performance_artifacts (artifact_id,run_id,kind,sha256,size_bytes,uri,created_at) VALUES (?,?,?,?,?,?,?) ON CONFLICT(artifact_id) DO UPDATE SET sha256=excluded.sha256,size_bytes=excluded.size_bytes,uri=excluded.uri`, a.ArtifactID, a.RunID, a.Kind, a.SHA256, a.SizeBytes, a.URI, a.CreatedAt)
	if err != nil {
		return fmt.Errorf("record performance artifact %q: %w", a.ArtifactID, err)
	}
	return nil
}

func (r *Registry) RegisterWorkload(ctx context.Context, w capperformance.Workload) error {
	if w.WorkloadID == "" || w.Version == "" || w.InputManifestSHA256 == "" || w.CreatedAt == "" {
		return errors.New("performance registry: workload identity and input manifest are required")
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO benchmark_workloads (workload_id,version,input_manifest_sha256,parameters_json,expected_output_sha256,created_at) VALUES (?,?,?,?,?,?) ON CONFLICT(workload_id,version) DO UPDATE SET input_manifest_sha256=excluded.input_manifest_sha256,parameters_json=excluded.parameters_json,expected_output_sha256=excluded.expected_output_sha256`, w.WorkloadID, w.Version, w.InputManifestSHA256, nonEmpty(w.ParametersJSON, "{}"), w.ExpectedOutputSHA256, w.CreatedAt)
	if err != nil {
		return fmt.Errorf("register workload %q/%q: %w", w.WorkloadID, w.Version, err)
	}
	return nil
}

func nonEmpty(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}
