// Package scriptgeneration — cross-repo contract check for the
// editorial_plan.v2 fixture matrix.
//
// The legacy Go planner is gone, so there is no second planner to diff against.
// What still needs a gate is the wire contract itself: every checked-in Rust
// request must decode through the Go producer DTO, and every checked-in sealed
// plan must decode through capmodel.OverlayItem with ids and scene windows
// intact. This replaces the deleted differential harness with the check that
// remains meaningful after the cutover.
package scriptgeneration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	overlayplan "github.com/Marcuss-ops/PipelineGen/internal/kernel/overlayplan"
)

const overlayPlanFixtureRoot = "../../../../BrainRust/overlay-compiler/tests/fixtures/overlay_plan"

func TestOverlayPlanV2FixturesDecodeThroughTheGoContract(t *testing.T) {
	if _, err := os.Stat(overlayPlanFixtureRoot); err != nil {
		t.Skipf("cross-repo BrainRust fixture unavailable: %v", err)
	}
	fixtures := []string{
		"minimal_phrase_only",
		"maps_trim_overlap",
		"entity_cards_verified_media",
		"editorial_toggles_charts_recap",
		"localized_short_phrase_empty",
		"recap_and_light_leaks",
		"general_chain_rendering",
		"chain_collision_resolution",
	}
	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			requestBytes, err := os.ReadFile(filepath.Join(overlayPlanFixtureRoot, "requests", name+".request.json"))
			if err != nil {
				t.Fatalf("read request: %v", err)
			}
			var request overlayplan.V2Request
			if err := json.Unmarshal(requestBytes, &request); err != nil {
				t.Fatalf("decode request through the Go DTO: %v", err)
			}
			if request.SchemaVersion != overlayplan.SchemaVersionV2 {
				t.Fatalf("request schema_version=%q, want %q", request.SchemaVersion, overlayplan.SchemaVersionV2)
			}
			if request.Plan.PlanID == "" || request.Plan.VideoID == "" {
				t.Fatalf("request identity is incomplete: %+v", request.Plan)
			}
			if len(request.SceneWindows) == 0 {
				t.Fatal("request carries no canonical scene windows")
			}
			planEndUS := int64(0)
			for _, bounds := range request.SceneWindows {
				if bounds[1] > planEndUS {
					planEndUS = bounds[1]
				}
			}

			planBytes, err := os.ReadFile(filepath.Join(overlayPlanFixtureRoot, "plans", name+".plan.json"))
			if err != nil {
				t.Fatalf("read sealed plan: %v", err)
			}
			var envelope struct {
				OK            bool   `json:"ok"`
				SchemaVersion string `json:"schema_version"`
				Plan          struct {
					SchemaVersion string                 `json:"schema_version"`
					PlanID        string                 `json:"plan_id"`
					VideoID       string                 `json:"video_id"`
					Items         []capmodel.OverlayItem `json:"items"`
				} `json:"plan"`
			}
			if err := json.Unmarshal(planBytes, &envelope); err != nil {
				t.Fatalf("decode sealed plan through the Go item DTO: %v", err)
			}
			if !envelope.OK || envelope.SchemaVersion != overlayplan.SchemaVersionV2 ||
				envelope.Plan.SchemaVersion != overlayplan.SchemaVersionV2 {
				t.Fatalf("sealed plan envelope is not editorial_plan.v2: %+v", envelope)
			}
			if envelope.Plan.PlanID != request.Plan.PlanID || envelope.Plan.VideoID != request.Plan.VideoID {
				t.Fatalf("plan identity %q/%q does not match request %q/%q",
					envelope.Plan.PlanID, envelope.Plan.VideoID, request.Plan.PlanID, request.Plan.VideoID)
			}
			if len(envelope.Plan.Items) == 0 {
				t.Fatal("sealed plan carries no items")
			}
			for index, item := range envelope.Plan.Items {
				if item.ID == "" || item.TemplateID == "" {
					t.Fatalf("item[%d] requires id and template_id: %+v", index, item)
				}
				bounds, ok := request.SceneWindows[item.SceneID]
				if !ok {
					t.Fatalf("item[%d] %q references scene %q without a request window", index, item.ID, item.SceneID)
				}
				start, end := item.StartUSValue(), item.EndUSValue()
				// A map carries the presentation window the Rust resolver bounded
				// to the plan; every other kind stays inside its own scene.
				allowedEnd := bounds[1]
				if strings.EqualFold(strings.TrimSpace(item.Kind), "map") && planEndUS > 0 {
					allowedEnd = planEndUS
				}
				if end <= start || start < bounds[0] || end > allowedEnd {
					t.Fatalf("item[%d] %q window [%d,%d) escapes its allowed extent [%d,%d)",
						index, item.ID, start, end, bounds[0], allowedEnd)
				}
			}
		})
	}
}
