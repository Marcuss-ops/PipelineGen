package ports

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/modules/scriptgen/domain"
)

// GenerationPayload is the data submitted to the async job worker. It
// captures only the inputs needed to re-run the generation so the worker
// can rehydrate state without depending on the HTTP request.
type GenerationPayload struct {
	Spec              domain.GenerationSpec `json:"spec"`
	CorrelationID     string                `json:"correlation_id"`
	SourceFingerprint string                `json:"source_fingerprint,omitempty"`
}

// JobSubmitter abstracts the long-running job system.
//
// The interface is intentionally small and decoupled from any concrete
// implementation (it does NOT import internal/application/jobs/*):
// Agent 1 will provide an adapter that wraps the existing *jobs.Service.
type JobSubmitter interface {
	SubmitGeneration(ctx context.Context, payload GenerationPayload) (domain.JobReference, error)
	GetStatus(ctx context.Context, jobID string) (status string, err error)
}
