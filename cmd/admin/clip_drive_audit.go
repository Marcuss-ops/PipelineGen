// cmd/admin/clip_drive_audit.go — compares the REAL Google Drive tree with
// the media_assets projection for YouTube clips and emits a JSON report of
// every divergence.
//
// Background (2026-08-06): the per-clip upload path threads the request
// folder_id as the publisher ParentFolderID, so YouTubeClipPath creates
// a nested `{group-fallback}/{video_id}` subfolder (e.g.
// `Tom Holland/youtube_uncategorized/uVoMqnwEdBQ`). The ClipAsset writer
// persisted the REQUEST folder instead of the physical leaf folder, so
// media_assets rows and the Drive tree diverged. The backfill command
// (`backfill-clip-folder-path`) repairs the divergence; THIS command is the
// diagnostic twin — it walks the live Drive tree and reports where the DB
// disagrees with reality, without writing anything.
//
// Scope: canonical YouTube clips only (source='youtube' AND id LIKE 'yt_%'),
// matching the backfill. Planner/stock bindings that share source='youtube'
// live under a different Drive root and are intentionally excluded.
//
// godlike/07 NO-FAKE-AVAILABILITY: the audit is read-only (dry-run only, no
// --apply). Divergences are counted and surfaced per-run, never silently
// dropped. The JSON report is emitted to stdout by default, or to a file
// with --report. A folder/file whose parent chain cannot be resolved is
// reported as failed, never assumed aligned.
//
// Semantics: the walk uses Reader.ListFiles, which returns only NON-trashed
// children. A clip whose Drive file is trashed (or deleted) is therefore
// reported as file_missing_on_drive — the file is not part of the live tree.
// This intentionally differs from the backfill's GetFileMeta parent-chain
// walk (which resolves trashed files too): the audit answers "is this clip
// actually reachable in the current Drive tree?", not "where did this file
// used to live?".
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

// clipFilenamePrefix / clipFilenameSuffix delimit clip-like Drive files
// (BuildClipFilename format: yt_<videoID>_<start>_<end>_<policy>_<slug>.mp4)
// used for orphan detection under the clips root.
const (
	clipFilenamePrefix = "yt_"
	clipFilenameSuffix = ".mp4"
)

// driveLister lists the non-trashed children of a Drive folder. Injected so
// tests can stub Drive responses without a network.
type driveLister func(ctx context.Context, parentID string) ([]drive.DriveFileInfo, error)

// driveTreeFolder is a folder discovered by the tree walk. The audit only
// needs the folder count; the file entries carry the physical location data.
type driveTreeFolder struct {
	ID   string
	Name string
}

// driveTreeFile is a file discovered by the tree walk, with the path of the
// folder that physically contains it.
type driveTreeFile struct {
	ID         string
	Name       string
	ParentID   string // immediate parent folder ID (the physical leaf folder)
	FolderPath string // relative path of the parent folder from the root
}

// driveTree is the materialized real Drive tree under the configured root.
type driveTree struct {
	folders map[string]driveTreeFolder
	files   map[string]driveTreeFile
}

// clipAssetAuditRow is the minimal media_assets projection the audit needs.
type clipAssetAuditRow struct {
	id           string
	driveFileID  string
	driveLink    string
	downloadLink string
	dbFolderID   string
	dbFolderPath string
}

// clipDriveDivergence is a single per-clip DB↔Drive disagreement.
type clipDriveDivergence struct {
	ClipID          string   `json:"clip_id"`
	DriveFileID     string   `json:"drive_file_id"`
	DriveLink       string   `json:"drive_link,omitempty"`
	DownloadLink    string   `json:"download_link,omitempty"`
	LinkFileID      string   `json:"link_file_id,omitempty"`
	DBFolderID      string   `json:"db_folder_id,omitempty"`
	DBFolderPath    string   `json:"db_folder_path,omitempty"`
	DriveFolderID   string   `json:"drive_folder_id,omitempty"`
	DriveFolderPath string   `json:"drive_folder_path,omitempty"`
	Issues          []string `json:"issues"`
}

// clipDriveOrphan is a clip-like file present on Drive but absent from the
// media_assets clip set (the inverse divergence: Drive has it, DB does not).
type clipDriveOrphan struct {
	FileID     string `json:"file_id"`
	Name       string `json:"name"`
	FolderPath string `json:"folder_path,omitempty"`
}

// clipDriveUntrackedUpload is a Drive file that IS referenced by a clip's
// drive_link/download_link but is NOT the clip's drive_file_id. This is the
// signature of a reprocess that uploaded a fresh Drive file without updating
// the identity pointer — the canonical link points at a file the identity
// column does not track.
type clipDriveUntrackedUpload struct {
	FileID     string `json:"file_id"`
	Name       string `json:"name"`
	FolderPath string `json:"folder_path,omitempty"`
}

// clipDriveAuditReport is the JSON report emitted by the command.
type clipDriveAuditReport struct {
	SchemaVersion    int                        `json:"schema_version"`
	GeneratedAt      string                     `json:"generated_at"`
	Mode             string                     `json:"mode"`
	RootFolderID     string                     `json:"root_folder_id"`
	Summary          clipDriveAuditSummary      `json:"summary"`
	Divergences      []clipDriveDivergence      `json:"divergences"`
	OrphanFiles      []clipDriveOrphan          `json:"orphan_clip_files"`
	UntrackedUploads []clipDriveUntrackedUpload `json:"untracked_uploads,omitempty"`
	WalkFailures     []string                   `json:"walk_failures,omitempty"`
}

// clipDriveAuditSummary aggregates the audit outcome.
type clipDriveAuditSummary struct {
	ClipsTotal         int `json:"clips_total"`
	Aligned            int `json:"aligned"`
	Divergences        int `json:"divergences"`
	NoDriveFileID      int `json:"no_drive_file_id"`
	FileMissingOnDrive int `json:"file_missing_on_drive"`
	FolderIDMismatch   int `json:"folder_id_mismatch"`
	FolderPathMismatch int `json:"folder_path_mismatch"`
	LinkFileIDMismatch int `json:"link_file_id_mismatch"`
	LinkFileMissing    int `json:"link_file_missing_on_drive"`
	UntrackedUploads   int `json:"untracked_uploads"`
	FoldersWalked      int `json:"folders_walked"`
	FilesWalked        int `json:"files_walked"`
	OrphanClipFiles    int `json:"orphan_clip_files"`
	Failed             int `json:"failed"`
}

// runClipDriveAudit implements the `clip-drive-audit` subcommand. Read-only:
// walks the live Drive tree, compares it with media_assets, and emits a JSON
// report of the divergences for every YouTube clip.
func runClipDriveAudit(args []string) error {
	fs := flag.NewFlagSet("clip-drive-audit", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	root := fs.String("root", "", "Drive root folder ID to walk (default: config drive.normal_clips_source_folder)")
	limit := fs.Int("limit", 0, "Maximum number of clip rows to audit; zero means all")
	reportPath := fs.String("report", "", "Write the JSON report to this file (default: stdout)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *limit < 0 {
		return fmt.Errorf("--limit must be non-negative")
	}

	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()

	rootCtx, _, rootCleanup, err := app.InitComposition(cfg, log)
	if err != nil {
		return fmt.Errorf("initialize composition: %w", err)
	}
	defer rootCleanup()
	if rootCtx == nil || rootCtx.DB == nil || rootCtx.DB.DB == nil {
		return fmt.Errorf("database is required")
	}
	if rootCtx.Drive == nil || rootCtx.Drive.Reader == nil {
		return fmt.Errorf("drive reader port is not available")
	}

	rootFolderID := strings.TrimSpace(*root)
	if rootFolderID == "" {
		rootFolderID = strings.TrimSpace(cfg.Drive.NormalClipsSourceFolder)
	}
	if rootFolderID == "" {
		return fmt.Errorf("drive root folder is not configured (set config drive.normal_clips_source_folder or pass --root)")
	}

	report, err := clipDriveAudit(ctx, rootCtx.DB.DB, rootCtx.Drive.Reader.ListFiles, rootFolderID, *limit)
	if err != nil {
		return err
	}
	payload, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("clip-drive-audit: marshal report: %w", err)
	}

	if out := strings.TrimSpace(*reportPath); out != "" {
		if err := os.WriteFile(out, append(payload, '\n'), 0o644); err != nil {
			return fmt.Errorf("clip-drive-audit: write report: %w", err)
		}
		fmt.Printf("clip-drive-audit: report written to %s (%d divergences)\n", out, report.Summary.Divergences)
	} else {
		fmt.Println(string(payload))
	}
	return nil
}

// clipDriveAudit walks the real Drive tree under rootFolderID and compares it
// with the media_assets youtube-clip projection. It never writes. Divergences
// are returned in the report; resolution failures are surfaced in
// WalkFailures (fail-closed, never silently treated as aligned).
func clipDriveAudit(
	ctx context.Context,
	db *sql.DB,
	list driveLister,
	rootFolderID string,
	limit int,
) (*clipDriveAuditReport, error) {
	tree, failures := walkDriveTree(ctx, list, rootFolderID)

	rows, err := loadClipAuditRows(ctx, db, limit)
	if err != nil {
		return nil, err
	}

	report := &clipDriveAuditReport{
		SchemaVersion: 1,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		Mode:          "dry-run",
		RootFolderID:  rootFolderID,
		WalkFailures:  failures,
	}
	report.Summary.ClipsTotal = len(rows)
	report.Summary.Failed = len(failures)

	for _, r := range rows {
		d := clipDriveDivergence{
			ClipID:       r.id,
			DriveFileID:  r.driveFileID,
			DriveLink:    r.driveLink,
			DownloadLink: r.downloadLink,
			DBFolderID:   r.dbFolderID,
			DBFolderPath: r.dbFolderPath,
		}
		switch {
		case r.driveFileID == "":
			d.Issues = []string{"no_drive_file_id"}
			report.Summary.NoDriveFileID++
		default:
			file, ok := tree.files[r.driveFileID]
			if !ok {
				d.Issues = []string{"file_missing_on_drive"}
				report.Summary.FileMissingOnDrive++
			} else {
				d.DriveFolderID = file.ParentID
				d.DriveFolderPath = file.FolderPath
				if d.DriveFolderID != d.DBFolderID {
					d.Issues = append(d.Issues, "folder_id_mismatch")
					report.Summary.FolderIDMismatch++
				}
				if d.DriveFolderPath != d.DBFolderPath {
					d.Issues = append(d.Issues, "folder_path_mismatch")
					report.Summary.FolderPathMismatch++
				}
			}
		}

		// Link identity: the canonical drive_link/download_link file ID must
		// match the identity pointer (drive_file_id). A reprocess that
		// published a fresh Drive file without updating drive_file_id leaves
		// the links pointing at a different file than the identity column
		// tracks. The link file is also fail-closed checked against the live
		// tree.
		linkID := extractDriveFileID(r.driveLink)
		if linkID == "" {
			linkID = extractDriveFileID(r.downloadLink)
		}
		if linkID != "" && linkID != r.driveFileID {
			d.LinkFileID = linkID
			d.Issues = append(d.Issues, "link_drive_file_id_mismatch")
			report.Summary.LinkFileIDMismatch++
			if _, ok := tree.files[linkID]; !ok {
				d.Issues = append(d.Issues, "link_file_missing_on_drive")
				report.Summary.LinkFileMissing++
			}
		}

		if len(d.Issues) == 0 {
			report.Summary.Aligned++
			continue
		}
		report.Summary.Divergences++
		report.Divergences = append(report.Divergences, d)
	}

	// Inverse divergence: clip-like files on Drive that media_assets does
	// not reference (orphans under the clips root). Orphan membership is
	// computed against the FULL drive_file_id set — a --limit narrows only
	// the per-clip divergence audit, never the orphan comparison (otherwise
	// every clip beyond the limit would appear as a false orphan). On a full
	// run the map is already in hand from the clip rows; only bounded runs
	// need the separate query.
	allFileIDs := make(map[string]struct{}, len(rows))
	allLinkFileIDs := make(map[string]struct{}, len(rows))
	for _, r := range rows {
		if r.driveFileID != "" {
			allFileIDs[r.driveFileID] = struct{}{}
		}
		if id := extractDriveFileID(r.driveLink); id != "" {
			allLinkFileIDs[id] = struct{}{}
		}
		if id := extractDriveFileID(r.downloadLink); id != "" {
			allLinkFileIDs[id] = struct{}{}
		}
	}
	if limit > 0 {
		var err error
		allFileIDs, err = loadAllClipDriveFileIDs(ctx, db)
		if err != nil {
			return nil, err
		}
		if allLinkFileIDs, err = loadAllClipLinkFileIDs(ctx, db); err != nil {
			return nil, err
		}
	}

	// A Drive file is "referenced" when either the identity pointer
	// (drive_file_id) OR a canonical link (drive_link/download_link) points
	// at it. Orphan = clip-like file with no reference at all. Untracked
	// upload = referenced by a link but not by the identity pointer (the
	// reprocess-without-drive_file_id-update signature).
	referenced := make(map[string]struct{}, len(allFileIDs)+len(allLinkFileIDs))
	for id := range allFileIDs {
		referenced[id] = struct{}{}
	}
	for id := range allLinkFileIDs {
		referenced[id] = struct{}{}
	}
	for id, f := range tree.files {
		if isClipLikeFilename(f.Name) {
			if _, known := referenced[id]; !known {
				report.Summary.OrphanClipFiles++
				report.OrphanFiles = append(report.OrphanFiles, clipDriveOrphan{
					FileID:     id,
					Name:       f.Name,
					FolderPath: f.FolderPath,
				})
			}
		}
		if _, inLinks := allLinkFileIDs[id]; inLinks {
			if _, inIdentity := allFileIDs[id]; !inIdentity {
				report.Summary.UntrackedUploads++
				report.UntrackedUploads = append(report.UntrackedUploads, clipDriveUntrackedUpload{
					FileID:     id,
					Name:       f.Name,
					FolderPath: f.FolderPath,
				})
			}
		}
	}
	sort.Slice(report.OrphanFiles, func(i, j int) bool { return report.OrphanFiles[i].Name < report.OrphanFiles[j].Name })
	report.Summary.FoldersWalked = len(tree.folders)
	report.Summary.FilesWalked = len(tree.files)
	return report, nil
}

// loadClipAuditRows selects the canonical youtube-clip rows
// (source='youtube' AND id LIKE 'yt_%'), matching the backfill scope.
func loadClipAuditRows(ctx context.Context, db *sql.DB, limit int) ([]clipAssetAuditRow, error) {
	query := `SELECT id, COALESCE(drive_file_id,''), COALESCE(drive_link,''), COALESCE(download_link,''), COALESCE(folder_id,''), COALESCE(folder_path,'')
		FROM media_assets
		WHERE source = 'youtube'
		  AND id LIKE 'yt_%'
		ORDER BY id`
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("clip-drive-audit: query: %w", err)
	}
	defer rows.Close()

	var out []clipAssetAuditRow
	for rows.Next() {
		var r clipAssetAuditRow
		if err := rows.Scan(&r.id, &r.driveFileID, &r.driveLink, &r.downloadLink, &r.dbFolderID, &r.dbFolderPath); err != nil {
			return nil, fmt.Errorf("clip-drive-audit: scan: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("clip-drive-audit: rows: %w", err)
	}
	return out, nil
}

// loadAllClipLinkFileIDs returns the complete set of Drive file IDs
// referenced by drive_link/download_link for canonical youtube clips,
// regardless of any --limit. Used for untracked-upload detection so a
// bounded run never mislabels a link-referenced file.
func loadAllClipLinkFileIDs(ctx context.Context, db *sql.DB) (map[string]struct{}, error) {
	rows, err := db.QueryContext(ctx, `SELECT COALESCE(drive_link,''), COALESCE(download_link,'') FROM media_assets
		WHERE source = 'youtube'
		  AND id LIKE 'yt_%'`)
	if err != nil {
		return nil, fmt.Errorf("clip-drive-audit: link id query: %w", err)
	}
	defer rows.Close()

	out := map[string]struct{}{}
	for rows.Next() {
		var driveLink, downloadLink string
		if err := rows.Scan(&driveLink, &downloadLink); err != nil {
			return nil, fmt.Errorf("clip-drive-audit: link id scan: %w", err)
		}
		if id := extractDriveFileID(driveLink); id != "" {
			out[id] = struct{}{}
		}
		if id := extractDriveFileID(downloadLink); id != "" {
			out[id] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("clip-drive-audit: link id rows: %w", err)
	}
	return out, nil
}

// loadAllClipDriveFileIDs returns the complete set of media_assets
// drive_file_id values for canonical youtube clips, regardless of any
// --limit. Used exclusively for orphan detection so a bounded run never
// mislabels reachable Drive files as DB orphans.
func loadAllClipDriveFileIDs(ctx context.Context, db *sql.DB) (map[string]struct{}, error) {
	rows, err := db.QueryContext(ctx, `SELECT drive_file_id FROM media_assets
		WHERE source = 'youtube'
		  AND id LIKE 'yt_%'
		  AND TRIM(COALESCE(drive_file_id, '')) <> ''`)
	if err != nil {
		return nil, fmt.Errorf("clip-drive-audit: orphan query: %w", err)
	}
	defer rows.Close()

	out := map[string]struct{}{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("clip-drive-audit: orphan scan: %w", err)
		}
		out[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("clip-drive-audit: orphan rows: %w", err)
	}
	return out, nil
}

// walkDriveTree recursively lists the Drive tree under rootFolderID using
// the injected lister. Returns the materialized tree plus a list of
// folder-walk failures (each failure is surfaced, the walk continues).
// A depth guard prevents cycles / runaway shared-drive trees.
func walkDriveTree(ctx context.Context, list driveLister, rootFolderID string) (*driveTree, []string) {
	tree := &driveTree{folders: map[string]driveTreeFolder{}, files: map[string]driveTreeFile{}}
	visited := map[string]bool{rootFolderID: true}

	type queueItem struct {
		folderID string
		path     string // slash-joined relative path of this folder
	}
	queue := []queueItem{{folderID: rootFolderID, path: ""}}
	var failures []string

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
					childPath := child.Name
					if item.path != "" {
						childPath = item.path + "/" + child.Name
					}
					if !visited[child.ID] {
						visited[child.ID] = true
						tree.folders[child.ID] = driveTreeFolder{ID: child.ID, Name: child.Name}
						next = append(next, queueItem{folderID: child.ID, path: childPath})
					}
				} else {
					tree.files[child.ID] = driveTreeFile{
						ID: child.ID, Name: child.Name, ParentID: item.folderID, FolderPath: item.path,
					}
				}
			}
		}
		queue = next
	}
	if len(queue) > 0 {
		failures = append(failures, fmt.Sprintf("folder walk exceeded max depth %d; %d folders unvisited", maxFolderDepth, len(queue)))
	}
	return tree, failures
}

// isClipLikeFilename reports whether a Drive file name looks like a processed
// YouTube clip (BuildClipFilename output), used for orphan detection.
func isClipLikeFilename(name string) bool {
	return strings.HasPrefix(name, clipFilenamePrefix) && strings.HasSuffix(name, clipFilenameSuffix)
}

// extractDriveFileID returns the Google Drive file ID embedded in a
// webViewLink (https://drive.google.com/file/d/{ID}/…) or a downloadLink
// (https://drive.google.com/uc?id={ID}). Returns "" when the URL carries no
// parseable Drive file ID.
func extractDriveFileID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if idx := strings.Index(raw, "/file/d/"); idx >= 0 {
		rest := raw[idx+len("/file/d/"):]
		if cut := strings.IndexAny(rest, "/?#"); cut >= 0 {
			rest = rest[:cut]
		}
		if rest != "" {
			return rest
		}
	}
	if idx := strings.Index(raw, "id="); idx >= 0 {
		rest := raw[idx+len("id="):]
		if cut := strings.IndexAny(rest, "&?#"); cut >= 0 {
			rest = rest[:cut]
		}
		if rest != "" {
			return rest
		}
	}
	return ""
}
