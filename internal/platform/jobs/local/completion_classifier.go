package local

import (
	"context"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	platformsqlite "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

// ClassifiedCompletionPort is the platform boundary between the jobs
// capability and the concrete persistence used by the local broker. It keeps
// driver-specific retry classification out of internal/capabilities/jobs.
type ClassifiedCompletionPort struct {
	inner appjobs.CompletionPort
}

var _ appjobs.CompletionPort = (*ClassifiedCompletionPort)(nil)

// NewClassifiedCompletionPort wraps a completion port with the canonical
// SQLite classifier. Nil stays nil so existing composition gates remain
// authoritative.
func NewClassifiedCompletionPort(inner appjobs.CompletionPort) appjobs.CompletionPort {
	if inner == nil {
		return nil
	}
	return &ClassifiedCompletionPort{inner: inner}
}

func (p *ClassifiedCompletionPort) CompleteWithArtifacts(ctx context.Context, cmd appjobs.CompleteWithArtifactsCommand) ([]string, error) {
	ids, err := p.inner.CompleteWithArtifacts(ctx, cmd)
	if err == nil {
		return ids, nil
	}
	decision, ok := platformsqlite.RetryClassifier(err)
	if ok && decision.Retryable {
		return ids, &retry.TransientInfrastructureError{Err: err}
	}
	return ids, err
}
