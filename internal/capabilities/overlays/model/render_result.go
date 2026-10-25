package model

import "fmt"

type RenderRequest struct {
	Plan        OverlayPlan `json:"plan"`
	OverlayID   string      `json:"overlay_id"`
	Speculative bool        `json:"speculative,omitempty"`
}

type RenderResult struct {
	SchemaVersion   string `json:"schema_version"`
	OverlayID       string `json:"overlay_id"`
	PlanID          string `json:"plan_id"`
	PlanFingerprint string `json:"plan_fingerprint"`
	RenderKey       string `json:"render_key"`
	ArtifactID      string `json:"artifact_id"`
	Filename        string `json:"filename"`
	LocalPath       string `json:"local_path,omitempty"`
	SHA256          string `json:"sha256"`
	SizeBytes       int64  `json:"size_bytes"`
	MIMEType        string `json:"mime_type"`
	Width           int    `json:"width"`
	Height          int    `json:"height"`
	FPSNum          int    `json:"fps_num"`
	FPSDen          int    `json:"fps_den"`
	DurationMs      int64  `json:"duration_ms"`
	HasAlpha        bool   `json:"has_alpha"`
	RendererVersion string `json:"renderer_version"`
	// SceneID and TemplateID tag the overlay's semantic origin (which scene
	// and which resolved template) so the persisted artifact is traceable
	// back to its OverlayIntent.
	SceneID    string `json:"scene_id,omitempty"`
	TemplateID string `json:"template_id,omitempty"`
	// MediaContract is the ID of the OverlayMediaContract the artifact was
	// certified against. Container / Codec / PixelFormat / AudioStreams are
	// the probed facts from the canonical media probe that passed Validate().
	MediaContract string `json:"media_contract,omitempty"`
	Container     string `json:"container,omitempty"`
	Codec         string `json:"codec,omitempty"`
	PixelFormat   string `json:"pixel_format,omitempty"`
	AudioStreams  int    `json:"audio_streams"`
	// Status is the canonical render-worker certification state
	// (OverlayStatusReady). It is only ever OverlayStatusReady because the
	// handler returns before building a RenderResult when probe or contract
	// validation fails.
	Status string `json:"status"`
}

func ValidateResultForPlan(plan OverlayPlan, result RenderResult) error {
	if err := plan.Validate(); err != nil {
		return err
	}
	if result.SchemaVersion != SchemaVersionResult {
		return fmt.Errorf("overlay result: unsupported schema version %q", result.SchemaVersion)
	}
	if result.PlanID != plan.PlanID || result.PlanFingerprint != plan.Fingerprint {
		return fmt.Errorf("overlay result: stale plan fingerprint")
	}
	for _, item := range plan.Items {
		if item.ID == result.OverlayID && item.RenderKey == result.RenderKey {
			return nil
		}
	}
	return fmt.Errorf("overlay result: render key does not belong to current plan")
}
