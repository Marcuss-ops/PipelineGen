package wiring

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/videocreate"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/instaeditcalendar"
)

type calendarScheduledPublisher struct {
	next videocreate.ArtifactPublisher
	gate scheduling.ScheduleGate
}

func (p calendarScheduledPublisher) Publish(ctx context.Context, req videocreate.PublishRequest) (videocreate.PublishedArtifact, error) {
	if err := p.gate.WaitUntilDue(ctx, req.JobID); err != nil {
		return videocreate.PublishedArtifact{}, err
	}
	return p.next.Publish(ctx, req)
}
