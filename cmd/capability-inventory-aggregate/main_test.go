package main

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestAggregateAppendsRepeatedLists(t *testing.T) {
	dir := t.TempDir()
	writeInventoryFixture(t, filepath.Join(dir, "a.yaml"), "version: 1\napplication_capabilities:\n  - name: one\n")
	writeInventoryFixture(t, filepath.Join(dir, "b.yaml"), "application_capabilities:\n  - name: two\n")
	manifestPath := filepath.Join(dir, "manifest.yaml")
	writeInventoryFixture(t, manifestPath, "sections:\n  - a.yaml\n  - b.yaml\n")

	data, err := aggregate(manifestPath)
	if err != nil {
		t.Fatalf("aggregate() error = %v", err)
	}
	var got struct {
		Version      int `yaml:"version"`
		Capabilities []struct {
			Name string `yaml:"name"`
		} `yaml:"application_capabilities"`
	}
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatalf("yaml.Unmarshal() error = %v", err)
	}
	if got.Version != 1 || len(got.Capabilities) != 2 || got.Capabilities[0].Name != "one" || got.Capabilities[1].Name != "two" {
		t.Fatalf("aggregate result = %#v", got)
	}
}

func TestAggregateRejectsScalarConflict(t *testing.T) {
	dir := t.TempDir()
	writeInventoryFixture(t, filepath.Join(dir, "a.yaml"), "version: 1\n")
	writeInventoryFixture(t, filepath.Join(dir, "b.yaml"), "version: 2\n")
	manifestPath := filepath.Join(dir, "manifest.yaml")
	writeInventoryFixture(t, manifestPath, "sections:\n  - a.yaml\n  - b.yaml\n")

	if _, err := aggregate(manifestPath); err == nil {
		t.Fatal("aggregate() error = nil, want conflict")
	}
}

func writeInventoryFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%s): %v", path, err)
	}
}
