package mediamemory

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPackageNeverImportsQdrantInfrastructure pins the only legitimate Qdrant
// boundary: the capability owns the KeyframeVisualIndexer port, and the
// concrete adapter lives in internal/platform/qdrant/indexing/mediamemory,
// wired by the composition root (internal/app/wiring/build_bundles_domain_assets.go).
//
// If this package ever imports the Qdrant infrastructure directly, the port
// stops being a seam and removing Qdrant (or swapping the visual index) turns
// into a capability rewrite. Keep the dependency pointing at the port.
func TestPackageNeverImportsQdrantInfrastructure(t *testing.T) {
	t.Parallel()

	const forbidden = "internal/platform/qdrant"
	scanned := 0
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		scanned++
		if strings.Contains(string(src), forbidden) {
			t.Errorf("%s imports %s; mediamemory must depend on its own port and let the composition root wire the adapter", path, forbidden)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk mediamemory package: %v", err)
	}
	if scanned == 0 {
		t.Fatal("scanned no production Go files; the guard would pass vacuously")
	}
}
