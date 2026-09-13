package wiring

import (
	"os"
	"strings"
	"testing"
)

func TestBuildImagesServiceDoesNotWireChromeImageGeneration(t *testing.T) {
	source, err := os.ReadFile("build_bundles_core.go")
	if err != nil {
		t.Fatalf("read build_bundles_core.go: %v", err)
	}
	text := string(source)
	for _, forbidden := range []string{
		`chromeimages "github.com/Marcuss-ops/PipelineGen/internal/platform/images/chrome"`,
		"chromeimages.NewChromeImageProviderPoolFromProfile(",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("build_bundles_core.go still wires retired Chrome image generation %q", forbidden)
		}
	}
	if !strings.Contains(text, "ImageGen: nil") {
		t.Fatal("composition root must leave AI/Chrome image generation unwired")
	}
}
