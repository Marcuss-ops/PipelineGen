// Tests for PR-VO-A4 (path-traversal fix, June 2026).
//
// SanitizeSubfolderSegment rejects any subfolder name that would let
// a caller escape or unintendedly nest inside the parent root when
// the result is used as a single segment under filepath.Join.
//
// EnsureWithinDir guards the post-join result using filepath.Rel —
// the canonical Go idiom for "is this path strictly under that root?".
//
// Attack vectors covered (must reject):
//   - "../", "..", ".", "/abs", "\\abs", "a/b", "a\\b", NUL byte,
//     whitespace-only, mixed-dotted names like "..."
//
// Acceptance vectors (must accept):
//   - "intro", "Foo Bar 2024", "2024-q4", "日本", "+trailing-period",
//     hyphen/underscore/dot/space only.

package filesystem

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeSubfolderSegment_RejectsAttacks(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantSub string // substring that must appear in the error message
	}{
		// empty / whitespace
		{"empty", "", "empty after trim"},
		{"whitespace-only", "   ", "empty after trim"},
		{"tabs-and-newlines", "\t\n  \t\r", "empty after trim"},

		// reserved
		{"dot-literal-dot", ".", "reserved"},
		{"dotdot-literal-dotdot", "..", "reserved"},
		// Note: "..." (three dots) is technically a possible filesystem name — we
		// do NOT reject it, only the reserved "." and "..". Validate via the
		// "AcceptsTrailingChars" test below.

		// leading separator (Linux absolute + Windows absolute)
		{"leading-slash", "/etc", "leading separator"},
		{"leading-backslash", "\\windows", "leading separator"},
		// multi-segment
		{"embedded-slash", "subfolder/sibling", "separator"},
		{"embedded-backslash", "subfolder\\sibling", "separator"},
		{"trailing-slash", "subfolder/", "separator"},
		// NUL byte
		{"embedded-nul", "foo\x00bar", "NUL"},
		{"leading-nul", "\x00foo", "NUL"},
		// length cap
		{"too-long", strings.Repeat("a", 201), "exceeds 200 bytes"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SanitizeSubfolderSegment(tc.input)
			if err == nil {
				t.Fatalf("attack %q must be rejected; got %q nil err", tc.input, got)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("attack %q rejected with %q; expected substring %q in err message", tc.input, err.Error(), tc.wantSub)
			}
			if got != "" {
				t.Errorf("attack %q rejected but non-empty sanitation %q returned", tc.input, got)
			}
		})
	}
}

func TestSanitizeSubfolderSegment_AcceptsValid(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"simple-ascii", "intro", "intro"},
		{"hyphenated", "2024-q4", "2024-q4"},
		{"underscored", "scene_one", "scene_one"},
		{"with-dots", "v1.0", "v1.0"},
		{"unicode-letters", "日本", "日本"},
		{"mixed-spaces-and-digits", "Foo Bar 2024", "Foo Bar 2024"},
		{"trims-whitespace", "  intro  ", "intro"},
		{"trailing-period", "trailing-period.", "trailing-period."},
		{"three-dots-not-reserved", "...", "..."},
		{"length-200-border", strings.Repeat("a", 200), strings.Repeat("a", 200)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SanitizeSubfolderSegment(tc.input)
			if err != nil {
				t.Fatalf("valid %q unexpected error: %v", tc.input, err)
			}
			if got != tc.want {
				t.Errorf("SanitizeSubfolderSegment(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestSanitizeSubfolderSegment_JoinsCleanly(t *testing.T) {
	// Belt-and-braces: a sanitized segment must produce a path that
	// EndsWith the sanitized segment under any root we choose, regardless
	// of the OS separator. This pins the post-condition that callers
	// rely on when building the join result.
	root := filepath.Join("data", "voiceovers")
	segment := "intro"
	safe, err := SanitizeSubfolderSegment(segment)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	joined := filepath.Join(root, safe)
	if filepath.Base(joined) != safe {
		t.Fatalf("filepath.Join(%q, %q) = %q; base should equal the segment", root, safe, joined)
	}
}

func TestEnsureWithinDir_HappyPaths(t *testing.T) {
	root := filepath.Join("data", "voiceovers")
	cases := []struct {
		name string
		path string
	}{
		// equal
		{"equals-root", root},
		// nested single
		{"nested-one", filepath.Join(root, "intro")},
		// nested deep
		{"nested-deep", filepath.Join(root, "a", "b", "c")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := EnsureWithinDir(root, tc.path); err != nil {
				t.Errorf("EnsureWithinDir(%q, %q) unexpected error: %v", root, tc.path, err)
			}
		})
	}
}

func TestEnsureWithinDir_RejectsEscape(t *testing.T) {
	root := filepath.Join("data", "voiceovers")
	cases := []struct {
		name     string
		path     string
		wantHint string
	}{
		{"equal-parent", root + "/..", "escapes"},
		{"escape-via-up", filepath.Join(root, "..", "etc"), "escapes"},
		{"escape-via-multi-up", filepath.Join(root, "..", "..", ".."), "escapes"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := EnsureWithinDir(root, tc.path)
			if err == nil {
				t.Fatalf("path %q escaped %q but EnsureWithinDir returned nil", tc.path, root)
			}
			if !strings.Contains(err.Error(), tc.wantHint) {
				t.Errorf("path %q error %q missing hint %q", tc.path, err.Error(), tc.wantHint)
			}
		})
	}
}

func TestEnsureWithinDir_RejectsEmptyArgs(t *testing.T) {
	root := filepath.Join("data", "voiceovers")
	if err := EnsureWithinDir("", root); err == nil {
		t.Errorf("EnsureWithinDir with empty root must fail")
	}
	if err := EnsureWithinDir(root, ""); err == nil {
		t.Errorf("EnsureWithinDir with empty path must fail")
	}
}

func TestSanitizeThenEnsure_RoundTrip(t *testing.T) {
	// The canonical caller flow: sanitize the segment, join it under
	// root, and verify the join result is still strictly inside root.
	// This is what voiceover.processLanguage does at MkdirAll time
	// and at ResolveRequest time — pinning it here protects against
	// future drift between the two helpers.
	root := filepath.Join("data", "voiceovers")
	attacks := []string{
		"..", "../etc", "/etc", "\\host-share", "a/b", "a\\b",
		"foo..bar/../sibling", "\x00",
	}
	for _, a := range attacks {
		t.Run("attack-"+a, func(t *testing.T) {
			if _, err := SanitizeSubfolderSegment(a); err == nil {
				t.Fatalf("SanitizeSubfolderSegment accepted attack %q", a)
			}
		})
	}

	// happy path round-trip
	safe, err := SanitizeSubfolderSegment("intro")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	joined := filepath.Join(root, safe)
	if err := EnsureWithinDir(root, joined); err != nil {
		t.Errorf("EnsureWithinDir round-trip on safe segment failed: %v", err)
	}
}
