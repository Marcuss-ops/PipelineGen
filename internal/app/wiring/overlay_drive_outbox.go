package wiring

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/renderinggen"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// durableOverlayArtifactPublisher makes the Drive hand-off durable. It only
// records an event here; the registered handler performs the upload later.
// Therefore a successful script job means "artifact certified + publication
// intent durably recorded", not "an HTTP request happened to finish before
// the runner closed".
type durableOverlayArtifactPublisher struct {
	repo   *outboxevents.Repository
	direct scriptgen.OverlayArtifactPublisher
}

func (p *durableOverlayArtifactPublisher) PublishOverlay(ctx context.Context, spec scriptgen.OverlayPublicationSpec, artifact *scriptgen.RenderArtifact) error {
	if p == nil || p.repo == nil || p.direct == nil {
		return fmt.Errorf("overlay Drive outbox is not configured")
	}
	if artifact == nil || strings.TrimSpace(artifact.SHA256) == "" || artifact.SizeBytes <= 0 || strings.TrimSpace(artifact.URL) == "" {
		return fmt.Errorf("overlay Drive outbox requires a certified locator artifact")
	}
	payload, err := json.Marshal(renderinggen.OverlayDrivePublicationRequest{Spec: spec, Artifact: *artifact})
	if err != nil {
		return fmt.Errorf("encode overlay Drive publication: %w", err)
	}
	aggregateID := firstNonEmptyOverlay(spec.JobID, spec.PlanID, spec.ProjectID)
	eventKey := "overlay-drive:" + aggregateID + ":" + spec.Language + ":" + spec.PlanID + ":" + spec.OverlayItemID + ":" + strings.ToLower(artifact.SHA256)
	if _, err := p.repo.Enqueue(ctx, nil, renderinggen.EventOverlayDrivePublicationRequested, aggregateID, "overlay_artifact", string(payload), eventKey); err != nil {
		return fmt.Errorf("enqueue overlay Drive publication: %w", err)
	}
	return nil
}

func firstNonEmptyOverlay(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return "overlay"
}

type overlayDrivePublicationHandler struct {
	direct scriptgen.OverlayArtifactPublisher
}

func (h *overlayDrivePublicationHandler) EventType() string {
	return renderinggen.EventOverlayDrivePublicationRequested
}
func (h *overlayDrivePublicationHandler) IdempotencyKey() string {
	return renderinggen.EventOverlayDrivePublicationRequested
}

func (h *overlayDrivePublicationHandler) Handle(ctx context.Context, evt outboxevents.Event) error {
	if h == nil || h.direct == nil {
		return fmt.Errorf("overlay Drive handler is not configured")
	}
	var req renderinggen.OverlayDrivePublicationRequest
	if err := json.Unmarshal([]byte(evt.PayloadJSON), &req); err != nil {
		return fmt.Errorf("decode overlay Drive publication: %w", err)
	}
	if err := h.direct.PublishOverlay(ctx, req.Spec, &req.Artifact); err != nil {
		return fmt.Errorf("publish overlay Drive artifact: %w", err)
	}
	return nil
}

func wireDurableOverlayPublisher(root *ComposeRoot, direct scriptgen.OverlayArtifactPublisher) (scriptgen.OverlayArtifactPublisher, error) {
	if root == nil || root.Outbox == nil || root.Outbox.EventsRepo == nil || root.Outbox.EventsRegistry == nil {
		return direct, nil
	}
	handler := &overlayDrivePublicationHandler{direct: direct}
	if err := root.Outbox.EventsRegistry.Register(handler); err != nil {
		return nil, fmt.Errorf("register overlay Drive outbox handler: %w", err)
	}
	return &durableOverlayArtifactPublisher{repo: root.Outbox.EventsRepo, direct: direct}, nil
}
