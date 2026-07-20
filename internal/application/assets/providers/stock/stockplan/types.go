// Package stockplan — batch planning for the stock pipeline.
//
// Fase 3 (July 2026): introduces a generic batch coordinator that
// turns a single source URL and a list of temporal groups into
// small per-group child stock jobs. Each child job receives at most
// MaxClipsPerGroup clips, and all children share the source through
// the cross-run source cache.
package stockplan

import (
	"context"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// DestinationSpec selects where the batch artifacts will land.
type DestinationSpec struct {
	DriveFolderID string `json:"drive_folder_id"`
	FolderName    string `json:"folder_name"`
}

// SamplingPolicy controls how temporal groups are sliced into clips.
type SamplingPolicy struct {
	ClipDurationSec     int `json:"clip_duration_sec"`
	MaxGroupDurationSec int `json:"max_group_duration_sec"`
	MaxClipsPerGroup    int `json:"max_clips_per_group"`
}

// GroupSpec describes one temporal slice of the source video.
type GroupSpec struct {
	Key      string            `json:"key"`
	Title    string            `json:"title"`
	StartSec float64           `json:"start_sec"`
	EndSec   float64           `json:"end_sec"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// BatchSpec is the canonical input to the batch coordinator.
type BatchSpec struct {
	SourceURL   string          `json:"source_url"`
	Destination DestinationSpec `json:"destination"`
	Sampling    SamplingPolicy  `json:"sampling"`
	Groups      []GroupSpec     `json:"groups"`
}

// PlannedGroup pairs a group with the clips generated for it.
type PlannedGroup struct {
	Key      string                   `json:"key"`
	Title    string                   `json:"title"`
	StartSec float64                  `json:"start_sec"`
	EndSec   float64                  `json:"end_sec"`
	Clips    []stockpipeline.ClipSpec `json:"clips"`
}

// BatchPlan is the resolved, data-only plan produced by PlanResolver.
type BatchPlan struct {
	BatchID     string          `json:"batch_id"`
	SourceURL   string          `json:"source_url"`
	Destination DestinationSpec `json:"destination"`
	Groups      []PlannedGroup  `json:"groups"`
}

// BatchRunResult is returned by the coordinator after a batch is started.
type BatchRunResult struct {
	BatchID string `json:"batch_id"`
	Status  string `json:"status"`
}

// BatchStatus is the full snapshot of a batch, including its groups.
type BatchStatus struct {
	Batch  *stockpipeline.StockBatch
	Groups []stockpipeline.StockBatchGroup
}

// ClipSampler turns a temporal group into a deterministic list of clips.
type ClipSampler interface {
	Sample(group GroupSpec, policy SamplingPolicy) ([]stockpipeline.ClipSpec, error)
}

// PlanResolver turns a BatchSpec into a BatchPlan.
type PlanResolver interface {
	Resolve(ctx context.Context, spec BatchSpec) (*BatchPlan, error)
}

// JobEnqueuer is the narrow port the coordinator needs to enqueue
// per-group child jobs.
type JobEnqueuer interface {
	Enqueue(ctx context.Context, req *job.EnqueueRequest) (*job.Job, error)
}

// SourceStager is the narrow port the coordinator can use to
// pre-stage the shared source. nil is allowed; in that case the
// coordinator relies on the first child job populating the cache.
type SourceStager interface {
	StageSource(ctx context.Context, url string) (*stockpipeline.StagedSource, error)
}

// BatchRepository is an alias for the canonical stock batch
// repository port so the coordinator package can reference it
// without re-declaring the full interface.
type BatchRepository = stockpipeline.StockBatchRepository

// Validate ensures the policy has sensible defaults.
func (p *SamplingPolicy) Normalize() {
	if p.ClipDurationSec <= 0 {
		p.ClipDurationSec = 4
	}
	if p.MaxGroupDurationSec <= 0 {
		p.MaxGroupDurationSec = 60
	}
	if p.MaxClipsPerGroup <= 0 {
		p.MaxClipsPerGroup = 15
	}
}

// Duration returns the effective clip duration as a time.Duration.
func (p *SamplingPolicy) Duration() time.Duration {
	return time.Duration(p.ClipDurationSec) * time.Second
}
