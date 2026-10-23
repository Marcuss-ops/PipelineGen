package wiring

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	obsmetrics "github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
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
	aggregateID := firstNonEmptyOverlay(spec.ResultJobID, spec.JobID, spec.PlanID, spec.ProjectID)
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
	// observer builds the delivery run that makes the REAL Drive upload
	// measurable: the outbox worker dispatches events outside any job run,
	// so without a bound run the publication cost would stay invisible wall
	// time (the overlay_upload metric on the render run only measures the
	// async enqueue). Nil degrades to the unmeasured pass-through.
	observer *kernobs.RunObserver
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
	jobID := firstNonEmptyOverlay(req.Spec.ResultJobID, evt.AggregateID, req.Spec.JobID)
	link := sqljobs.OverlayDriveLink{
		ItemID: req.Spec.OverlayItemID, Language: req.Spec.Language, PlanID: req.Spec.PlanID,
		DriveFileID: req.Artifact.DriveFileID, DriveLink: req.Artifact.DriveLink,
		FolderID: req.Artifact.DriveFolderID,
	}
	current, err := h.results.OverlayDriveLinkIsCurrent(ctx, jobID, link)
	if err != nil {
		if errors.Is(err, sqljobs.ErrOverlayDriveParentNotTerminal) {
			// Parent completion is a scheduling precondition, not a failed upload.
			// Defer without consuming attempts; the publisher will retry at a
			// one-minute cadence until the parent result is committed.
			return outboxevents.NewDeferredError(err, time.Minute)
		}
		return fmt.Errorf("check overlay Drive result target: %w", err)
	}
	if !current {
		// A render from a failed attempt can finish after the retry has already
		// committed a newer plan. Acknowledge this stale event without publishing.
		return nil
	}
	// Measure the REAL Drive upload as one canonical operation. When the
	// dispatch context carries no run (the normal outbox case) a delivery run
	// is bound for this event so the measurement lands in
	// run_operation_observations like every other owner-measured operation:
	// the rows carry the job identity and overlay item in metadata_json and
	// join the owning job through the delivery run's job_id.
	publishCtx := ctx
	if kernobs.FromContext(ctx) == nil && h.observer != nil {
		delivery := h.observer.StartRun(ctx, kernobs.RunInfo{
			JobID:     jobID,
			JobType:   renderinggen.EventOverlayDrivePublicationRequested,
			AttemptID: kernobs.NewAttemptID(),
		})
		publishCtx = kernobs.WithRun(ctx, delivery)
		defer delivery.Finish()
	}
	metadata, err := json.Marshal(map[string]any{
		"path":          "outbox",
		"job_id":        jobID,
		"plan_id":       req.Spec.PlanID,
		"item_id":       req.Spec.OverlayItemID,
		"language":      req.Spec.Language,
		"event_id":      evt.ID,
		"artifact_sha":  req.Artifact.SHA256,
		"artifact_size": req.Artifact.SizeBytes,
	})
	if err != nil {
		return fmt.Errorf("encode overlay Drive publication measurement: %w", err)
	}
	if err := kernobs.MeasureOperation(publishCtx, kernobs.OperationInfo{
		Stage:        kernobs.StageOverlayRender,
		Component:    kernobs.ComponentDrive,
		Operation:    kernobs.OperationDrivePublish,
		Provider:     "drive",
		Items:        1,
		MetadataJSON: string(metadata),
	}, func(measureCtx context.Context) error {
		return h.direct.PublishOverlay(measureCtx, req.Spec, &req.Artifact)
	}); err != nil {
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
	// The delivery-run observer persists measurements to the observability
	// database when available; without it the handler still publishes, only
	// unmeasured (fail-open: instrumentation must never block a publication).
	var observer *kernobs.RunObserver
	if root.ObservabilityDB != nil && root.ObservabilityDB.DB != nil {
		observer = kernobs.NewRunObserverWithCollector(
			obsmetrics.NewSQLiteRecorder(root.ObservabilityDB.DB),
			obsmetrics.NewRunReportsCollector(),
		)
	}
	handler := &overlayDrivePublicationHandler{direct: direct, results: root.Jobs.Repo, observer: observer}
	if err := root.Outbox.EventsRegistry.Register(handler); err != nil {
		return nil, fmt.Errorf("register overlay Drive outbox handler: %w", err)
	}
	return &durableOverlayArtifactPublisher{repo: root.Outbox.EventsRepo, direct: direct}, nil
}
