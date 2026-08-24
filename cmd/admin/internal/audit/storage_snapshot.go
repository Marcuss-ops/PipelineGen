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
//  3. Qdrant state   — collection list, point counts, runtime-alias
//     target, existing snapshots, plus NEW server-side snapshots for
//     production collections (the Qdrant-native backup analog, same
//     surface as `dr-qdrant take-snapshot`).
//  4. Drive inventory — recursive walk of every configured Drive root
//     (same walkDriveTree engine as clip-drive-audit) written to a
//     separate JSON file (the listing can be large).
//
// HARD INVARIANT (godlike/07 NO-FAKE-AVAILABILITY + the GC plan rule
// "nessuna cancellazione nella prima esecuzione"): this command performs
// ZERO deletions. It only reads (SQLite SELECT, Qdrant GET/POST-snapshot,
// Drive listing) and writes snapshot artifacts under the output dir.
// No --apply flag exists and none may be added.
//
// Usage:
//
//	go run ./cmd/admin storage-snapshot [flags]
//
// Flags:
//
//	--out DIR                    snapshot output dir (default <DataDir>/snapshots/<UTC-ts>)
//	--skip-qdrant                skip the whole Qdrant section
//	--skip-qdrant-snapshots      record Qdrant state but do NOT create server-side snapshots
//	--skip-drive                 skip the Drive inventory section
//
// Output layout:
//
//	<out>/manifest.json            — whole report (sqlite + qdrant + drive summary)
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
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	qdrantschema "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
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
	Qdrant        qdrantSnapshotSection `json:"qdrant"`
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

type qdrantSnapshotSection struct {
	Status         string             `json:"status"` // "ok" | "skipped" | "error"
	Error          string             `json:"error,omitempty"`
	BaseURL        string             `json:"base_url"`
	RuntimeAlias   string             `json:"runtime_alias"`
	AliasTarget    string             `json:"alias_target,omitempty"`
	Collections    []qdrantCollection `json:"collections"`
	SnapshotsTaken []string           `json:"snapshots_taken,omitempty"`
	SnapshotsErr   []string           `json:"snapshots_errors,omitempty"`
}

type qdrantCollection struct {
	Name        string `json:"name"`
	Points      int    `json:"points_count"`
	Status      string `json:"status"`
	Snapshotted bool   `json:"snapshotted"`
	Snapshot    string `json:"snapshot_name,omitempty"`
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

// runStorageSnapshot is registered in subcommands.go as "storage-snapshot".
func RunStorageSnapshot(args []string) error {
	fs := flag.NewFlagSet("storage-snapshot", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	out := fs.String("out", "", "snapshot output dir (default <DataDir>/snapshots/<UTC-ts>)")
	skipQdrant := fs.Bool("skip-qdrant", false, "skip the whole Qdrant section")
	skipQdrantSnaps := fs.Bool("skip-qdrant-snapshots", false, "record Qdrant state but do NOT create server-side snapshots")
	skipDrive := fs.Bool("skip-drive", false, "skip the Drive inventory section")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	ctx := cli.CmdContext()

	// Resolve output dir.
	outDir := strings.TrimSpace(*out)
	if outDir == "" {
		outDir = filepath.Join(cfg.Storage.BackupsPath(), "snapshots",
			time.Now().UTC().Format("20060102T150405Z"))
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("storage-snapshot: create out dir %s: %w", outDir, err)
	}

	log.Info("storage-snapshot starting",
		zap.String("out", outDir),
		zap.Bool("skip_qdrant", *skipQdrant),
		zap.Bool("skip_qdrant_snapshots", *skipQdrantSnaps),
		zap.Bool("skip_drive", *skipDrive),
	)

	m := storageSnapshotManifest{
		SchemaVersion: 1,
		Mode:          "snapshot",
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		NoDeletions:   true,
		OutDir:        outDir,
	}

	exitCode := 0

	// 1+2. SQLite: backup + per-table counts.
	m.SQLite, err = snapshotSQLiteSet(ctx, cfg, outDir, log)
	if err != nil {
		log.Error("storage-snapshot: sqlite section failed", zap.Error(err))
		exitCode = 1
	}

	// 3. Qdrant: state + optional server-side snapshots.
	if *skipQdrant {
		m.Qdrant = qdrantSnapshotSection{Status: "skipped"}
	} else {
		m.Qdrant, err = snapshotQdrant(ctx, cfg, outDir, *skipQdrantSnaps, log)
		if err != nil {
			log.Error("storage-snapshot: qdrant section failed", zap.Error(err))
			exitCode = 1
		}
	}

	// 4. Drive inventory.
	if *skipDrive {
		m.Drive = driveSnapshotSection{Status: "skipped"}
	} else {
		m.Drive, err = snapshotDriveInventory(ctx, cfg, outDir, log)
		if err != nil {
			log.Error("storage-snapshot: drive section failed", zap.Error(err))
			exitCode = 1
		}
	}

	// Write the manifest.
	payload, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("storage-snapshot: marshal manifest: %w", err)
	}
	manifestPath := filepath.Join(outDir, "manifest.json")
	if err := os.WriteFile(manifestPath, append(payload, '\n'), 0o644); err != nil {
		return fmt.Errorf("storage-snapshot: write manifest: %w", err)
	}

	printStorageSnapshotSummary(m, manifestPath)
	if exitCode != 0 {
		return fmt.Errorf("storage-snapshot: one or more sections failed — see %s", manifestPath)
	}
	return nil
}

// ── SQLite section ─────────────────────────────────────────────────────

// snapshotSQLiteSet backs up the primary + observability DBs (VACUUM
// INTO) and exports per-table row counts for each. The whole section
// fails (status=error) if either DB fails — a partial backup set is
// reported, never silently accepted.
func snapshotSQLiteSet(ctx context.Context, cfg *config.Config, outDir string, log *zap.Logger) (sqliteSnapshotSection, error) {
	sec := sqliteSnapshotSection{Status: "ok"}

	primarySrc := cfg.Storage.PrimaryDBFullPath()
	primaryOut := filepath.Join(outDir, "sqlite-primary.sqlite")
	primary, err := snapshotOneDB(ctx, primarySrc, primaryOut, log)
	if err != nil {
		sec.Status = "error"
		sec.Error = fmt.Sprintf("primary: %v", err)
		return sec, err
	}
	sec.Primary = primary

	obsSrc := cfg.Storage.ObservabilityDBFullPath()
	obsOut := filepath.Join(outDir, "sqlite-observability.sqlite")
	obs, err := snapshotOneDB(ctx, obsSrc, obsOut, log)
	if err != nil {
		sec.Status = "error"
		sec.Error = fmt.Sprintf("observability: %v", err)
		return sec, err
	}
	sec.Observability = obs
	return sec, nil
}

// snapshotOneDB performs the VACUUM INTO backup then exports per-table
// row counts from a read-only handle of the SAME source DB.
func snapshotOneDB(ctx context.Context, srcPath, outPath string, log *zap.Logger) (sqliteDBSnapshot, error) {
	snap := sqliteDBSnapshot{SourcePath: srcPath, BackupPath: outPath}

	if _, err := os.Stat(srcPath); err != nil {
		return snap, fmt.Errorf("stat source %s: %w", srcPath, err)
	}

	res, err := storage.Backup(srcPath, outPath)
	if err != nil {
		return snap, fmt.Errorf("backup %s: %w", srcPath, err)
	}
	snap.SizeBytes = res.SizeBytes
	snap.SHA256 = res.SHA256
	snap.DurationMs = res.DurationMs

	// Read-only handle for the counts (never mutates the source).
	ro, err := storage.OpenReadOnly(srcPath)
	if err != nil {
		return snap, fmt.Errorf("open read-only %s for counts: %w", srcPath, err)
	}
	defer ro.Close()

	counts, err := exportTableCounts(ctx, ro)
	if err != nil {
		return snap, fmt.Errorf("table counts %s: %w", srcPath, err)
	}
	snap.TableCounts = counts

	log.Info("snapshotted DB",
		zap.String("src", srcPath),
		zap.String("out", outPath),
		zap.Int64("size", snap.SizeBytes),
		zap.String("sha256", snap.SHA256),
		zap.Int("tables", len(counts)),
	)
	return snap, nil
}

// exportTableCounts returns per-table row counts via the canonical
// storage.AllUserTables + TableCounts helpers.
func exportTableCounts(ctx context.Context, db *sql.DB) (map[string]int, error) {
	tables, err := storage.AllUserTables(ctx, db)
	if err != nil {
		return nil, err
	}
	counts, err := storage.TableCounts(ctx, db, tables)
	if err != nil {
		return nil, err
	}
	return counts, nil
}

// ── Qdrant section ─────────────────────────────────────────────────────

// snapshotQdrant records collection state and (unless skipped) creates
// server-side snapshots for production collections — the Qdrant-native
// backup, same surface as `dr-qdrant take-snapshot`. Snapshot creation
// is best-effort per collection: a failed snapshot is recorded in
// SnapshotsErr, but the state section still succeeds (the state is what
// the GC plan compares against).
func snapshotQdrant(ctx context.Context, cfg *config.Config, outDir string, skipSnapshots bool, log *zap.Logger) (qdrantSnapshotSection, error) {
	sec := qdrantSnapshotSection{
		Status:       "ok",
		BaseURL:      cfg.Qdrant.BaseURL,
		RuntimeAlias: qdrantschema.DefaultV3Schema().RuntimeAlias,
	}

	client := transport.NewClient(&qdrantschema.Config{
		BaseURL: cfg.Qdrant.BaseURL,
		APIKey:  cfg.Qdrant.APIKey,
		Timeout: cfg.Qdrant.Timeout,
	}, log)

	names, err := client.ListCollections(ctx)
	if err != nil {
		sec.Status = "error"
		sec.Error = fmt.Sprintf("list collections: %v", err)
		return sec, err
	}
	sort.Strings(names)

	// Runtime alias target (the active projection collection).
	if target, err := client.GetAliasTarget(ctx, sec.RuntimeAlias); err == nil {
		sec.AliasTarget = target
	} else {
		sec.SnapshotsErr = append(sec.SnapshotsErr, fmt.Sprintf("resolve alias %q: %v", sec.RuntimeAlias, err))
	}

	for _, name := range names {
		col := qdrantCollection{Name: name}
		if info, err := client.GetCollection(ctx, name); err == nil {
			col.Points = info.PointTotal
			col.Status = info.Status
		} else if n, cntErr := client.CountPoints(ctx, name); cntErr == nil {
			col.Points = n
			col.Status = "unknown"
		} else {
			col.Status = "error"
			sec.SnapshotsErr = append(sec.SnapshotsErr, fmt.Sprintf("collection %q info: %v", name, err))
		}

		if !skipSnapshots && shouldSnapshotCollection(name) {
			if snap, snapErr := client.CreateSnapshot(ctx, name); snapErr != nil {
				sec.SnapshotsErr = append(sec.SnapshotsErr, fmt.Sprintf("snapshot %q: %v", name, snapErr))
			} else {
				col.Snapshotted = true
				col.Snapshot = snap.Name
				sec.SnapshotsTaken = append(sec.SnapshotsTaken, snap.Name)
			}
		}
		sec.Collections = append(sec.Collections, col)
	}

	// Persist the collections listing separately for downstream phases.
	payload, _ := json.MarshalIndent(sec.Collections, "", "  ")
	_ = os.WriteFile(filepath.Join(outDir, "qdrant-collections.json"), append(payload, '\n'), 0o644)

	return sec, nil
}

// shouldSnapshotCollection reports whether a collection is a production
// projection worth snapshotting. Test / recovery / synthetic collections
// are excluded — their content is disposable by construction.
func shouldSnapshotCollection(name string) bool {
	lower := strings.ToLower(name)
	if strings.Contains(lower, "test") || strings.Contains(lower, "synthetic") ||
		strings.Contains(lower, "recovery") || strings.Contains(lower, "benchmark") {
		return false
	}
	return true
}

// ── Drive section ──────────────────────────────────────────────────────

// snapshotDriveInventory walks every configured Drive root and writes
// the full recursive file listing (id, name, mime, size, md5, parent,
// path) to drive-inventory.json. The walk keeps the raw DriveFileInfo
// metadata so later GC phases (drive_orphans.json) can compute sizes and
// hashes without re-querying Drive.
func snapshotDriveInventory(ctx context.Context, cfg *config.Config, outDir string, log *zap.Logger) (driveSnapshotSection, error) {
	sec := driveSnapshotSection{Status: "ok"}

	roots := collectDriveRoots(cfg.Drive)
	if len(roots) == 0 {
		sec.Status = "skipped"
		sec.Error = "no Drive root folders configured (set media_root_folder or any specific root)"
		return sec, nil
	}
	sec.Roots = roots

	uploader, err := buildDriveAdminForCLI(ctx, cfg, log)
	if err != nil {
		sec.Status = "error"
		sec.Error = fmt.Sprintf("init Drive client: %v", err)
		return sec, err
	}

	inventory, walkFailures := walkDriveInventory(ctx, uploader.ListFiles, roots)

	sec.Summary.Folders = inventory.folders
	sec.Summary.Files = inventory.files
	sec.Summary.TotalBytes = inventory.totalBytes

	invPath := filepath.Join(outDir, "drive-inventory.json")
	payload, _ := json.MarshalIndent(inventory.entries, "", "  ")
	if err := os.WriteFile(invPath, append(payload, '\n'), 0o644); err != nil {
		sec.Status = "error"
		sec.Error = fmt.Sprintf("write inventory: %v", err)
		return sec, err
	}
	sec.InventoryRel = "drive-inventory.json"
	if len(walkFailures) > 0 {
		sec.Error = fmt.Sprintf("%d folder(s) failed to walk (see log)", len(walkFailures))
	}
	log.Info("drive inventory complete",
		zap.Int("roots", len(roots)),
		zap.Int("folders", inventory.folders),
		zap.Int("files", inventory.files),
		zap.Int64("total_bytes", inventory.totalBytes),
		zap.Int("walk_failures", len(walkFailures)),
	)
	return sec, nil
}

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
	add(dc.ArtlistFolder())
	add(dc.BooksFolder())
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

func printStorageSnapshotSummary(m storageSnapshotManifest, manifestPath string) {
	fmt.Printf("=== storage-snapshot ===\n")
	fmt.Printf("  out_dir:      %s\n", m.OutDir)
	fmt.Printf("  generated:    %s\n", m.GeneratedAt)
	fmt.Printf("  no deletions: %v (this run performs none)\n", m.NoDeletions)

	fmt.Println("  --- sqlite ---")
	switch m.SQLite.Status {
	case "ok":
		fmt.Printf("    primary:       %s (%d bytes, sha256 %s, %d tables)\n",
			filepath.Base(m.SQLite.Primary.BackupPath), m.SQLite.Primary.SizeBytes,
			shortHash(m.SQLite.Primary.SHA256), len(m.SQLite.Primary.TableCounts))
		fmt.Printf("    observability: %s (%d bytes, sha256 %s, %d tables)\n",
			filepath.Base(m.SQLite.Observability.BackupPath), m.SQLite.Observability.SizeBytes,
			shortHash(m.SQLite.Observability.SHA256), len(m.SQLite.Observability.TableCounts))
		if n, ok := m.SQLite.Primary.TableCounts["media_assets"]; ok {
			fmt.Printf("    media_assets:  %d\n", n)
		}
	default:
		fmt.Printf("    status: %s (%s)\n", m.SQLite.Status, m.SQLite.Error)
	}

	fmt.Println("  --- qdrant ---")
	switch m.Qdrant.Status {
	case "ok":
		fmt.Printf("    base_url:    %s\n", m.Qdrant.BaseURL)
		fmt.Printf("    alias:       %s -> %s\n", m.Qdrant.RuntimeAlias, m.Qdrant.AliasTarget)
		fmt.Printf("    collections: %d\n", len(m.Qdrant.Collections))
		total := 0
		for _, c := range m.Qdrant.Collections {
			total += c.Points
		}
		fmt.Printf("    points:      %d\n", total)
		if len(m.Qdrant.SnapshotsTaken) > 0 {
			fmt.Printf("    snapshots:   %d taken (%s)\n", len(m.Qdrant.SnapshotsTaken), m.Qdrant.SnapshotsTaken[0])
		}
	case "skipped":
		fmt.Printf("    skipped\n")
	default:
		fmt.Printf("    status: %s (%s)\n", m.Qdrant.Status, m.Qdrant.Error)
	}

	fmt.Println("  --- drive ---")
	switch m.Drive.Status {
	case "ok":
		fmt.Printf("    roots:    %d\n", len(m.Drive.Roots))
		fmt.Printf("    folders:  %d\n", m.Drive.Summary.Folders)
		fmt.Printf("    files:    %d\n", m.Drive.Summary.Files)
		fmt.Printf("    listing:  %s\n", m.Drive.InventoryRel)
	case "skipped":
		fmt.Printf("    skipped%s\n", optionalSuffix(m.Drive.Error))
	default:
		fmt.Printf("    status: %s (%s)\n", m.Drive.Status, m.Drive.Error)
	}

	fmt.Printf("  manifest:     %s\n", manifestPath)
}

func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

func optionalSuffix(s string) string {
	if s == "" {
		return ""
	}
	return " (" + s + ")"
}
