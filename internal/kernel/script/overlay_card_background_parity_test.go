package script

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
)

// The two Go modules cannot share a constant, so the closed payload vocabulary
// and the renderer's recipe map are pinned here. Without this, a renderer
// rename would keep validating in script.generate and fail only at render.
func TestCardBackgroundMotionsMatchRendererCatalog(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test source")
	}
	root := filepath.Join(filepath.Dir(source), "..", "..", "..", "..")
	const rel = "RenderingGen/renderinggen/internal/overlay/text_plate_motion.go"
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Skipf("RenderingGen plate catalog %s not available: %v", rel, err)
	}
	block := regexp.MustCompile(`(?s)var plateMotionRecipes = map\[string\]plateMotionRecipe\{(.*?)\n\}`).FindStringSubmatch(string(raw))
	if block == nil {
		t.Fatalf("cannot parse plateMotionRecipes out of %s; the catalog shape changed", rel)
	}
	entries := regexp.MustCompile(`(?m)^\s*"([^"]+)":\s*plate`).FindAllStringSubmatch(block[1], -1)
	if len(entries) == 0 {
		t.Fatalf("parsed an empty plate catalog from %s; refusing to assert against nothing", rel)
	}
	renderer := map[string]bool{}
	for _, entry := range entries {
		renderer[entry[1]] = true
	}
	payload := map[string]bool{}
	for _, motion := range CertifiedCardBackgroundMotions {
		payload[motion] = true
	}
	for motion := range renderer {
		if !payload[motion] {
			t.Errorf("renderer plate motion %q is not certified in the payload vocabulary", motion)
		}
	}
	for motion := range payload {
		if !renderer[motion] {
			t.Errorf("payload plate motion %q has no renderer recipe", motion)
		}
	}
}
