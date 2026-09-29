package wiring

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/renderinggen"
	sqljobs "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/jobs"
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
	// Final-job scene composites are immediately referenced by the PREPARE
	// payload, so their Drive identity must be available before this method
	// returns. Publish these few scene assets synchronously, then retain the
	// outbox event for the normal idempotent result projection and audit trail.
	// Ordinary per-item overlays remain on the asynchronous path.
	if spec.RequireDriveBeforeReturn || strings.Contains(spec.PlanID, ":final-composite:") {
		if err := p.direct.PublishOverlay(ctx, spec, artifact); err != nil {
			return fmt.Errorf("publish final-job composite to Drive: %w", err)
		}
		// The caller needs the Drive identity on this same artifact instance
		// before it builds the single final_job request. Do not enqueue a second
		// publication whose renderer job ID is not the parent script job ID.
		return nil
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
	direct  scriptgen.OverlayArtifactPublisher
	results *sqljobs.SQLiteStore
}

func (h *overlayDrivePublicationHandler) EventType() string {
	return renderinggen.EventOverlayDrivePublicationRequested
}
func (h *overlayDrivePublicationHandler) IdempotencyKey() string {
	return renderinggen.EventOverlayDrivePublicationRequested
}

func (h *overlayDrivePublicationHandler) Handle(ctx context.Context, evt outboxevents.Event) error {
	if h == nil || h.direct == nil || h.results == nil {
		return fmt.Errorf("overlay Drive handler is not configured")
	}
	var req renderinggen.OverlayDrivePublicationRequest
	if err := json.Unmarshal([]byte(evt.PayloadJSON), &req); err != nil {
		return fmt.Errorf("decode overlay Drive publication: %w", err)
	}
	jobID := firstNonEmptyOverlay(evt.AggregateID, req.Spec.JobID)
	link := sqljobs.OverlayDriveLink{
		ItemID: req.Spec.OverlayItemID, Language: req.Spec.Language, PlanID: req.Spec.PlanID,
		DriveFileID: req.Artifact.DriveFileID, DriveLink: req.Artifact.DriveLink,
		FolderID: req.Artifact.DriveFolderID,
	}
	current, err := h.results.OverlayDriveLinkIsCurrent(ctx, jobID, link)
	if err != nil {
		// The outbox retries while the parent job is running. Wait before the
		// external upload so every retry does not create another Drive file.
		return fmt.Errorf("check overlay Drive result target: %w", err)
	}
	if !current {
		// A render from a failed attempt can finish after the retry has already
		// committed a newer plan. Acknowledge this stale event without publishing.
		return nil
	}
	if err := h.direct.PublishOverlay(ctx, req.Spec, &req.Artifact); err != nil {
		return fmt.Errorf("publish overlay Drive artifact: %w", err)
	}
	link.DriveFileID = req.Artifact.DriveFileID
	link.DriveLink = req.Artifact.DriveLink
	link.FolderID = req.Artifact.DriveFolderID
	if err := h.results.RecordOverlayDriveLink(ctx, jobID, link); err != nil {
		return fmt.Errorf("project overlay Drive link into job result: %w", err)
	}
	return nil
}

func wireDurableOverlayPublisher(root *ComposeRoot, direct scriptgen.OverlayArtifactPublisher) (scriptgen.OverlayArtifactPublisher, error) {
	if root == nil || root.Outbox == nil || root.Outbox.EventsRepo == nil || root.Outbox.EventsRegistry == nil {
		return direct, nil
	}
	if root.Jobs == nil || root.Jobs.Repo == nil {
		return nil, fmt.Errorf("wire overlay Drive publisher: jobs result repository is required")
	}
	handler := &overlayDrivePublicationHandler{direct: direct, results: root.Jobs.Repo}
	if err := root.Outbox.EventsRegistry.Register(handler); err != nil {
		return nil, fmt.Errorf("register overlay Drive outbox handler: %w", err)
	}
	return &durableOverlayArtifactPublisher{repo: root.Outbox.EventsRepo, direct: direct}, nil
}
