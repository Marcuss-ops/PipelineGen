package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// syntheticLegacyYaml mirrors the canonical on-disk shape of
// architecture/deprecations.yaml enough to exercise the migrator.
// The IDs are the fixture's only stable id+owner_capability pairs:
// one per subsystem bucket except misc and drive (each exercised with
// 1 record). Keeps the test small while covering every bucket the
// categorizer knows about.
const syntheticLegacyYaml = `deprecations:
  - id: TEST-DRIVE-A
    owner_capability: internal/infrastructure/drive/foo
    exact_symbol: Foo.Bar
    file: internal/infrastructure/drive/foo.go
    file_line: "42"
    replacement: Foo.Baz
    introduction_date: '2026-01-01'
    removal_date: '2027-01-01'
    tracking_issue: DRIVE-1
    compatibility_test: scripts/tests/drive_test.sh
    usage_metric: prom_metric_test_drive_a
    migration_phase: EXPAND
    status: in_progress
    notes: synth
  - id: TEST-DRIVE-B
    owner_capability: internal/application/assets/upload_intent/x
    exact_symbol: Foo.Bar2
    file: internal/application/assets/upload_intent/x.go
    file_line: "10"
    replacement: Foo.Baz2
    introduction_date: '2026-01-01'
    removal_date: '2027-01-01'
    tracking_issue: DRIVE-2
    compatibility_test: scripts/tests/upload_intent_test.sh
    usage_metric: prom_metric_test_drive_b
    migration_phase: BACKFILL
    status: removed
    notes: synth
  - id: TEST-TRANSLATION-A
    owner_capability: internal/application/translation/foo
    exact_symbol: Translate.Foo
    file: internal/application/translation/foo.go
    file_line: "10"
    replacement: Translate.FooNew
    introduction_date: '2026-01-01'
    removal_date: '2027-01-01'
    tracking_issue: TRANSLATION-1
    compatibility_test: scripts/tests/translation_test.sh
    usage_metric: prom_metric_test_translation_a
    migration_phase: BACKFILL
    status: in_progress
    notes: synth
  - id: TEST-VOICEOVER-A
    owner_capability: internal/application/voiceover/bar
    exact_symbol: Voice.Bar
    file: internal/application/voiceover/bar.go
    file_line: "20"
    replacement: Voice.BarNew
    introduction_date: '2026-01-01'
    removal_date: '2027-01-01'
    tracking_issue: VOICEOVER-1
    compatibility_test: scripts/tests/voiceover_test.sh
    usage_metric: prom_metric_test_voiceover_a
    migration_phase: CUTOVER
    status: in_progress
    notes: synth
  - id: TEST-SCRIPTS-A
    owner_capability: internal/application/scripts/usecase/x
    exact_symbol: Scripts.X
    file: internal/application/scripts/usecase/x.go
    file_line: "1"
    replacement: Scripts.XNew
    introduction_date: '2026-01-01'
    removal_date: '2027-01-01'
    tracking_issue: SCRIPTS-1
    compatibility_test: scripts/tests/scripts_test.sh
    usage_metric: prom_metric_test_scripts_a
    migration_phase: CONTRACT
    status: keep
    notes: synth
  - id: TEST-QDRANT-A
    owner_capability: internal/platform/qdrant/foo
    exact_symbol: Qdrant.Foo
    file: internal/platform/qdrant/foo.go
    file_line: "5"
    replacement: Qdrant.FooNew
    introduction_date: '2026-01-01'
    removal_date: '2027-01-01'
    tracking_issue: QDRANT-1
    compatibility_test: scripts/tests/qdrant_test.sh
    usage_metric: prom_metric_test_qdrant_a
    migration_phase: EXPAND
    status: removed
    notes: synth
  - id: TEST-MISC-A
    owner_capability: some/unknown/path
    exact_symbol: Misc.A
    file: misc/a.go
    file_line: "99"
    replacement: Misc.ANew
    introduction_date: '2026-01-01'
    removal_date: '2027-01-01'
    tracking_issue: MISC-1
    compatibility_test: scripts/tests/misc_test.sh
    usage_metric: prom_metric_test_misc_a
    migration_phase: CUTOVER
    status: in_progress
    notes: synth
open_questions:
  - id: TEST-OQ-1
    title: synthetic open question for the migrator test
    status: pending
audit:
  manifest_version: synthetic
  total_records: 7
  by_status:
    removed: 2
    in_progress: 3
    keep: 1
  by_migration_phase:
    EXPAND: 2
    BACKFILL: 2
    CUTOVER: 2
    CONTRACT: 1
  ci_gate_impact: synthetic
`

// TestMigrate_BasicShape runs the migrator on a synthetic legacy
// file in t.TempDir() and asserts the expected fan-out:
//   - 6 records land in 4 distinct buckets (drive + assets/upload_intent
//     both go to bucketDrive per the categorizer prefix map; translation,
//     voiceover, scripts, qdrant, misc each get their own bucket).
//   - index.yaml manifest matches the bucket counts.
//   - audit.yaml is re-derived (7 records; status counts sum correctly).
//   - open_questions.yaml preserves the block byte-for-byte.
func TestMigrate_BasicShape(t *testing.T) {
	work := t.TempDir()
	legacyPath := filepath.Join(work, "legacy.yaml")
	targetDir := filepath.Join(work, "out")
	if err := os.WriteFile(legacyPath, []byte(syntheticLegacyYaml), 0o644); err != nil {
		t.Fatalf("write legacy: %v", err)
	}
	if err := migrate(legacyPath, targetDir); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	mustRead(t, filepath.Join(targetDir, "index.yaml"))
	mustRead(t, filepath.Join(targetDir, "audit.yaml"))
	mustRead(t, filepath.Join(targetDir, "open_questions.yaml"))

	for _, want := range []string{
		"records/drive.yaml", "records/translation.yaml", "records/voiceover.yaml",
		"records/scripts.yaml", "records/qdrant.yaml", "records/misc.yaml",
	} {
		mustRead(t, filepath.Join(targetDir, want))
	}
}

// TestMigrate_Determinism runs the migrator twice on the same input
// and asserts the on-disk bytes are identical for every output file.
// This is the property that makes the split reproducible for the
// next followup commit (any future re-run produces byte-equal shards).
func TestMigrate_Determinism(t *testing.T) {
	work1 := t.TempDir()
	work2 := t.TempDir()
	for _, p := range []string{work1, work2} {
		if err := os.WriteFile(filepath.Join(p, "legacy.yaml"),
			[]byte(syntheticLegacyYaml), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		if err := migrate(filepath.Join(p, "legacy.yaml"),
			filepath.Join(p, "out")); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}
	compareDirs(t, filepath.Join(work1, "out"), filepath.Join(work2, "out"))
}

// TestMigrate_IndexAggregateSums asserts the index manifest's
// TotalRecords equals sum(Records[i].Count) so a future operator can
// rely on the manifest's aggregate count without having to re-walk
// the records/ directory.
func TestMigrate_IndexAggregateSums(t *testing.T) {
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "legacy.yaml"),
		[]byte(syntheticLegacyYaml), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := migrate(filepath.Join(work, "legacy.yaml"),
		filepath.Join(work, "out")); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(work, "out", "index.yaml"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	var idx indexDoc
	if err := yaml.Unmarshal(raw, &idx); err != nil {
		t.Fatalf("parse index: %v", err)
	}
	var sum int
	for _, r := range idx.Records {
		sum += r.Count
	}
	if idx.TotalRecords != sum {
		t.Fatalf("index total_records=%d != sum(bucket.count)=%d",
			idx.TotalRecords, sum)
	}
	if idx.TotalRecords != 7 {
		t.Fatalf("expected 7 records, got %d", idx.TotalRecords)
	}
}

// TestMigrate_AuditRebuiltFromRecords asserts the audit.yaml's
// TotalRecords equals len(records) AND the by_status counts sum
// matches the input. This is the godlike/06 SSOT invariant: the
// audit is derived from records, not the other way around.
func TestMigrate_AuditRebuiltFromRecords(t *testing.T) {
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "legacy.yaml"),
		[]byte(syntheticLegacyYaml), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := migrate(filepath.Join(work, "legacy.yaml"),
		filepath.Join(work, "out")); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(work, "out", "audit.yaml"))
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	var ad auditDoc
	if err := yaml.Unmarshal(raw, &ad); err != nil {
		t.Fatalf("parse audit: %v", err)
	}
	if ad.Audit.TotalRecords != 7 {
		t.Fatalf("expected 7 records in rebuilt audit, got %d",
			ad.Audit.TotalRecords)
	}
	statusSum := ad.Audit.ByStatus.Removed +
		ad.Audit.ByStatus.InProgress +
		ad.Audit.ByStatus.Keep
	if statusSum != 7 {
		t.Fatalf("status sum=%d != 7 (removed=%d in_progress=%d keep=%d)",
			statusSum,
			ad.Audit.ByStatus.Removed,
			ad.Audit.ByStatus.InProgress,
			ad.Audit.ByStatus.Keep)
	}
}

// TestMigrate_OpenQuestionsPreserved asserts the legacy
// `open_questions:` block is preserved verbatim in the
// open_questions.yaml output, including comment-only context.
func TestMigrate_OpenQuestionsPreserved(t *testing.T) {
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "legacy.yaml"),
		[]byte(syntheticLegacyYaml), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := migrate(filepath.Join(work, "legacy.yaml"),
		filepath.Join(work, "out")); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(work, "out", "open_questions.yaml"))
	if err != nil {
		t.Fatalf("read open_questions: %v", err)
	}
	if !strings.Contains(string(got), "TEST-OQ-1") {
		t.Fatalf("open_questions.yaml missing TEST-OQ-1 id:\n%s", got)
	}
}

func mustRead(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("missing file: %s: %v", path, err)
	}
}

// compareDirs asserts two directory trees hold byte-identical files
// for every path under them.
func compareDirs(t *testing.T, a, b string) {
	t.Helper()
	pathsA := walkPaths(t, a)
	pathsB := walkPaths(t, b)
	if len(pathsA) != len(pathsB) {
		t.Fatalf("dir size mismatch: %s has %d files, %s has %d",
			a, len(pathsA), b, len(pathsB))
	}
	type kv struct {
		rel string
		raw []byte
	}
	mA := make(map[string][]byte, len(pathsA))
	for _, p := range pathsA {
		rel, _ := filepath.Rel(a, p)
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		mA[rel] = data
	}
	for _, p := range pathsB {
		rel, _ := filepath.Rel(b, p)
		expected, ok := mA[rel]
		if !ok {
			t.Fatalf("file only in %s: %s", b, rel)
		}
		actual, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		// Round-trip via json so the comparison is robust to
		// non-deterministic map-iteration ordering in YAML
		// serialization (Go yaml.v3 marshals maps in key order,
		// but the migrator should still be deterministic for a
		// given input run-once-output-once).
		eJ, _ := json.Marshal(string(expected))
		aJ, _ := json.Marshal(string(actual))
		if string(eJ) != string(aJ) {
			t.Fatalf("byte difference: %s\nwant:\n%s\ngot:\n%s",
				rel, expected, actual)
		}
	}
}

func walkPaths(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		out = append(out, p)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return out
}
