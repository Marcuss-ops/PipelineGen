package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestExtractGoPathTokens_NarrativeEnglishSkipped asserts the
// false-positive guard: lines that look like English prose are NOT
// matched unless they actually start with one of the canonical
// Go-path prefixes.
func TestExtractGoPathTokens_NarrativeEnglishSkipped(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "single canonical internal path",
			in:   "internal/platform/qdrant/dr",
			want: []string{"internal/platform/qdrant/dr"},
		},
		{
			name: "single canonical pkg path",
			in:   "pkg/defaults",
			want: []string{"pkg/defaults"},
		},
		{
			name: "single canonical cmd path",
			in:   "cmd/server",
			want: []string{"cmd/server"},
		},
		{
			name: "internal .go file path",
			in:   "internal/foo/bar.go",
			want: []string{"internal/foo/bar.go"},
		},
		{
			name: "pkg .go file path",
			in:   "pkg/defaults/script.go",
			want: []string{"pkg/defaults/script.go"},
		},
		{
			name: "ignored prose (no Go prefix)",
			in:   "the algorithm was wrong on Tuesday",
			want: nil,
		},
		{
			name: "ignored near-match 'algorithm'",
			in:   "the Algorithmic complexity came up",
			want: nil,
		},
		{
			name: "token with trailing punct trimmed",
			in:   "see internal/foo/bar.go for context.",
			want: []string{"internal/foo/bar.go"},
		},
		{
			name: "list-style three paths, deduped",
			in:   "internal/a, internal/b, internal/a",
			want: []string{"internal/a", "internal/b"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := extractGoPathTokens(c.in)
			if !equalStringSlices(got, c.want) {
				t.Errorf("extractGoPathTokens(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// TestScanYAMLLeafScalars_SkipsComments asserts that commented-out
// leaf scalars are NOT extracted (zero-legacy rule per AGENTS.md).
func TestScanYAMLLeafScalars_SkipsComments(t *testing.T) {
	const yaml = `# full-line comment
# also a comment

key1: pkg/defaults
key2: cmd/server
# key3: pkg/hidden  (commented-out; should NOT be picked up)

list:
  - internal/foo
  - internal/bar

exit_gate: |-
  multi-line body
  across two lines
`
	tmp, err := os.CreateTemp("", "symbol_refs_*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(yaml); err != nil {
		t.Fatal(err)
	}
	tmp.Close()

	leaves, err := scanYAMLLeafScalars(tmp.Name())
	if err != nil {
		t.Fatal(err)
	}

	// We expect: pkg/defaults, cmd/server, internal/foo, internal/bar,
	// plus one block-scalar body leaf (joined multi-line). At least 5.
	if len(leaves) < 5 {
		t.Fatalf("want >=5 leaves, got %d (%+v)", len(leaves), leaves)
	}

	// Verify pkg/hidden (commented-out) NOT present.
	for _, leaf := range leaves {
		if strings.Contains(leaf.value, "pkg/hidden") {
			t.Errorf("commented-out key3 should be excluded, got leaf=%+v", leaf)
		}
	}
	// Verify multi-line block scalar body is captured as one leaf.
	foundBlock := false
	for _, leaf := range leaves {
		if strings.Contains(leaf.value, "multi-line body") &&
			strings.Contains(leaf.value, "across two lines") {
			foundBlock = true
		}
	}
	if !foundBlock {
		t.Errorf("block scalar body should be captured as one leaf, got %+v", leaves)
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestScanYAMLScopedLeafScalars_FiltersToParentKeys asserts that
// only leaves whose ancestor chain includes `linked_issues` or
// `blocker` are emitted by the slice-4/4 scoped walker. Narratives
// under `description:`, `exit_gate:`, and unrelated top-level keys
// are NOT emitted so Go-path mentions in prose do not become
// false-positive violations. The test mirrors the user-spec shape:
//
//   - `linked_issues:` list items with sub-fields (id, owner_capability,
//     status, deadline) are in scope.
//   - `blocker:` list items are in scope.
//   - block-scalar bodies under `linked_issues:` are in scope; bodies
//     under `exit_gate:` (or any other ancestry) are filtered out.
func TestScanYAMLScopedLeafScalars_FiltersToParentKeys(t *testing.T) {
	const yaml = `
top_key: "internal/services/pkg_a"

linked_issues:
  - id: PR-A-ONE
    owner_capability: internal/capabilities/voiceover
    status: pending
    deadline: 2026-07-10
  - id: PR-B-TWO
    owner_capability: "internal/application/voiceover"

blocker:
  - "16"

narrative_under_unrelated_key:
  internal/not_in_scope: yes
  exit_gate: |
    this prose mentions internal/application/voiceover but should be
    filtered out by the parent-key scope filter.
`
	tmp, err := os.CreateTemp("", "symbol_refs_scoped_*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(yaml); err != nil {
		t.Fatal(err)
	}
	tmp.Close()

	leaves, err := scanYAMLScopedLeafScalars(tmp.Name(), ScopedParentKeys)
	if err != nil {
		t.Fatal(err)
	}

	var got []string
	for _, leaf := range leaves {
		got = append(got, leaf.value)
	}

	mustContain := []string{
		"PR-A-ONE",
		"internal/capabilities/voiceover",
		"internal/application/voiceover",
		"pending",
		"16",
	}
	for _, want := range mustContain {
		if !sliceContainsValue(got, want) {
			t.Errorf("expected leaf value %q in scoped output, got %v", want, got)
		}
	}

	mustNotContain := []string{
		"internal/not_in_scope",
		"internal/services/pkg_a",
		"this prose mentions internal/application/voiceover",
	}
	for _, ban := range mustNotContain {
		if sliceContainsValue(got, ban) {
			t.Errorf("did not expect leaf value %q in scoped output (parent-key filter failed), got %v", ban, got)
		}
	}
}

// TestCollectYAMLFiles_Slice44Scope asserts that the slice-4/4
// collector returns ONLY architecture/current.yaml and
// architecture/issues.yaml (the two files named in the user spec),
// even when other yaml surfaces (policy.yaml, deprecations.yaml,
// ownership.generated.yaml, archive/...) exist on disk. This
// prevents the broader pre-scope scan from re-emerging during a
// future refactor.
func TestCollectYAMLFiles_Slice44Scope(t *testing.T) {
	tmp, err := os.MkdirTemp("", "symbol_refs_col_*.d")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	// Create one in-scope file + several out-of-scope files to
	// confirm the collector drops the out-of-scope ones.
	mustWrite := func(p string) {
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.WriteString("key: internal/anything\n")
		_ = f.Close()
	}
	mustWrite(filepath.Join(tmp, "current.yaml"))
	mustWrite(filepath.Join(tmp, "issues.yaml"))
	mustWrite(filepath.Join(tmp, "policy.yaml"))
	mustWrite(filepath.Join(tmp, "deprecations.yaml"))
	mustWrite(filepath.Join(tmp, "ownership.generated.yaml"))
	if err := os.MkdirAll(filepath.Join(tmp, "archive", "2026-06-29"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(filepath.Join(tmp, "archive", "2026-06-29", "current-snapshot-2026-06-29.yaml"))

	got, err := collectYAMLFiles(tmp)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		filepath.Join(tmp, "current.yaml"),
		filepath.Join(tmp, "issues.yaml"),
	}
	if !equalStringSlices(got, want) {
		t.Errorf("collectYAMLFiles returned %v, want exactly %v (slice 4/4 scope contract violated)", got, want)
	}
}

func sliceContainsValue(s []string, e string) bool {
	for _, v := range s {
		if v == e {
			return true
		}
	}
	return false
}
