package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadManifestMergesUniqueSections(t *testing.T) {
	dir := t.TempDir()
	writePromptFixture(t, filepath.Join(dir, "core.yaml"), "system:\n  base: base prompt\n")
	writePromptFixture(t, filepath.Join(dir, "filters.yaml"), "filtering:\n  stop_phrases: [stop]\n")
	manifest := filepath.Join(dir, "prompts.yaml")
	writePromptFixture(t, manifest, "includes:\n  - core.yaml\n  - filters.yaml\n")

	cfg, err := Load(manifest)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.System.Base != "base prompt" {
		t.Fatalf("System.Base = %q, want base prompt", cfg.System.Base)
	}
	if len(cfg.Filtering.StopPhrases) != 1 || cfg.Filtering.StopPhrases[0] != "stop" {
		t.Fatalf("Filtering.StopPhrases = %#v", cfg.Filtering.StopPhrases)
	}
}

func TestLoadManifestRejectsDuplicateTopLevelSection(t *testing.T) {
	dir := t.TempDir()
	writePromptFixture(t, filepath.Join(dir, "a.yaml"), "system:\n  base: first\n")
	writePromptFixture(t, filepath.Join(dir, "b.yaml"), "system:\n  base: second\n")
	manifest := filepath.Join(dir, "prompts.yaml")
	writePromptFixture(t, manifest, "includes:\n  - a.yaml\n  - b.yaml\n")

	_, err := Load(manifest)
	if err == nil || !strings.Contains(err.Error(), "duplicate prompt section") {
		t.Fatalf("Load() error = %v, want duplicate prompt section", err)
	}
}

func TestLoadManifestRejectsIncludeCycle(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.yaml")
	b := filepath.Join(dir, "b.yaml")
	writePromptFixture(t, a, "includes:\n  - b.yaml\n")
	writePromptFixture(t, b, "includes:\n  - a.yaml\n")

	_, err := Load(a)
	if err == nil || !strings.Contains(err.Error(), "include cycle") {
		t.Fatalf("Load() error = %v, want include cycle", err)
	}
}

func writePromptFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%s): %v", path, err)
	}
}
