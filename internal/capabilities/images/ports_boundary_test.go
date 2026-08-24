package images

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplicationImagesProductionHasNoConcreteSemanticOrHTTPImports(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(".", entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		source := string(data)
		for _, forbidden := range []string{
			"internal/infrastructure/ai/semantic",
			"internal/infrastructure/httpclient",
		} {
			if strings.Contains(source, forbidden) {
				t.Errorf("%s imports forbidden concrete dependency %q", path, forbidden)
			}
		}
	}
}
