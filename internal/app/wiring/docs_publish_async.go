package wiring

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type docsPublishJobEnqueuer interface {
	Enqueue(context.Context, *job.EnqueueRequest) (*job.Job, error)
}

type docsPublishChildEnqueuer struct{ jobs docsPublishJobEnqueuer }

var _ scriptgen.DocsPublishEnqueuer = (*docsPublishChildEnqueuer)(nil)

func (e *docsPublishChildEnqueuer) EnqueueDocsPublish(ctx context.Context, req scriptgen.DocsPublishEnqueue) error {
	if e == nil || e.jobs == nil {
		return fmt.Errorf("docs publish child enqueue: jobs service is not wired")
	}
	parentID, runID := strings.TrimSpace(req.ParentJobID), strings.TrimSpace(req.RunID)
	if parentID == "" || runID == "" {
		return fmt.Errorf("docs publish child enqueue: parent job id and run id are required")
	}
	payload, err := json.Marshal(map[string]string{
		"parent_job_id": parentID, "parent_job_type": scriptpkg.TypeGenerate, "run_id": runID,
	})
	if err != nil {
		return fmt.Errorf("docs publish child enqueue: encode payload: %w", err)
	}
	payload = job.InjectParentLink(payload, job.ParentLink{ParentJobID: parentID, ParentRunID: runID})
	var linkedPayload map[string]any
	if err := json.Unmarshal(payload, &linkedPayload); err != nil {
		return fmt.Errorf("docs publish child enqueue: decode linked payload: %w", err)
	}
	correlationID := strings.TrimSpace(req.CorrelationID)
	if correlationID == "" {
		correlationID = parentID + ":docs:" + runID
	}
	child, err := e.jobs.Enqueue(ctx, &job.EnqueueRequest{
		Type: scriptpkg.TypeDocsPublish, Payload: linkedPayload, CorrelationID: correlationID,
		ActiveKey:  "docs-publish:" + parentID + ":" + runID,
		MaxRetries: appjobs.Compose().DefaultMaxRetries(scriptpkg.TypeDocsPublish),
	})
	if err != nil {
		return fmt.Errorf("docs publish child enqueue: %w", err)
	}
	if child == nil || strings.TrimSpace(child.ID) == "" {
		return fmt.Errorf("docs publish child enqueue: jobs service returned an empty child")
	}
	return nil
}
