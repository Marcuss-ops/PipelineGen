package scriptgeneration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

// TestOverlayContractCorpusProducerWire drives the producer half of the shared
// corpus: every PipelineGen fixture must survive the production wire mapper,
// because that mapper (not the fixture) is what RenderingGen receives.
// contractCorpus is the resolved corpus location: the manifest path plus the
// resolver that maps a manifest source to a file on this checkout.
type contractCorpus struct {
	manifest string
	source   func(string) string
}

// vendoredContractCorpusDir is the copy committed with this module so a bare
// PipelineGen checkout (CI) can verify the wire contract without the sibling
// repositories. The workspace copy wins when it is present, so a cross-repo
// corpus update is exercised locally.
const vendoredContractCorpusDir = "testdata/overlay_contract_corpus"

// contractCorpusFor finds the corpus. Preference order: explicit env root,
// workspace root that owns contracts/, vendored module copy. Walking up keeps
// the test hermetic in a nested worktree, where the package sits deeper than in
// the main checkout.
func contractCorpusFor(t *testing.T) contractCorpus {
	t.Helper()
	workspace := func(root string) contractCorpus {
		return contractCorpus{
			manifest: filepath.Join(root, "contracts", "overlay-v2", "manifest.json"),
			source:   func(src string) string { return filepath.Join(root, filepath.FromSlash(src)) },
		}
	}
	if root := os.Getenv("VELOX_CONTRACT_CORPUS_ROOT"); root != "" {
		return workspace(root)
	}
	// The CI path can be exercised locally without hiding the workspace: the
	// escape hatch forces the vendored copy even inside the monorepo.
	if os.Getenv("VELOX_CONTRACT_CORPUS_VENDORED") != "1" {
		start, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		for dir := start; ; {
			if _, err := os.Stat(filepath.Join(dir, "contracts", "overlay-v2", "manifest.json")); err == nil {
				return workspace(dir)
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	vendored := contractCorpus{
		manifest: filepath.Join(vendoredContractCorpusDir, "manifest.json"),
		source: func(src string) string {
			src = strings.TrimPrefix(filepath.ToSlash(src), "refactored/")
			if rest, ok := strings.CutPrefix(src, "contracts/overlay-v2/"); ok {
				return filepath.Join(vendoredContractCorpusDir, filepath.FromSlash(rest))
			}
			// Module-relative fixture paths (refactored/... in the manifest).
			return filepath.Join("..", "..", "..", filepath.FromSlash(src))
		},
	}
	if _, err := os.Stat(vendored.manifest); err != nil {
		t.Fatalf("contract corpus not found: no workspace contracts/overlay-v2 and no vendored copy at %s; set VELOX_CONTRACT_CORPUS_ROOT", vendoredContractCorpusDir)
	}
	return vendored
}

func TestOverlayContractCorpusProducerWire(t *testing.T) {
	corpus := contractCorpusFor(t)
	raw, err := os.ReadFile(corpus.manifest)
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
			encoded, err := os.ReadFile(corpus.source(fixture.Source))
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
