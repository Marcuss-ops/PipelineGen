package backfill

import (
	"strings"
	"testing"
)

// The CLI help and the operations runbook document `--source youtube`; the
// parser used to accept only `--source=youtube`, so the documented command
// failed with `unknown flag: --source`. Both spellings are now supported and
// pinned here.

func TestParseTextTracksBackfillArgs_EqualsForm(t *testing.T) {
	deps, err := parseTextTracksBackfillArgs([]string{
		"--source=youtube",
		"--languages=it,en,de",
		"--asset-ids=asset-a,asset-b",
		"--limit=5",
		"--progress=10",
		"--checkpoint=/tmp/cp.json",
		"--all",
		"--apply",
		"--json",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if deps.Source != "youtube" || deps.Languages != "it,en,de" || deps.AssetIDs != "asset-a,asset-b" {
		t.Fatalf("value flags not parsed: %+v", deps)
	}
	if deps.Limit != 5 || deps.Progress != 10 || deps.Checkpoint != "/tmp/cp.json" {
		t.Fatalf("numeric/checkpoint flags not parsed: %+v", deps)
	}
	if !deps.Apply || !deps.JSON || deps.OnlyMissing {
		t.Fatalf("boolean flags not parsed: %+v", deps)
	}
}

func TestParseTextTracksBackfillArgs_SpaceForm(t *testing.T) {
	deps, err := parseTextTracksBackfillArgs([]string{
		"--source", "youtube",
		"--languages", "it,en,de",
		"--text-kind", "transcript",
		"--asset-ids", "asset-a,asset-b",
		"--limit", "5",
		"--progress", "10",
		"--checkpoint", "/tmp/cp.json",
		"--all",
		"--apply",
		"--json",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if deps.Source != "youtube" || deps.Languages != "it,en,de" || deps.AssetIDs != "asset-a,asset-b" {
		t.Fatalf("value flags not parsed: %+v", deps)
	}
	if deps.Limit != 5 || deps.Progress != 10 || deps.Checkpoint != "/tmp/cp.json" {
		t.Fatalf("numeric/checkpoint flags not parsed: %+v", deps)
	}
	if !deps.Apply || !deps.JSON || deps.OnlyMissing {
		t.Fatalf("boolean flags not parsed: %+v", deps)
	}
}

func TestParseTextTracksBackfillArgs_MixedForms(t *testing.T) {
	deps, err := parseTextTracksBackfillArgs([]string{
		"--source=youtube",
		"--languages", "it,en",
		"--asset-ids", "asset-a",
		"--apply",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if deps.Source != "youtube" || deps.Languages != "it,en" || deps.AssetIDs != "asset-a" {
		t.Fatalf("mixed forms not parsed: %+v", deps)
	}
}

func TestParseTextTracksBackfillArgs_MissingValueFailsClosed(t *testing.T) {
	for _, args := range [][]string{
		{"--source"},
		{"--source", "--languages", "it,en"},
		{"--languages"},
		{"--limit"},
		{"--progress", "--apply"},
	} {
		if _, err := parseTextTracksBackfillArgs(args); err == nil {
			t.Fatalf("args %v: expected missing-value error", args)
		} else if !strings.Contains(err.Error(), "requires a value") {
			t.Fatalf("args %v: error = %v, want requires a value", args, err)
		}
	}
}

func TestParseTextTracksBackfillArgs_InvalidNumbersFailClosed(t *testing.T) {
	if _, err := parseTextTracksBackfillArgs([]string{"--source", "youtube", "--languages", "it", "--limit", "abc"}); err == nil {
		t.Fatal("expected invalid --limit error")
	}
	if _, err := parseTextTracksBackfillArgs([]string{"--source", "youtube", "--languages", "it", "--progress", "-1"}); err == nil {
		t.Fatal("expected invalid --progress error")
	}
}

func TestParseTextTracksBackfillArgs_UnknownFlagAndConflicts(t *testing.T) {
	if _, err := parseTextTracksBackfillArgs([]string{"--source", "youtube", "--languages", "it", "--nope"}); err == nil {
		t.Fatal("expected unknown flag error")
	}
	if _, err := parseTextTracksBackfillArgs([]string{"--source", "youtube", "--languages", "it", "--apply", "--dry-run"}); err == nil {
		t.Fatal("expected apply/dry-run conflict")
	}
}

func TestParseTextTracksBackfillArgs_Defaults(t *testing.T) {
	deps, err := parseTextTracksBackfillArgs([]string{"--source", "youtube", "--languages", "it,en"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if deps.Progress != 50 {
		t.Fatalf("default progress = %d, want 50", deps.Progress)
	}
	if deps.TextKind != "transcript" {
		t.Fatalf("default text-kind = %q, want transcript", deps.TextKind)
	}
	if deps.Apply || deps.DryRun {
		t.Fatalf("apply/dry-run must default to false: %+v", deps)
	}
}
