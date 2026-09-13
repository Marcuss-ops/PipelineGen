package scriptgeneration

import (
	"context"
	"time"
)

// OverlayPublicationSpec carries the stable logical identity used by Drive
// routing. It deliberately contains no provider-specific fields: the
// platform adapter owns the concrete publication contract.
type OverlayPublicationSpec struct {
	ScriptName string
	Language   string
	ProjectID  string
	PlanID     string

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
	OverlayItemID    string
	OverlayItemKind  string
	OverlayEntityID  string
	OverlayText      string
	SourceStartUS    int64
	SourceEndUS      int64
	TargetDurationUS int64
}

// OverlayArtifactPublisher publishes a certified RenderingGen artifact after
// the queue has completed. Keeping this as a capability port means the queue
// path cannot silently report success while the required Drive side effect is
// still missing.
type OverlayArtifactPublisher interface {
	PublishOverlay(context.Context, OverlayPublicationSpec, *RenderArtifact) error
}
