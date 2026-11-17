// cmd/admin/storage_snapshot.go — GC FASE 1: unified read-only snapshot.
//
// The PipelineGen GC plan (storage-audit / garbage-collector) opens with
// a snapshot phase: before ANY classification, reconciliation, or
// deletion, the operator must capture the full state of every store so
// every later phase is provably reversible. This command implements that
// phase in one shot:
//
//  1. SQLite backup  — VACUUM INTO copy of the primary (media) and
//     observability DBs via the canonical storage.Backup helper.
//  2. Counts/tables  — per-table row counts for both DBs via the
//     canonical storage.AllUserTables + TableCounts helpers.
//  3. Drive inventory — recursive walk of every configured Drive root
//     (same walkDriveTree engine as clip-drive-audit) written to a
//     separate JSON file (the listing can be large).
//
// HARD INVARIANT (godlike/07 NO-FAKE-AVAILABILITY + the GC plan rule
// "nessuna cancellazione nella prima esecuzione"): this command performs
// ZERO deletions. It only reads (SQLite SELECT, Drive listing) and
// writes snapshot artifacts under the output dir. The former vector engine
// section was removed with the legacy vector engine on 2026-11-17.
// No --apply flag exists and none may be added.
//
// Usage:
//
//	go run ./cmd/admin storage-snapshot [flags]
//
// Flags:
//
//	--out DIR                    snapshot output dir (default <DataDir>/snapshots/<UTC-ts>)
//	--skip-drive                 skip the Drive inventory section
//
// Output layout:
//
//	<out>/manifest.json            — whole report (sqlite + drive summary)
//	<out>/sqlite-primary.sqlite    — VACUUM INTO backup of the primary DB
//	<out>/sqlite-observability.sqlite — VACUUM INTO backup of the observability DB
//	<out>/drive-inventory.json     — full recursive Drive file listing
//
// The command exits 0 when every non-skipped section completed; a
// section that errors is reported in the manifest AND as a non-zero
// exit so CI/operators never mistake a partial snapshot for a complete
// one (godlike/07).
package audit

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// ── Manifest types (machine-readable report) ───────────────────────────

// storageSnapshotManifest is the top-level snapshot report. Every
// section carries its own status so a partial run is unambiguous.
type storageSnapshotManifest struct {
	SchemaVersion int                   `json:"schema_version"`
	Mode          string                `json:"mode"`
	GeneratedAt   string                `json:"generated_at"`
	NoDeletions   bool                  `json:"no_deletions_performed"`
	OutDir        string                `json:"out_dir"`
	SQLite        sqliteSnapshotSection `json:"sqlite"`
	Drive         driveSnapshotSection  `json:"drive"`
}

type sqliteSnapshotSection struct {
	Status        string           `json:"status"` // "ok" | "error"
	Error         string           `json:"error,omitempty"`
	Primary       sqliteDBSnapshot `json:"primary"`
	Observability sqliteDBSnapshot `json:"observability"`
}

type sqliteDBSnapshot struct {
	SourcePath  string         `json:"source_path"`
	BackupPath  string         `json:"backup_path"`
	SizeBytes   int64          `json:"size_bytes"`
	SHA256      string         `json:"sha256"`
	DurationMs  int64          `json:"duration_ms"`
	TableCounts map[string]int `json:"table_counts"`
}

type driveSnapshotSection struct {
	Status       string               `json:"status"` // "ok" | "skipped" | "error"
	Error        string               `json:"error,omitempty"`
	Roots        []string             `json:"roots"`
	Summary      driveSnapshotSummary `json:"summary"`
	InventoryRel string               `json:"inventory_file,omitempty"`
}

type driveSnapshotSummary struct {
	Folders    int   `json:"folders"`
	Files      int   `json:"files"`
	TotalBytes int64 `json:"total_bytes"`
}

// driveInventoryEntry is one file in the full Drive listing (written to
// drive-inventory.json). Mirrors the clip-drive-audit tree shape plus
// the raw Drive metadata (size, md5, mime) needed by later GC phases
// (drive_orphans.json computation).
type driveInventoryEntry struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	MimeType   string `json:"mime_type"`
	Size       int64  `json:"size"`
	MD5        string `json:"md5,omitempty"`
	ParentID   string `json:"parent_id,omitempty"`
	FolderPath string `json:"folder_path,omitempty"`
}

// ── CLI entry point ────────────────────────────────────────────────────

// ── SQLite section ─────────────────────────────────────────────────────

// snapshotSQLiteSet backs up the primary + observability DBs (VACUUM
// INTO) and exports per-table row counts for each. The whole section
// fails (status=error) if either DB fails — a partial backup set is
// reported, never silently accepted.

// snapshotOneDB performs the VACUUM INTO backup then exports per-table
// row counts from a read-only handle of the SAME source DB.

// exportTableCounts returns per-table row counts via the canonical
// storage.AllUserTables + TableCounts helpers.

// ── Drive section ──────────────────────────────────────────────────────

// snapshotDriveInventory walks every configured Drive root and writes
// the full recursive file listing (id, name, mime, size, md5, parent,
// path) to drive-inventory.json. The walk keeps the raw DriveFileInfo
// metadata so later GC phases (drive_orphans.json) can compute sizes and
// hashes without re-querying Drive.

// driveInventoryResult is the materialized inventory walk outcome.
type driveInventoryResult struct {
	entries    []driveInventoryEntry
	folders    int
	files      int
	totalBytes int64
}

// walkDriveInventory recursively walks each root folder and collects
// every non-trashed file with its full Drive metadata. Folder entries
// are counted but not listed (the GC phases key on files). Failures are
// collected per-folder and the walk continues (mirrors walkDriveTree's
// fail-continue contract); the depth guard prevents cycles / runaway
// trees, same maxFolderDepth as clip-drive-audit.
func walkDriveInventory(ctx context.Context, list driveLister, roots []string) (driveInventoryResult, []string) {
	res := driveInventoryResult{}
	var failures []string
	visited := map[string]bool{}

	type queueItem struct {
		folderID string
		path     string
	}
	queue := make([]queueItem, 0, len(roots))
	for _, root := range roots {
		if root == "" {
			continue
		}
		visited[root] = true
		queue = append(queue, queueItem{folderID: root, path: ""})
	}

	for depth := 0; len(queue) > 0 && depth < maxFolderDepth; depth++ {
		next := queue[:0]
		for _, item := range queue {
			children, err := list(ctx, item.folderID)
			if err != nil {
				failures = append(failures, fmt.Sprintf("folder %s (%s): %v", item.folderID, item.path, err))
				continue
			}
			for _, child := range children {
				if child.MimeType == driveFolderMimeType {
					res.folders++
					childPath := child.Name
					if item.path != "" {
						childPath = item.path + "/" + child.Name
					}
					if !visited[child.ID] {
						visited[child.ID] = true
						next = append(next, queueItem{folderID: child.ID, path: childPath})
					}
				} else {
					res.files++
					res.totalBytes += child.Size
					res.entries = append(res.entries, driveInventoryEntry{
						ID:         child.ID,
						Name:       child.Name,
						MimeType:   child.MimeType,
						Size:       child.Size,
						MD5:        child.MD5Checksum,
						ParentID:   child.Parents[0],
						FolderPath: item.path,
					})
				}
			}
		}
		queue = next
	}
	if len(queue) > 0 {
		failures = append(failures, fmt.Sprintf("folder walk exceeded max depth %d; %d folders unvisited", maxFolderDepth, len(queue)))
	}
	sort.Slice(res.entries, func(i, j int) bool {
		if res.entries[i].FolderPath != res.entries[j].FolderPath {
			return res.entries[i].FolderPath < res.entries[j].FolderPath
		}
		return res.entries[i].Name < res.entries[j].Name
	})
	return res, failures
}

// collectDriveRoots returns the distinct set of configured Drive root
// folder IDs (media root + every specific root), sorted for stable
// output. Empty values are dropped; duplicates are collapsed.
func collectDriveRoots(dc config.DriveConfig) []string {
	seen := map[string]struct{}{}
	var roots []string
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		roots = append(roots, id)
	}
	add(dc.RootFolder())
	add(dc.StockFolder())
	add(dc.AIClipsFolder())
	add(dc.NormalClipsSourceFolder)
	add(dc.ClipsFolder())
	add(dc.VoiceoverFolder())
	add(dc.ScriptsFolder())
	add(dc.ImagesFolder())
	add(dc.CopertineFolder())
	add(dc.SoundEffectsFolder())
	add(dc.OutroFolder())
	add(dc.AvatarAIRootFolder)
	sort.Strings(roots)
	return roots
}

// ── Output ─────────────────────────────────────────────────────────────
