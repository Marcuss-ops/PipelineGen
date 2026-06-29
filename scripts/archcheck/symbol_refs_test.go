package main

import (
	"os"
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
			in:   "internal/application/qdrant/dr",
			want: []string{"internal/application/qdrant/dr"},
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
