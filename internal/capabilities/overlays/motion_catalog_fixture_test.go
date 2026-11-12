package overlays

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
)

// canonicalMotionCatalogFixture is the catalog snapshot checked into this
// repository so the parity tests do not depend on a sibling RenderingGen
// checkout. The sibling copy is still consulted when present: any drift fails
// the test instead of silently validating a stale vocabulary.
const canonicalMotionCatalogFixture = "testdata/chronontemplate_catalog.v1.json"

func loadCanonicalMotionCatalog(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(canonicalMotionCatalogFixture)
	if err != nil {
		t.Fatalf("read vendored canonical motion catalog: %v", err)
	}

	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for dir := root; ; dir = filepath.Dir(dir) {
		candidate := filepath.Join(dir, "RenderingGen", "renderinggen", "internal", "motion", "catalog", "chronontemplate_catalog.v1.json")
		if upstream, readErr := os.ReadFile(candidate); readErr == nil {
			if sha256.Sum256(upstream) != sha256.Sum256(data) {
				t.Fatalf("vendored canonical motion catalog drifted from %s; refresh %s", candidate, canonicalMotionCatalogFixture)
			}
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	return data
}
