package overlays

import (
	"fmt"
	"strings"
)

// SchemaVersionPrepare is the schema version of the overlay.prepare job
// payload: the run's pre-timing OverlayIntents plus the render canvas.
const SchemaVersionPrepare = "renderinggen.overlay-prepare.v1"

// PrepareRequest is the overlay.prepare job payload. It is submitted
// IMMEDIATELY after entity extraction — before TTS, before any timing — so
// the RenderingGen worker can resolve templates and prefetch the entity
// assets in parallel with audio synthesis. It carries the pre-timing
// OverlayIntents (each with its resolved template_id and PENDING timing
// state), never a timed OverlayPlan: prepare is the deterministic
// template/asset warm-up phase that precedes the timing-frozen render.
type PrepareRequest struct {
	SchemaVersion string          `json:"schema_version"`
	PlanID        string          `json:"plan_id"`
	VideoID       string          `json:"video_id"`
	ProjectID     string          `json:"project_id,omitempty"`
	Width         int             `json:"width"`
	Height        int             `json:"height"`
	FPS           int             `json:"fps"`
	Intents       []OverlayIntent `json:"intents"`
}

// Validate enforces the prepare contract: supported schema, non-empty
// identity, positive canvas, at least one intent, and every intent valid and
// still PENDING. Prepare is the pre-timing phase — a FROZEN intent must never
// be re-prepared, and an empty intent set is a caller error, not a no-op.
func (p PrepareRequest) Validate() error {
	if p.SchemaVersion != SchemaVersionPrepare {
		return fmt.Errorf("overlay prepare: unsupported schema version %q", p.SchemaVersion)
	}
	if strings.TrimSpace(p.PlanID) == "" || strings.TrimSpace(p.VideoID) == "" {
		return fmt.Errorf("overlay prepare: plan_id and video_id are required")
	}
	if p.Width <= 0 || p.Height <= 0 || p.FPS <= 0 {
		return fmt.Errorf("overlay prepare: width, height and fps must be positive")
	}
	if len(p.Intents) == 0 {
		return fmt.Errorf("overlay prepare: no intents to prepare")
	}
	for i := range p.Intents {
		if err := p.Intents[i].Validate(); err != nil {
			return fmt.Errorf("overlay prepare: intent[%d]: %w", i, err)
		}
		if p.Intents[i].TimingState != TimingStatePending {
			return fmt.Errorf("overlay prepare: intent[%d] is not PENDING (got %q)", i, p.Intents[i].TimingState)
		}
	}
	return nil
}
