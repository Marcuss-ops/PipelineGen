// Package scan — companion test for percheck_overlay_catalog_parity.go.
//
// Pins:
//
//	(a) clean fixture — the three-copy contract holds, no violation.
//	(b) "preset drift" — AUTHOR.overlay_presets differing from EMITTED emits
//	    catalog_presets_drift.
//	(c) "embedded stale" — EMBEDDED differing from EMITTED emits
//	    catalog_embedded_stale.
//	(d) "motion vanished" — an authored motion id absent from EMITTED emits
//	    catalog_motion_vanished (a SUBSET is allowed, a disappearance is not).
//	(e) "missing copy" — one present copy with the others absent emits
//	    catalog_copy_missing (never a silent pass).
//	(f) "no sibling checkout" — zero copies present is a no-op.
//	(g) "unparseable copy" — malformed JSON fails closed.
//	(h) the REAL repository tree is clean under this gate.
package boundaries

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

func scanOverlayCatalogParity(t *testing.T, root string) *report.Report {
	t.Helper()
	r := &report.Report{}
	ScanOverlayCatalogParity(root, &policy.Policy{}, r)
	return r
}

func overlayCatalogParityViolations(r *report.Report) []report.Violation {
	var out []report.Violation
	for _, v := range r.Violations {
		if v.Rule == overlayCatalogParityRule {
			out = append(out, v)
		}
	}
	return out
}

func writeCatalogDoc(t *testing.T, path string, doc map[string]any) {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

// catalogFixture is a synthetic two-repo checkout.
type catalogFixture struct {
	root     string
	author   string
	emitted  string
	embedded string
}

// newCatalogFixture writes a clean three-copy catalog: the author document
// owns two presets and one motion, the emitted copy enriches `motions` with a
// compiled-only id (allowed), and the embedded copy is identical to emitted.
func newCatalogFixture(t *testing.T) catalogFixture {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "refactored")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	copies := overlayCatalogCopies(root)
	fx := catalogFixture{root: root, author: copies[0].path, emitted: copies[1].path, embedded: copies[2].path}
	authorDoc := map[string]any{
		"schema_version":  "v1",
		"overlay_presets": []any{"phrase_default", "image_fade_in"},
		"motions":         []any{map[string]any{"id": "text_fade_up"}},
	}
	emittedDoc := map[string]any{
		"schema_version":  "v1",
		"overlay_presets": []any{"phrase_default", "image_fade_in"},
		"motions": []any{
			map[string]any{"id": "text_fade_up"},
			map[string]any{"id": "apple_compiled_only"},
		},
	}
	writeCatalogDoc(t, fx.author, authorDoc)
	writeCatalogDoc(t, fx.emitted, emittedDoc)
	writeCatalogDoc(t, fx.embedded, emittedDoc)
	return fx
}

func readCatalogDoc(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestOverlayCatalogParityCleanFixture(t *testing.T) {
	fx := newCatalogFixture(t)
	r := scanOverlayCatalogParity(t, fx.root)
	if got := overlayCatalogParityViolations(r); len(got) != 0 {
		t.Fatalf("clean fixture must pass, got %+v", got)
	}
}

func TestOverlayCatalogParityPresetDriftTrips(t *testing.T) {
	fx := newCatalogFixture(t)
	doc := readCatalogDoc(t, fx.emitted)
	doc["overlay_presets"] = []any{"phrase_default", "image_fade_in", "ghost_preset"}
	writeCatalogDoc(t, fx.emitted, doc)
	// Keep embedded identical so ONLY the author↔emitted rule can fire.
	writeCatalogDoc(t, fx.embedded, doc)
	r := scanOverlayCatalogParity(t, fx.root)
	got := overlayCatalogParityViolations(r)
	if len(got) != 1 || got[0].MatchedRule != "catalog_presets_drift" {
		t.Fatalf("preset drift must emit catalog_presets_drift, got %+v", got)
	}
}

func TestOverlayCatalogParityEmbeddedStaleTrips(t *testing.T) {
	fx := newCatalogFixture(t)
	doc := readCatalogDoc(t, fx.embedded)
	doc["overlay_presets"] = []any{"phrase_default"}
	writeCatalogDoc(t, fx.embedded, doc)
	r := scanOverlayCatalogParity(t, fx.root)
	got := overlayCatalogParityViolations(r)
	if len(got) != 1 || got[0].MatchedRule != "catalog_embedded_stale" {
		t.Fatalf("stale embedded copy must emit catalog_embedded_stale, got %+v", got)
	}
}

func TestOverlayCatalogParityMotionVanishedTrips(t *testing.T) {
	fx := newCatalogFixture(t)
	doc := readCatalogDoc(t, fx.emitted)
	// Drop the authored motion from the emitted copy; the enrichment stays.
	doc["motions"] = []any{map[string]any{"id": "apple_compiled_only"}}
	writeCatalogDoc(t, fx.emitted, doc)
	writeCatalogDoc(t, fx.embedded, doc)
	r := scanOverlayCatalogParity(t, fx.root)
	got := overlayCatalogParityViolations(r)
	if len(got) != 1 || got[0].MatchedRule != "catalog_motion_vanished" {
		t.Fatalf("a vanished authored motion must emit catalog_motion_vanished, got %+v", got)
	}
}

func TestOverlayCatalogParityMissingCopyTrips(t *testing.T) {
	fx := newCatalogFixture(t)
	for _, path := range []string{fx.emitted, fx.embedded} {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	r := scanOverlayCatalogParity(t, fx.root)
	got := overlayCatalogParityViolations(r)
	if len(got) != 1 || got[0].MatchedRule != "catalog_copy_missing" {
		t.Fatalf("one present copy with two missing must emit catalog_copy_missing, got %+v", got)
	}
}

func TestOverlayCatalogParityNoSiblingIsNoop(t *testing.T) {
	root := t.TempDir()
	r := scanOverlayCatalogParity(t, root)
	if got := overlayCatalogParityViolations(r); len(got) != 0 {
		t.Fatalf("a bare PipelineGen checkout must be a no-op, got %+v", got)
	}
}

func TestOverlayCatalogParityUnparseableTrips(t *testing.T) {
	fx := newCatalogFixture(t)
	if err := os.WriteFile(fx.author, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := scanOverlayCatalogParity(t, fx.root)
	got := overlayCatalogParityViolations(r)
	if len(got) != 1 || got[0].MatchedRule != "catalog_unparseable" {
		t.Fatalf("malformed catalog must fail closed, got %+v", got)
	}
}

func TestOverlayCatalogParityRealTreeIsClean(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	r := scanOverlayCatalogParity(t, root)
	if got := overlayCatalogParityViolations(r); len(got) != 0 {
		t.Fatalf("the real repository catalogs must satisfy the three-copy contract, got %+v", got)
	}
}
