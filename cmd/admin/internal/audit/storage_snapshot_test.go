// cmd/admin/storage_snapshot_test.go — pure-function tests for the
// storage-snapshot subcommand (GC FASE 1).
//
// godlike/07 minimum-blast-radius: only the pure helpers are tested
// here (no DB, no Qdrant, no Drive): collectDriveRoots (dedupe/empty/
// sort), shouldSnapshotCollection (production vs disposable names), and
// the manifest JSON shape (the machine contract downstream phases and
// the operator dashboard read). The live walkers (snapshotOneDB,
// snapshotQdrant, walkDriveInventory) are exercised end-to-end when the
// operator runs the command against the real stores.
package audit

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

func TestCollectDriveRoots_DedupeAndSort(t *testing.T) {
	dc := config.DriveConfig{
		MediaRootFolder:         "root-1",
		StockRootFolder:         "stock-1",
		NormalClipsSourceFolder: "clips-src",
		// ArtlistRootFolder intentionally empty -> dropped.
		SoundEffectsRootFolder: "sfx-1",
		// ClipsRootFolder repeats MediaRootFolder -> collapsed.
		ClipsRootFolder: "root-1",
	}

	roots := collectDriveRoots(dc)

	want := []string{"clips-src", "root-1", "sfx-1", "stock-1"}
	if len(roots) != len(want) {
		t.Fatalf("collectDriveRoots: got %d roots %v, want %d", len(roots), roots, len(want))
	}
	for i := range want {
		if roots[i] != want[i] {
			t.Errorf("roots[%d] = %q, want %q (full: %v)", i, roots[i], want[i], roots)
		}
	}
}

func TestCollectDriveRoots_AllEmpty(t *testing.T) {
	var dc config.DriveConfig
	roots := collectDriveRoots(dc)
	if len(roots) != 0 {
		t.Fatalf("expected no roots for empty config, got %v", roots)
	}
}

func TestShouldSnapshotCollection(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"media_assets", true},
		{"media_assets_v4_abc", true},
		{"media_assets_v3_e5_768_siglip_768", true},
		{"synthetic_assets_test_v3", false},
		{"media_assets_v4_recovery_20260817_1712", false},
		{"benchmark_collection", false},
		{"some_test_collection", false},
	}
	for _, c := range cases {
		if got := shouldSnapshotCollection(c.name); got != c.want {
			t.Errorf("shouldSnapshotCollection(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestStorageSnapshotManifest_JSONShape locks the machine-readable
// contract: the manifest must always declare no_deletions_performed=true
// (the FASE 1 hard invariant) and carry the per-section status fields
// that downstream GC phases / the operator dashboard read.
func TestStorageSnapshotManifest_JSONShape(t *testing.T) {
	m := storageSnapshotManifest{
		SchemaVersion: 1,
		Mode:          "snapshot",
		GeneratedAt:   "2026-08-22T10:00:00Z",
		NoDeletions:   true,
		SQLite:        sqliteSnapshotSection{Status: "ok"},
		Qdrant:        qdrantSnapshotSection{Status: "skipped"},
		Drive:         driveSnapshotSection{Status: "ok", Summary: driveSnapshotSummary{Files: 12, Folders: 3}},
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	s := string(raw)
	for _, want := range []string{
		`"no_deletions_performed":true`,
		`"schema_version":1`,
		`"mode":"snapshot"`,
		`"sqlite":{"status":"ok"`,
		`"qdrant":{"status":"skipped"`,
		`"files":12`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("manifest JSON missing %s in %s", want, s)
		}
	}

	var back storageSnapshotManifest
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	if !back.NoDeletions {
		t.Error("round-tripped manifest lost the no_deletions invariant")
	}
}
