package rustexec

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	overlayplan "github.com/Marcuss-ops/PipelineGen/internal/kernel/overlayplan"
)

const editorialPlanLabel = "semantic-engine editorial plan"

// compiledOverlayPlanV2 is the final-plan response. Items decode directly into
// the renderer DTO; the planner counts stay explicit because OverlayPlan keeps
// them application-only (json:"-").
type compiledOverlayPlanV2 struct {
	SchemaVersion   string                 `json:"schema_version"`
	PlanID          string                 `json:"plan_id"`
	VideoID         string                 `json:"video_id"`
	ProjectID       string                 `json:"project_id,omitempty"`
	Language        string                 `json:"language"`
	Width           int                    `json:"width"`
	Height          int                    `json:"height"`
	FPSNum          int                    `json:"fps_num"`
	FPSDen          int                    `json:"fps_den"`
	DurationMS      int64                  `json:"duration_ms"`
	Background      json.RawMessage        `json:"background,omitempty"`
	Items           []capmodel.OverlayItem `json:"items"`
	RecapPlacements int                    `json:"recap_placements"`
	RecapCards      int                    `json:"recap_cards"`
	ChartPlacements int                    `json:"chart_placements"`
	ChartCards      int                    `json:"chart_cards"`
	TimedScenes     int                    `json:"timed_scenes"`
	OutputProfileID string                 `json:"output_profile_id,omitempty"`
}

type compileOverlayPlanV2Response struct {
	OK            bool                   `json:"ok"`
	Error         string                 `json:"error,omitempty"`
	SchemaVersion string                 `json:"schema_version"`
	Plan          *compiledOverlayPlanV2 `json:"plan,omitempty"`
}

// CompileOverlayPlanV2 sends the complete editorial_plan.v2 envelope as the
// top-level request. The worker answers with the sealed final plan; no sparse
// per-concern batch operation remains in the Go contract. The request is
// compacted to one NDJSON line and the response is decoded into the renderer's
// OverlayPlan.
func (a *SemanticEngineAdapter) CompileOverlayPlanV2(ctx context.Context, request json.RawMessage) (*capmodel.OverlayPlan, error) {
	if a == nil || a.executor == nil {
		return nil, fmt.Errorf("%s: executor is not configured", editorialPlanLabel)
	}
	var envelope struct {
		SchemaVersion string `json:"schema_version"`
	}
	if err := json.Unmarshal(request, &envelope); err != nil {
		return nil, fmt.Errorf("%s decode request: %w", editorialPlanLabel, err)
	}
	if envelope.SchemaVersion != overlayplan.SchemaVersionV2 {
		return nil, fmt.Errorf("%s unsupported schema_version %q", editorialPlanLabel, envelope.SchemaVersion)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, request); err != nil {
		return nil, fmt.Errorf("%s compact request: %w", editorialPlanLabel, err)
	}
	stdout, _, err := a.executor.Run(ctx, compact.Bytes())
	if err != nil {
		return nil, fmt.Errorf("%s execute: %w", editorialPlanLabel, err)
	}
	var response compileOverlayPlanV2Response
	if err := json.Unmarshal(stdout, &response); err != nil {
		return nil, fmt.Errorf("%s decode: %w", editorialPlanLabel, err)
	}
	if !response.OK {
		return nil, fmt.Errorf("%s worker: %s", editorialPlanLabel, strings.TrimSpace(response.Error))
	}
	if response.Plan == nil {
		return nil, fmt.Errorf("%s returned no final plan", editorialPlanLabel)
	}
	plan := response.Plan
	if response.SchemaVersion != overlayplan.SchemaVersionV2 || plan.SchemaVersion != overlayplan.SchemaVersionV2 {
		return nil, fmt.Errorf("%s returned schema %q/%q, want %q", editorialPlanLabel, response.SchemaVersion, plan.SchemaVersion, overlayplan.SchemaVersionV2)
	}
	out := &capmodel.OverlayPlan{
		// editorial_plan.v2 is the Rust compiler's request/response contract.
		// Its compiled result is projected into the Go renderer model, whose
		// public plan contract remains overlay-plan.v1.
		SchemaVersion:   capmodel.SchemaVersionPlan,
		PlanID:          plan.PlanID,
		VideoID:         plan.VideoID,
		ProjectID:       plan.ProjectID,
		Language:        plan.Language,
		Width:           plan.Width,
		Height:          plan.Height,
		FPSNum:          plan.FPSNum,
		FPSDen:          plan.FPSDen,
		DurationMS:      plan.DurationMS,
		Items:           plan.Items,
		RecapPlacements: plan.RecapPlacements,
		RecapCards:      plan.RecapCards,
		ChartPlacements: plan.ChartPlacements,
		ChartCards:      plan.ChartCards,
		TimedScenes:     plan.TimedScenes,
		OutputProfileID: plan.OutputProfileID,
	}
	if background := strings.TrimSpace(string(plan.Background)); background != "" && background != "null" {
		var decoded capmodel.OverlayBackground
		if err := json.Unmarshal(plan.Background, &decoded); err != nil {
			return nil, fmt.Errorf("%s decode background: %w", editorialPlanLabel, err)
		}
		out.Background = &decoded
	}
	return out, nil
}

var _ interface {
	CompileOverlayPlanV2(context.Context, json.RawMessage) (*capmodel.OverlayPlan, error)
} = (*SemanticEngineAdapter)(nil)
