package renderqueue

import (
	"context"
	"time"
)

// OverlayPublicationSpec carries the stable logical identity used by Drive
// routing. It deliberately contains no provider-specific fields: the platform
// adapter owns the concrete publication contract.
type OverlayPublicationSpec struct {
	ScriptName string
	Language   string
	ProjectID  string
	// JobID is the stable generation/run identity used for Drive folder
	// routing. PlanID may be a per-item child plan (for example
	// job:item:003:phrase), so it must not be used as the Drive job folder
	// when separate item renders are enabled.
	JobID string
	// ResultJobID identifies the API job whose result receives the Drive link.
	ResultJobID string
	PlanID      string
	// DriveFolderID is the job-selected Drive parent. The platform publisher
	// creates/reuses its deterministic overlay child and only falls back to
	// its configured root when this is empty.
	DriveFolderID string
	// RequireObjectStoreBeforeReturn marks intermediate render artifacts (every
	// overlay render, and the final-job HTTPS payload) that stay in Objectstore
	// and are not published to Drive.
	RequireObjectStoreBeforeReturn bool
	RequireDriveBeforeReturn       bool

	// Completion metrics are captured by PipelineGen while waiting for the
	// RenderingGen queue. They are copied into the Drive receipt so the
	// published overlay has an auditable timing record next to it.
	CompletionWait  time.Duration
	PollingSleep    time.Duration
	PollingInterval time.Duration
	PollCount       int

	// OverlayItem* identify the single semantic item rendered into this
	// artifact. They make every Drive video/receipt auditable without forcing
	// downstream consumers to reconstruct the source plan.
	OverlayItemID       string
	OverlayItemKind     string
	OverlayMotionID     string
	OverlayEntityID     string
	OverlayEntityName   string
	OverlayEntityIDs    []string
	OverlayEntityLabels []string
	OverlayText         string
	SourceStartUS       int64
	SourceEndUS         int64
	TargetDurationUS    int64
	OverlayBatchID      string
	OverlayBatchItemIDs []string
	OverlayBatchEndUS   int64
}

// OverlayArtifactPublisher publishes a certified RenderingGen artifact after
// the queue has completed. Keeping this as a capability port means the queue
// path cannot silently report success while the required Drive side effect is
// still missing.
type OverlayArtifactPublisher interface {
	PublishOverlay(context.Context, OverlayPublicationSpec, *RenderArtifact) error
}

// OverlayArtifactProber verifies that a cached artifact locator still resolves
// in the object store before it is served as a completed render.
type OverlayArtifactProber interface {
	Present(ctx context.Context, artifact RenderArtifact) (bool, error)
}
