package wiring

import (
	"os"
	"strings"
	"testing"
)

func TestBuildImagesServiceUsesChromeInfrastructureAdapter(t *testing.T) {
	source, err := os.ReadFile("build_bundles_core.go")
	if err != nil {
		t.Fatalf("read build_bundles_core.go: %v", err)
	}
	text := string(source)
	for _, required := range []string{
		`chromeimages "github.com/Marcuss-ops/PipelineGen/internal/platform/images/chrome"`,
		"chromeimages.NewChromeImageProviderPoolFromProfile(",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("build_bundles_core.go is missing canonical Chrome wiring %q", required)
		}
	}
	if strings.Contains(text, "imgservice.NewChromeImageProviderPoolFromProfile(") {
		t.Fatal("composition root still constructs Chrome provider through application/images")
	}
}
