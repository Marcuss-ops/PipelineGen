package knobs

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Generated artifact paths, relative to the repository root. They are the
// example config and the runbook section that used to be maintained by hand
// (and drifted from the code): both are now rendered from the registry.
const (
	ConfigArtifact  = "config/velox-knobs.generated.yaml"
	RunbookArtifact = "docs/operations/VELOX-KNOBS.md"
)

const generatedHeader = "GENERATED FILE — DO NOT EDIT. Run `go run ./cmd/velox-knobs -write` after editing internal/app/knobs/registry.go."

// deploymentKnobs returns the rows that describe deployment configuration
// (markers and test harness variables are inventory-only).
func deploymentKnobs() []Knob {
	out := make([]Knob, 0, len(registry))
	for _, knob := range registry {
		if knob.Kind == KindEnv || knob.Kind == KindConfig {
			out = append(out, knob)
		}
	}
	return out
}

// RenderConfigYAML renders the example-config block. It is deliberately a
// hand-rolled (deterministic) document instead of a marshalled map: the order
// and the comments are part of the artifact an operator reads.
func RenderConfigYAML() string {
	var b strings.Builder
	b.WriteString("# " + generatedHeader + "\n")
	b.WriteString("#\n")
	b.WriteString("# Every VELOX_* knob the composition root reads or declares, with its\n")
	b.WriteString("# default and the value a production host should run. `kind: config`\n")
	b.WriteString("# means the value is bound through a config field env tag; `kind: env`\n")
	b.WriteString("# means the composition root (or the owning capability) reads the\n")
	b.WriteString("# environment directly. An unset variable uses `default`.\n\n")
	b.WriteString("knobs:\n")
	for _, knob := range deploymentKnobs() {
		b.WriteString("  " + knob.Name + ":\n")
		b.WriteString("    kind: " + string(knob.Kind) + "\n")
		b.WriteString("    default: " + yamlString(knob.Default) + "\n")
		b.WriteString("    safe_production: " + yamlString(knob.SafeProduction) + "\n")
		b.WriteString("    owner: " + yamlString(knob.Owner) + "\n")
		b.WriteString("    description: " + yamlString(knob.Description) + "\n")
	}
	return b.String()
}

// RenderRunbook renders the operator table (name, default, owner, safe value
// in production, description) as a Markdown document.
func RenderRunbook() string {
	var b strings.Builder
	b.WriteString("<!-- " + generatedHeader + " -->\n\n")
	b.WriteString("# VELOX_* knobs — registry\n\n")
	b.WriteString("Single source: `internal/app/knobs/registry.go`. The composition root reads\n")
	b.WriteString("these names through that package, logs every non-default value at boot, and\n")
	b.WriteString("a test fails when a `VELOX_*` name appears in `internal/app/wiring` without\n")
	b.WriteString("a registry entry.\n\n")
	b.WriteString("| Knob | Kind | Default | Owner | Safe in production | Description |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- |\n")
	for _, knob := range deploymentKnobs() {
		b.WriteString(fmt.Sprintf("| `%s` | %s | %s | `%s` | %s | %s |\n",
			knob.Name, knob.Kind, markdownCell(knob.Default), knob.Owner,
			markdownCell(knob.SafeProduction), markdownCell(knob.Description)))
	}
	b.WriteString("\n## Inventory-only names\n\n")
	b.WriteString("These look like knobs but are not deployment configuration; they are kept in\n")
	b.WriteString("the table so the inventory is complete.\n\n")
	b.WriteString("| Name | Kind | Owner | What it is |\n")
	b.WriteString("| --- | --- | --- | --- |\n")
	for _, knob := range registry {
		if knob.Kind != KindMarker && knob.Kind != KindTestOnly {
			continue
		}
		b.WriteString(fmt.Sprintf("| `%s` | %s | `%s` | %s |\n",
			knob.Name, knob.Kind, knob.Owner, markdownCell(knob.Description)))
	}
	return b.String()
}

// artifact pairs a generated path with its rendered content.
type artifact struct {
	path    string
	content string
}

func artifacts() []artifact {
	return []artifact{
		{path: ConfigArtifact, content: RenderConfigYAML()},
		{path: RunbookArtifact, content: RenderRunbook()},
	}
}

// WriteArtifacts (re)generates both artifacts under root.
func WriteArtifacts(root string) error {
	for _, item := range artifacts() {
		path := filepath.Join(root, item.path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("knob artifacts: create %s: %w", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(item.content), 0o644); err != nil {
			return fmt.Errorf("knob artifacts: write %s: %w", path, err)
		}
	}
	return nil
}

// CheckArtifacts verifies the committed artifacts match the registry. It
// returns a non-nil error listing the drifted files, so CI fails with the
// command that fixes it instead of a bare diff.
func CheckArtifacts(root string) error {
	var drifted []string
	for _, item := range artifacts() {
		path := filepath.Join(root, item.path)
		got, err := os.ReadFile(path)
		switch {
		case err != nil:
			drifted = append(drifted, item.path+" (missing)")
		case !bytes.Equal(got, []byte(item.content)):
			drifted = append(drifted, item.path)
		}
	}
	if len(drifted) == 0 {
		return nil
	}
	sort.Strings(drifted)
	return fmt.Errorf("knob artifacts are stale: %s (run `go run ./cmd/velox-knobs -write`)",
		strings.Join(drifted, ", "))
}

// yamlString quotes a value as a YAML double-quoted scalar.
func yamlString(value string) string {
	return strconv.Quote(value)
}

// markdownCell escapes the table separator and normalizes newlines.
func markdownCell(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	return strings.ReplaceAll(value, "\n", " ")
}
