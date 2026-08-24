package render_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLegacyMediaAssetProcessorContractsStayRemoved(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve test file path")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))

	for _, legacyFile := range []string{
		"internal/media/mediaasset/adapter.go",
		"internal/media/mediaasset/types.go",
	} {
		path := filepath.Join(repoRoot, legacyFile)
		_, err := os.Stat(path)
		if err == nil {
			t.Fatalf("legacy processor file must not exist: %s", legacyFile)
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stat %s: %v", legacyFile, err)
		}
	}

	forbiddenSymbols := []string{
		"AssetInput",
		"AssetResult",
		"DownloadProcessUpload",
		"ToCoreProcessor",
		"type MediaProcessor interface",
	}

	productionFiles, err := filepath.Glob(filepath.Join(repoRoot, "internal", "media", "mediaasset", "*.go"))
	if err != nil {
		t.Fatalf("glob mediaasset package: %v", err)
	}
	productionFiles = append(productionFiles, filepath.Join(repoRoot, "internal", "app", "media_processor.go"))

	for _, path := range productionFiles {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		// Skip files that no longer exist (e.g. legacy media_processor.go
		// was eliminated in the core→domain migration).
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			continue
		}
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, symbol := range forbiddenSymbols {
			if strings.Contains(string(content), symbol) {
				rel, _ := filepath.Rel(repoRoot, path)
				t.Fatalf("legacy processor symbol %q found in %s", symbol, rel)
			}
		}
	}
}
