package main

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// TestLoadDeprecationManifest_ShardedEqualsLegacy is the GOLDEN test
// for the planned split of architecture/deprecations.yaml into a
// sharded directory. It loads the same three records under two
// distinct on-disk shapes and asserts that the resulting
// []deprecationRecord slices are equal modulo filesystem-alphabetical
// ordering. If this test ever regresses it means the planned split is
// unsafe — do NOT delete it without re-running the production
// validator on a representative registry content set.
func TestLoadDeprecationManifest_ShardedEqualsLegacy(t *testing.T) {
	legacyManifest, legacyDup, lerr := loadDeprecationManifest("testdata/deprecations_legacy.yaml")
	if lerr != nil {
		t.Fatalf("load legacy: %v", lerr)
	}
	shardedManifest, shardedDup, serr := loadDeprecationManifest("testdata/deprecations_sharded")
	if serr != nil {
		t.Fatalf("load sharded: %v", serr)
	}
	if len(legacyDup) != 0 || len(shardedDup) != 0 {
		t.Fatalf("unexpected duplicate-key violations on synthetic fixtures: legacy=%v sharded=%v",
			legacyDup, shardedDup)
	}
	if len(legacyManifest.Deprecations) != len(shardedManifest.Deprecations) {
		t.Fatalf("record count mismatch: legacy=%d sharded=%d",
			len(legacyManifest.Deprecations), len(shardedManifest.Deprecations))
	}
	sortByID(legacyManifest.Deprecations)
	sortByID(shardedManifest.Deprecations)
	if !reflect.DeepEqual(legacyManifest.Deprecations, shardedManifest.Deprecations) {
		t.Fatalf("Deprecations slices diverged:\nlegacy=%+v\nsharded=%+v",
			legacyManifest.Deprecations, shardedManifest.Deprecations)
	}
	// Audit parity catches any regression where the sharded-mode
	// auditBlock unmarshal stops matching the legacy-mode auditBlock
	// (e.g. a typo in the struct tag or yaml.v3 behaviour drift).
	// The fixtures carry identical audit content on purpose.
	if !reflect.DeepEqual(legacyManifest.Audit, shardedManifest.Audit) {
		t.Fatalf("Audit blocks diverged:\nlegacy=%+v\nsharded=%+v",
			legacyManifest.Audit, shardedManifest.Audit)
	}
}

// TestLoadDeprecationManifest_CrossShardDuplicateID asserts the
// loader hard-fails when two shards declare the same id. This is the
// property that keeps the canonical registry single-source-of-truth
// across the planned split.
func TestLoadDeprecationManifest_CrossShardDuplicateID(t *testing.T) {
	dir := t.TempDir()
	records := filepath.Join(dir, "records")
	if err := os.MkdirAll(records, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	shardA := filepath.Join(records, "a.yaml")
	shardB := filepath.Join(records, "b.yaml")
	if err := os.WriteFile(shardA, []byte(`deprecations:
  - id: DUP-ID-EXAMPLE
    owner_capability: internal/infrastructure/drive/x
    exact_symbol: X
    file: x.go
    file_line: "1"
    replacement: Y
    introduction_date: '2026-01-01'
    removal_date: 'never'
    tracking_issue: T
    compatibility_test: T
    usage_metric: T
    migration_phase: EXPAND
    status: in_progress
    notes: ""
`), 0o644); err != nil {
		t.Fatalf("write A: %v", err)
	}
	if err := os.WriteFile(shardB, []byte(`deprecations:
  - id: DUP-ID-EXAMPLE
    owner_capability: internal/application/translation/x
    exact_symbol: X
    file: x.go
    file_line: "1"
    replacement: Y
    introduction_date: '2026-01-01'
    removal_date: 'never'
    tracking_issue: T
    compatibility_test: T
    usage_metric: T
    migration_phase: BACKFILL
    status: in_progress
    notes: ""
`), 0o644); err != nil {
		t.Fatalf("write B: %v", err)
	}
	_, _, err := loadDeprecationManifest(dir)
	if err == nil {
		t.Fatalf("expected duplicate-id error, got nil")
	}
	if !strings.Contains(err.Error(), "DUP-ID-EXAMPLE") {
		t.Fatalf("expected error to mention id, got: %v", err)
	}
}

// TestLoadDeprecationManifest_Sharded_RequiresRecordsDir asserts a
// directory listing with no records/*.yaml shards yields a manifest
// with zero records (not an error). This keeps the loader permissive
// on auxiliary-only shards (e.g. an audit-only directory).
func TestLoadDeprecationManifest_Sharded_AllowsEmptyRecords(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "records"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	manifest, _, err := loadDeprecationManifest(dir)
	if err != nil {
		t.Fatalf("load empty sharded dir: %v", err)
	}
	if len(manifest.Deprecations) != 0 {
		t.Fatalf("expected 0 records, got %d", len(manifest.Deprecations))
	}
}

// TestCheckDeprecationsAt_NoViolations proves the validator accepts
// a fully-populated registry without violations. The legacy fixture
// carries three valid TEST-* records that satisfy every
// requiredDeprecationFields entry.
func TestCheckDeprecationsAt_NoViolations(t *testing.T) {
	stats, violations := checkDeprecationsAt("testdata/deprecations_legacy.yaml")
	if stats["deprecations_total"] != 3 {
		t.Fatalf("expected 3 records, got %d (violations=%v)",
			stats["deprecations_total"], violations)
	}
	if len(violations) != 0 {
		t.Fatalf("expected 0 violations, got: %v", violations)
	}
	if stats["deprecations_violations"] != 0 {
		t.Fatalf("expected deprecations_violations=0, got %d",
			stats["deprecations_violations"])
	}
}

// TestCheckDeprecationsAt_MissingField asserts a record missing one
// of the required fields is reported as a violation, regardless of
// whether the file is in legacy or sharded form.
func TestCheckDeprecationsAt_MissingField(t *testing.T) {
	dir := t.TempDir()
	records := filepath.Join(dir, "records")
	if err := os.MkdirAll(records, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// owner_capability deliberately empty.
	shard := filepath.Join(records, "x.yaml")
	if err := os.WriteFile(shard, []byte(`deprecations:
  - id: TEST-MISSING-FIELD
    exact_symbol: X
    file: x.go
    file_line: "1"
    replacement: Y
    introduction_date: '2026-01-01'
    removal_date: 'never'
    tracking_issue: T
    compatibility_test: T
    usage_metric: T
    migration_phase: EXPAND
    status: in_progress
    notes: ""
`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	stats, violations := checkDeprecationsAt(dir)
	if stats["deprecations_total"] != 1 {
		t.Fatalf("expected 1 record, got %d", stats["deprecations_total"])
	}
	if len(violations) == 0 {
		t.Fatalf("expected at least one violation, got 0")
	}
	found := false
	for _, v := range violations {
		if strings.Contains(v, `missing required field "owner_capability"`) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected owner_capability violation, got: %v", violations)
	}
}

// TestCheckDeprecationsAt_ExpiredNotRemoved asserts the temporal
// rule: a record with removal_date in the past and status !=
// "removed" is a violation. We use a fixed past date (2020-01-01)
// so the assertion is deterministic across time zones.
func TestCheckDeprecationsAt_ExpiredNotRemoved(t *testing.T) {
	dir := t.TempDir()
	records := filepath.Join(dir, "records")
	if err := os.MkdirAll(records, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	shard := filepath.Join(records, "x.yaml")
	if err := os.WriteFile(shard, []byte(`deprecations:
  - id: TEST-EXPIRED-1
    owner_capability: internal/infrastructure/drive/x
    exact_symbol: X
    file: x.go
    file_line: "1"
    replacement: Y
    introduction_date: '2019-01-01'
    removal_date: '2020-01-01'
    tracking_issue: T
    compatibility_test: T
    usage_metric: T
    migration_phase: EXPAND
    status: in_progress
    notes: ""
`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, violations := checkDeprecationsAt(dir)
	found := false
	for _, v := range violations {
		if strings.Contains(v, `has removal_date="2020-01-01"`) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected expired-but-not-removed violation, got: %v", violations)
	}
}

// sortByID sorts records by ID ascending so two manifests loaded from
// physically different layouts can be compared without depending on
// filesystem traversal order.
func sortByID(records []deprecationRecord) {
	sort.Slice(records, func(i, j int) bool {
		return records[i].ID < records[j].ID
	})
}
