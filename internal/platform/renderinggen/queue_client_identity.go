package renderinggen

import (
	"context"
	"fmt"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	queueclient "github.com/Marcuss-ops/RenderingGen/queue/client"
)

// classifyRenderSubmitCollision distinguishes an idempotent replay from a real
// identity conflict after the queue answered 409. The queue's unique id is the
// atomic guard; this read only classifies the collision. It never re-arms the
// existing job: a different identity must fail closed so a new request cannot
// be bound to an old render.
func classifyRenderSubmitCollision(ctx context.Context, client *queueclient.Client, jobID string, identity scriptgen.RenderRequestIdentity) error {
	want := identity.IdempotencyKey(jobID)
	existing, err := client.Get(ctx, jobID)
	if err != nil {
		return fmt.Errorf("%w: job %s exists but its identity cannot be verified: %v", scriptgen.ErrRenderRequestConflict, jobID, err)
	}
	if existing.ID != jobID {
		return fmt.Errorf("%w: job %s resolved to different queue id %s", scriptgen.ErrRenderRequestConflict, jobID, existing.ID)
	}
	if existing.IdempotencyKey == want {
		return fmt.Errorf("%w: job %s", scriptgen.ErrJobExists, jobID)
	}
	return fmt.Errorf("%w: job %s already exists with a different request identity", scriptgen.ErrRenderRequestConflict, jobID)
}

// classifyRenderBatchCollision applies the same rule to every member of an
// atomic chunk family. One mismatched or unverifiable member rejects the whole
// family; a family is only a replay when every identity matches.
func classifyRenderBatchCollision(ctx context.Context, client *queueclient.Client, jobs []scriptgen.RenderQueueJob, identities []scriptgen.RenderRequestIdentity) error {
	for i, job := range jobs {
		want := identities[i].IdempotencyKey(job.ID)
		existing, err := client.Get(ctx, job.ID)
		if err != nil {
			return fmt.Errorf("%w: chunk %s exists but its identity cannot be verified: %v", scriptgen.ErrRenderRequestConflict, job.ID, err)
		}
		if existing.IdempotencyKey != want {
			return fmt.Errorf("%w: chunk %s already exists with a different request identity", scriptgen.ErrRenderRequestConflict, job.ID)
		}
	}
	return fmt.Errorf("%w: chunk family", scriptgen.ErrJobExists)
}
