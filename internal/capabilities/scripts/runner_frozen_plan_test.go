package scriptgeneration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	overlayplan "github.com/Marcuss-ops/PipelineGen/internal/kernel/overlayplan"
)

const frozenRunnerPlanDir = "testdata/overlay_plan/runner"

// frozenRunnerPlanResolver serves a frozen plan for a Runner fixture that
// certifies editorial projection. The Runner calls the mandatory v2 entry
// point; only the plan payload is a checked-in test fixture.
type frozenRunnerPlanResolver struct {
	plan *capmodel.OverlayPlan
}

func (r frozenRunnerPlanResolver) CompileOverlayPlanV2(_ context.Context, _ json.RawMessage) (*capmodel.OverlayPlan, error) {
	return r.plan, nil
}

func loadFrozenRunnerPlan(t *testing.T, name string) *capmodel.OverlayPlan {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(frozenRunnerPlanDir, name))
	if err != nil {
		t.Fatalf("read frozen runner plan %s: %v", name, err)
	}
	var plan capmodel.OverlayPlan
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatalf("decode frozen runner plan %s: %v", name, err)
	}
	// EntityRef is producer-only (`json:"-"`), so it can never ride the frozen
	// wire plan. Reattach the identity this fixture certifies before the plan
	// reaches the Runner's presentation and validation boundaries.
	if name == "editorial_budget.plan.json" {
		for i := range plan.Items {
			if plan.Items[i].EntityID != "ent_52fd80dd0140190c" {
				continue
			}
			plan.Items[i].EntityRef = &capmodel.OverlayEntityRef{
				EntityID:          "ent_52fd80dd0140190c",
				Type:              "PERSON",
				Name:              "Tim Cook",
				CanonicalEntityID: "person:tim-cook",
			}
		}
	}
	return &plan
}

// entityChainRunnerResolver builds the entity-name chains expected by the
// entity-only fixture. It is derived from the v2 request's certified
// EntityTimeline, so the Runner exercises the single-call v2 entry point.
type entityChainRunnerResolver struct {
}

func (entityChainRunnerResolver) CompileOverlayPlanV2(_ context.Context, payload json.RawMessage) (*capmodel.OverlayPlan, error) {
	var request overlayplan.V2Request
	if err := json.Unmarshal(payload, &request); err != nil {
		return nil, err
	}
	plan := &capmodel.OverlayPlan{
		SchemaVersion: capmodel.SchemaVersionPlan,
		PlanID:        request.Plan.PlanID,
		VideoID:       request.Plan.VideoID,
		ProjectID:     request.Plan.ProjectID,
		Language:      request.Language,
		Width:         request.Plan.Width,
		Height:        request.Plan.Height,
		FPSNum:        request.Plan.FPSNum,
		FPSDen:        request.Plan.FPSDen,
		TimedScenes:   len(request.Scenes),
		Intake:        &capmodel.OverlayPlanIntake{Scenes: len(request.Scenes), TimedScenes: len(request.Scenes)},
	}
	if request.EntityTimeline != nil {
		for _, scene := range request.EntityTimeline.Scenes {
			segments := make([]capmodel.ChainSegment, 0, len(scene.Entities))
			var startUS, endUS int64
			haveStart := false
			for _, entity := range scene.Entities {
				segmentStart := entity.AudioStartUS
				if segmentStart == 0 {
					segmentStart = entity.TimelineStartUS
				}
				segmentDuration := entity.AudioEndUS - segmentStart
				if segmentDuration <= 0 {
					segmentDuration = 100_000
				}
				kind := "entity_card"
				if strings.EqualFold(entity.Type, "GPE") || strings.EqualFold(entity.Type, "LOCATION") {
					kind = "location"
				}
				segments = append(segments, capmodel.ChainSegment{
					Text:       entity.Name,
					Kind:       kind,
					TemplateID: strings.ToLower(entity.Type) + "_default",
					StartUS:    segmentStart,
					DurationUS: segmentDuration,
				})
				if !haveStart || segmentStart < startUS {
					startUS = segmentStart
					haveStart = true
				}
				if segmentEnd := segmentStart + segmentDuration; segmentEnd > endUS {
					endUS = segmentEnd
				}
			}
			if len(segments) < 2 {
				continue
			}
			plan.Items = append(plan.Items, capmodel.OverlayItem{
				ID:         "entity-chain-" + scene.SceneID,
				SceneID:    scene.SceneID,
				Kind:       capmodel.KindMixedChain,
				TemplateID: capmodel.TemplateMixedChain,
				Text:       segments[0].Text,
				StartUS:    startUS,
				DurationUS: endUS - startUS,
				StartMs:    startUS / 1000,
				EndMs:      (endUS + 999) / 1000,
				Params:     map[string]any{"chain_segments": segments},
			})
			if endMS := (endUS + 999) / 1000; endMS > plan.DurationMS {
				plan.DurationMS = endMS
			}
		}
	}
	for i := range plan.Items {
		plan.Items[i].RenderKey = capmodel.ComputeRenderKey(*plan, plan.Items[i])
	}
	plan.Fingerprint = plan.FingerprintValue()
	return plan, nil
}
