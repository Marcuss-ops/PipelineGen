package scriptgeneration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

// TestOverlayContractCorpusProducerWire drives the producer half of the shared
// corpus: every PipelineGen fixture must survive the production wire mapper,
// because that mapper (not the fixture) is what RenderingGen receives.
func TestOverlayContractCorpusProducerWire(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "contracts", "overlay-v2", "manifest.json"))
	if err != nil {
		t.Fatalf("read contract corpus manifest: %v", err)
	}
	var manifest struct {
		Fixtures []struct {
			ID     string `json:"id"`
			Source string `json:"source"`
			Kind   string `json:"kind"`
		} `json:"fixtures"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("decode contract corpus manifest: %v", err)
	}
	checked := 0
	for _, fixture := range manifest.Fixtures {
		if fixture.Kind != "pipelinegen_plan" {
			continue
		}
		checked++
		t.Run(fixture.ID, func(t *testing.T) {
			encoded, err := os.ReadFile(filepath.Join(root, fixture.Source))
			if err != nil {
				t.Fatalf("read %s: %v", fixture.Source, err)
			}
			var plan capmodel.OverlayPlan
			if err := json.Unmarshal(encoded, &plan); err != nil {
				t.Fatalf("decode %s: %v", fixture.ID, err)
			}
			wire, err := marshalRenderingGenOverlayPlan(plan)
			if err != nil {
				t.Fatalf("marshal %s: %v", fixture.ID, err)
			}
			var decoded struct {
				SchemaVersion string `json:"schema_version"`
				Items         []struct {
					EntityID string `json:"entity_id"`
					Map      struct {
						AreaGlowRadiusKM *float64 `json:"area_glow_radius_km"`
						Pins             []struct {
							Scope string `json:"scope"`
						} `json:"pins"`
					} `json:"map"`
				} `json:"items"`
			}
			if err := json.Unmarshal(wire, &decoded); err != nil {
				t.Fatalf("decode producer wire %s: %v", fixture.ID, err)
			}
			if decoded.SchemaVersion != "renderinggen.overlay-plan.v1" {
				t.Fatalf("%s schema = %q", fixture.ID, decoded.SchemaVersion)
			}
			for _, item := range decoded.Items {
				if item.Map.AreaGlowRadiusKM != nil {
					t.Fatalf("%s leaks producer-only area_glow_radius_km to RenderingGen", fixture.ID)
				}
				for _, pin := range item.Map.Pins {
					if pin.Scope != "" {
						t.Fatalf("%s leaks producer-only pin scope to RenderingGen", fixture.ID)
					}
				}
			}
		})
	}
	if checked == 0 {
		t.Fatal("manifest contains no pipelinegen_plan fixtures")
	}
}
