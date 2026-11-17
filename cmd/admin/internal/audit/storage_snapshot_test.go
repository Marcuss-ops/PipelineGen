// cmd/admin/storage_snapshot_test.go — pure-function tests for the
// storage-snapshot subcommand (GC FASE 1).
//
// godlike/07 minimum-blast-radius: only the pure helpers are tested
// here (no DB, no Drive): collectDriveRoots (dedupe/empty/sort) and the
// manifest JSON shape (the machine contract downstream phases and the
// operator dashboard read). The live walkers (snapshotOneDB,
// walkDriveInventory) are exercised end-to-end when the operator runs
// the command against the real stores. The pgvector section was removed
// with the legacy vector engine on 2026-11-17.
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
		SoundEffectsRootFolder:  "sfx-1",
		// ClipsRootFolder repeats MediaRootFolder -> collapsed.
		ClipsRootFolder: "root-1",
	}

	roots := collectDriveRoots(dc)

	// The images root is ALWAYS collected: Drive.ImagesFolder() resolves to the
	// canonical DefaultImagesRootFolderID when no images_root_folder is set (the
	// per-image layout requires a single dedicated root), so a snapshot audit must
	// always cover it.
	want := []string{config.DefaultImagesRootFolderID, "clips-src", "root-1", "sfx-1", "stock-1"}
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
	// An unconfigured DriveConfig is no longer "no roots": the images root is a
	// code-level default (config.DefaultImagesRootFolderID), so it is the one root
	// that survives an empty config and must never be silently dropped from a
	// snapshot.
	if len(roots) != 1 || roots[0] != config.DefaultImagesRootFolderID {
		t.Fatalf("expected exactly [%s] for empty config, got %v", config.DefaultImagesRootFolderID, roots)
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
