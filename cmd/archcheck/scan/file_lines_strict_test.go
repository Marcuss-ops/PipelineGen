// Package scan — file-line-strict scanner tests.
//
// Black-box test for ScanFileLinesStrict:
//   - Synthetic .go fixtures laid down under <root>/internal/<rel>/
//     so ScanFileLinesStrict's fixed `internal/` + `cmd/` walk picks
//     them up exactly like real sources.
//   - Synthetic allowlist fixtures written alongside so the parser
//     edge cases (comments, blanks, missing-file) can be exercised
//     without polluting the archcheck command-line.
//
// Cross-references:
//   - cmd/archcheck/scan/file_lines_strict.go: the scanner under test
//   - cmd/archcheck/policy/model.go: Policy.MaxLinesPerFileStrict + MaxLinesStrictAllowlist
//   - docs/migrations/max-lines-strict-allowlist.txt: prod allowlist (15 files >600 LOC from the JSON baseline)
//   - architecture/policy.yaml: max_lines_per_file_strict + max_lines_strict_allowlist knobs
//
// Test scope:
//   - Cap=600. Five synthetic .go files covering under-cap / over-cap/
//     in-allowlist / not-in-allowlist / missing-allowlist / opt-out.
//   - Allowlist parser tests assert the comment + blank-skip rule
//     and the forward-slash normalization.
package scan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
)

// writeSizeFile lays down `<root>/internal/<rel>/<file>.go` with the
// supplied source body. The directory layout mirrors the real
// codebase so ScanFileLinesStrict's fixed `internal/` walk picks up
// the fixtures exactly like real sources.
func writeSizeFile(t *testing.T, root, rel, file, body string) {
	t.Helper()
	dir := filepath.Join(root, "internal", rel)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	target := filepath.Join(dir, file)
	if err := os.WriteFile(target, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", target, err)
	}
}

// buildBody produces a Go file body of exactly N meaningful newlines
// (counting any `\n` line break the scanner will see). count=599
// produces a sub-cap file; count=601 produces an over-cap file. The
// body is wrapped in package+one-type decls so it compiles if a
// developer ever runs `go build ./...` in the temp dir.
func buildBody(count int) string {
	var b strings.Builder
	b.WriteString("package synthpkg\n\ntype Anchor struct{}\n\n")
	// Each `pad` line is 1 newline; we add `count - 4` extra
	// (4 from the header lines including the trailing newline that
	// bufio.Scanner still counts as a line). So the final body has
	// exactly `count` newlines as Scan() returns them if count >= 4.
	extra := count - 4
	if extra < 0 {
		extra = 0
	}
	for i := 0; i < extra; i++ {
		b.WriteString("// padding line for size-cap test fixtures — not compiled in prod.\n")
	}
	return b.String()
}

// minimalStrictPolicy returns a Policy with only the file-line-strict
// knobs set; everything else is zero (all other scan functions would
// no-op but this test only invokes ScanFileLinesStrict).
func minimalStrictPolicy(max int, allowlist string) *policy.Policy {
	return &policy.Policy{
		MaxLinesPerFileStrict:   max,
		MaxLinesStrictAllowlist: allowlist,
	}
}

// TestScanFileLinesStrict_Cap600_Boundaries — user spec test.
//
// Cap=600. Fixtures at 599 (under), 601 in allowlist (exempt),
// 601 not in allowlist (violation), allowlist path empty (violation),
// opt-out (max=0, no violation regardless of count).
func TestScanFileLinesStrict_Cap600_Boundaries(t *testing.T) {
	root := t.TempDir()

	// 1) Under cap (599 lines). Should NOT violate.
	writeSizeFile(t, root, "under", "under.go", buildBody(599))

	// 2) Over cap (601 lines), NOT in allowlist. Should violate.
	writeSizeFile(t, root, "over_no_allowlist", "over_no_allowlist.go", buildBody(601))

	// 3) Over cap (601 lines), IN allowlist. Should NOT violate.
	writeSizeFile(t, root, "over_in_allowlist", "over_in_allowlist.go", buildBody(601))

	// Allowlist: only the over_in_allowlist.go path.
	allowlist := "docs/migrations/max-lines-strict-allowlist.txt"
	allowBody := "# this file is the test allowlist\n" +
		"\n" +
		"internal/over_in_allowlist/over_in_allowlist.go   # owner=@tests\n"
	if err := os.MkdirAll(filepath.Join(root, "docs", "migrations"), 0o755); err != nil {
		t.Fatalf("mkdir allowlist dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, allowlist), []byte(allowBody), 0o644); err != nil {
		t.Fatalf("write allowlist: %v", err)
	}

	pol := minimalStrictPolicy(600, allowlist)
	r := emptyReport(pol)
	ScanFileLinesStrict(root, pol, r)

	if got := len(r.Violations); got != 1 {
		t.Fatalf("want 1 violation (only over-cap-not-in-allowlist), got %d:\n%s",
			got, dumpViolations(r.Violations))
	}
	v := r.Violations[0]
	if v.MatchedRule != "max_lines_per_file_strict" {
		t.Errorf("MatchedRule = %q, want %q", v.MatchedRule, "max_lines_per_file_strict")
	}
	if v.Rule != "max_lines_per_file_strict" {
		t.Errorf("Rule = %q, want %q", v.Rule, "max_lines_per_file_strict")
	}
	if v.Severity != "warn" {
		t.Errorf("Severity = %q, want %q", v.Severity, "warn")
	}
	if v.MaxLines != 600 {
		t.Errorf("MaxLines = %d, want 600", v.MaxLines)
	}
	if v.ActualLines != 601 {
		t.Errorf("ActualLines = %d, want 601", v.ActualLines)
	}
	if !strings.HasSuffix(v.File, "over_no_allowlist.go") {
		t.Errorf("File = %q, want suffix over_no_allowlist.go", v.File)
	}
	if !strings.Contains(v.Note, "601") || !strings.Contains(v.Note, "max 600") {
		t.Errorf("Note = %q, want substring `601` and `max 600`", v.Note)
	}
}

// TestScanFileLinesStrict_EmptyAllowlistPath — when the policy
// declares a cap but the allowlist path is empty, every over-cap
// file emits a violation (no opt-out). Pins the fail-closed
// contract: empty string opts the rule OUT (no violations), but a
// declared cap with an empty allowlist path means "no exemptions
// granted yet".
func TestScanFileLinesStrict_NoAllowlistConfigured(t *testing.T) {
	root := t.TempDir()

	writeSizeFile(t, root, "over_no_list", "over_no_list.go", buildBody(605))

	pol := minimalStrictPolicy(600, "")
	r := emptyReport(pol)
	ScanFileLinesStrict(root, pol, r)

	if got := len(r.Violations); got != 1 {
		t.Fatalf("want 1 violation (over-cap + no allowlist), got %d: %s",
			got, dumpViolations(r.Violations))
	}
	if !strings.HasSuffix(r.Violations[0].File, "over_no_list.go") {
		t.Errorf("File = %q, want suffix over_no_list.go", r.Violations[0].File)
	}
}

// TestScanFileLinesStrict_OptOut — when MaxLinesPerFileStrict <= 0
// the scanner must be a no-op regardless of file sizes.
func TestScanFileLinesStrict_OptOut(t *testing.T) {
	root := t.TempDir()
	writeSizeFile(t, root, "big", "big.go", buildBody(2000))

	pol := minimalStrictPolicy(0, "")
	r := emptyReport(pol)
	ScanFileLinesStrict(root, pol, r)

	if got := len(r.Violations); got != 0 {
		t.Fatalf("MaxLinesPerFileStrict=0 must opt out; got %d violations", got)
	}
}

// TestScanFileLinesStrict_SkipsTestFiles — `_test.go` files MUST
// NOT participate in the over-cap count even when they exceed the
// cap by an absurd margin. Tests are fixtures, not production.
func TestScanFileLinesStrict_SkipsTestFiles(t *testing.T) {
	root := t.TempDir()

	// Over-cap production file → violation expected.
	writeSizeFile(t, root, "prod", "prod.go", buildBody(602))

	// Over-cap test file → MUST be skipped.
	writeSizeFile(t, root, "tests", "under_test.go", buildBody(9999))

	pol := minimalStrictPolicy(600, "")
	r := emptyReport(pol)
	ScanFileLinesStrict(root, pol, r)

	if got := len(r.Violations); got != 1 {
		t.Fatalf("only the production over-cap file should violate; got %d: %s",
			got, dumpViolations(r.Violations))
	}
	if !strings.HasSuffix(r.Violations[0].File, "prod.go") {
		t.Errorf("File = %q, want suffix prod.go", r.Violations[0].File)
	}
}

// TestLoadLineStrictAllowlist_Parser — verify the comment-and-blank
// skip rule plus multi-path support and forward-slash
// normalization. No assertion on scanner output, just on the
// in-memory set.
func TestLoadLineStrictAllowlist_Parser(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "allow.txt")
	body := "" +
		"# header comment — must be ignored\n" +
		"\n" +
		"# another comment\n" +
		"internal/foo/foo.go\n" +
		"  cmd/admin/long.go  \n" + // leading + trailing whitespace MUST be trimmed
		"internal/bar/bar.go# trailing inline hash (not a comment — treated as path)\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write allowlist: %v", err)
	}

	got := loadLineStrictAllowlist(root, "allow.txt")
	want := map[string]bool{
		"internal/foo/foo.go": true,
		"cmd/admin/long.go":   true,
		"internal/bar/bar.go# trailing inline hash (not a comment — treated as path)": true,
	}
	// Map-equality: walk want and assert each present in got.
	for k := range want {
		if !got[k] {
			t.Errorf("want allowlist to contain %q, got %v", k, got)
		}
	}
	if len(got) != len(want) {
		t.Errorf("allowlist size = %d, want %d (got=%v)", len(got), len(want), got)
	}
}

// TestLoadLineStrictAllowlist_MissingFile — when the allowlist path
// doesn't exist or isn't readable, the scanner MUST treat the
// allowlist as empty (fail-closed for the rule, fail-open for the
// file list — i.e. all over-cap files violate, none exempt).
func TestLoadLineStrictAllowlist_MissingFile(t *testing.T) {
	root := t.TempDir()
	got := loadLineStrictAllowlist(root, "missing/allowlist.txt")
	if len(got) != 0 {
		t.Errorf("missing allowlist must produce empty set; got %v", got)
	}
}
