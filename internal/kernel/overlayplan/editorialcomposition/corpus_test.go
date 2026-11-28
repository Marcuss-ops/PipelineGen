package editorialcomposition

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var updateCorpus = flag.Bool("update", false, "rewrite the lowered corpus fixtures from the current lowering")

// corpusManifest is the shared authority of the editorial composition corpus.
// Paths are workspace-relative, exactly like `contracts/overlay-v2`, so the
// same manifest is readable by every boundary.
type corpusManifest struct {
	SchemaVersion string          `json:"schema_version"`
	Capabilities  string          `json:"capabilities"`
	Fixtures      []corpusFixture `json:"fixtures"`
}

type corpusFixture struct {
	ID            string `json:"id"`
	Source        string `json:"source"`
	Expect        string `json:"expect"`
	Lowered       string `json:"lowered,omitempty"`
	Layers        int    `json:"layers,omitempty"`
	Code          string `json:"code,omitempty"`
	ErrorContains string `json:"error_contains,omitempty"`
}

func workspaceRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "contracts", "editorial-composition-v1", "manifest.json")); err != nil {
		t.Skipf("workspace contract corpus is not available: %v", err)
	}
	return root
}

func loadCorpus(t *testing.T) (string, corpusManifest, *Catalog) {
	t.Helper()
	root := workspaceRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "contracts", "editorial-composition-v1", "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest corpusManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if manifest.SchemaVersion != "editorial-composition-corpus.v1" {
		t.Fatalf("unexpected corpus schema %q", manifest.SchemaVersion)
	}
	catalogRaw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(manifest.Capabilities)))
	if err != nil {
		t.Fatalf("read capability catalog: %v", err)
	}
	catalog, err := ParseCatalog(catalogRaw)
	if err != nil {
		t.Fatalf("parse capability catalog: %v", err)
	}
	return root, manifest, catalog
}

func TestCorpusManifest(t *testing.T) {
	_, manifest, catalog := loadCorpus(t)
	if len(manifest.Fixtures) < 12 {
		t.Fatalf("corpus has %d fixtures, need at least 12", len(manifest.Fixtures))
	}
	if catalog.Len() < 8 {
		t.Fatalf("capability catalog declares %d motions, need at least 8", catalog.Len())
	}
	seen := make(map[string]struct{}, len(manifest.Fixtures))
	rejected := 0
	for _, fixture := range manifest.Fixtures {
		if _, duplicate := seen[fixture.ID]; duplicate {
			t.Fatalf("duplicate fixture id %q", fixture.ID)
		}
		seen[fixture.ID] = struct{}{}
		switch fixture.Expect {
		case "compiled":
			if fixture.Lowered == "" || fixture.Layers == 0 {
				t.Fatalf("compiled fixture %q must pin lowered bytes and a layer count", fixture.ID)
			}
		case "rejected":
			rejected++
			if fixture.Code == "" || fixture.ErrorContains == "" {
				t.Fatalf("rejected fixture %q must pin a code and a message fragment", fixture.ID)
			}
			if CodeOf(&Error{Code: fixture.Code}) != fixture.Code {
				t.Fatalf("fixture %q pins an unknown code %q", fixture.ID, fixture.Code)
			}
		default:
			t.Fatalf("fixture %q has unknown expectation %q", fixture.ID, fixture.Expect)
		}
	}
	if rejected < 5 {
		t.Fatalf("corpus has %d rejected fixtures, need at least 5", rejected)
	}
}

func TestCorpusCompositions(t *testing.T) {
	root, manifest, catalog := loadCorpus(t)
	for _, fixture := range manifest.Fixtures {
		t.Run(fixture.ID, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(fixture.Source)))
			if err != nil {
				t.Fatalf("read %s: %v", fixture.Source, err)
			}
			switch fixture.Expect {
			case "rejected":
				if err := decodeAndValidate(raw, catalog); err == nil {
					t.Fatalf("composition validated, want %q", fixture.Code)
				} else if code := CodeOf(err); code != fixture.Code {
					t.Fatalf("rejection code = %q, want %q (%v)", code, fixture.Code, err)
				} else if !strings.Contains(err.Error(), fixture.ErrorContains) {
					t.Fatalf("rejection %q does not contain %q", err, fixture.ErrorContains)
				}
			case "compiled":
				composition, err := Decode(raw)
				if err != nil {
					t.Fatalf("decode: %v", err)
				}
				lowered, err := composition.Lower(catalog)
				if err != nil {
					t.Fatalf("lower: %v", err)
				}
				if len(lowered.Layers) != fixture.Layers {
					t.Fatalf("layers = %d, want %d", len(lowered.Layers), fixture.Layers)
				}
				encoded, err := MarshalLayers(lowered)
				if err != nil {
					t.Fatalf("marshal layers: %v", err)
				}
				goldenPath := filepath.Join(root, filepath.FromSlash(fixture.Lowered))
				if *updateCorpus {
					if err := os.WriteFile(goldenPath, encoded, 0o644); err != nil {
						t.Fatalf("write golden %s: %v", goldenPath, err)
					}
				}
				golden, err := os.ReadFile(goldenPath)
				if err != nil {
					t.Fatalf("read golden %s (run with -update to write it): %v", fixture.Lowered, err)
				}
				if !bytes.Equal(golden, encoded) {
					t.Fatalf("lowered bytes differ from %s; run go test -run TestCorpusCompositions -update", fixture.Lowered)
				}
				roundTrip, err := DecodeLayers(golden)
				if err != nil {
					t.Fatalf("decode golden layers: %v", err)
				}
				if len(roundTrip.Layers) != len(lowered.Layers) {
					t.Fatalf("decoded layers = %d, want %d", len(roundTrip.Layers), len(lowered.Layers))
				}
			}
		})
	}
}

func decodeAndValidate(raw []byte, catalog *Catalog) error {
	composition, err := Decode(raw)
	if err != nil {
		return err
	}
	return composition.Validate(catalog)
}
