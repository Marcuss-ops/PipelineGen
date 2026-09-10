package scriptgeneration

import "context"

// OverlayPublicationSpec carries the stable logical identity used by Drive
// routing. It deliberately contains no provider-specific fields: the
// platform adapter owns the concrete publication contract.
type OverlayPublicationSpec struct {
	ScriptName string
	Language   string
	ProjectID  string
	PlanID     string
}

// OverlayArtifactPublisher publishes a certified RenderingGen artifact after
// the queue has completed. Keeping this as a capability port means the queue
// path cannot silently report success while the required Drive side effect is
// still missing.
type OverlayArtifactPublisher interface {
	PublishOverlay(context.Context, OverlayPublicationSpec, *RenderArtifact) error
}
