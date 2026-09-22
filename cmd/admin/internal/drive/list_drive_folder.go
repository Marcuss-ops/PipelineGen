// cmd/admin/list_drive_folder.go — Drive folder scanner
//
// Recursive Drive folder scan plus optional sync of discovered folders
// into the canonical `clip_folders` SQLite table.
//
// This command lists only FOLDERS. Use `drive-ls` to list the files
// (and folders) inside a folder, and `search-drive` for raw Drive
// queries.
//
// Post-fix wiring:
//
//   - app.ExportInitCoreMinimal was removed in PR4d-final; we use
//     wiring.InitComposition instead.
//   - The deleted internal/media/models package; the local
//     `folderRec` struct replaces `models.ClipFolder`. The struct
//     mirrors the column shape from migration 011_create_characters.sql
//   - the canonical schema in internal/platform/sqlite/canonical.go.
//   - internal/repository/clips is removed; the canonical
//     *assets.ClipsRepository (root.Repos.ClipsRepo) only knows about
//     `media_assets`, not the `clip_folders` table. We use raw SQL on
//     root.DB.DB to upsert rows to `clip_folders` directly.
//   - internal/upload/drive is removed; root.Drive.DriveUploader is the
//     canonical (internal/infrastructure/drive) replacement.
package drive

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

// folderRec mirrors the columns of `clip_folders` used by the canonical
// listing + Drive folder sync code. Used purely to (de)serialise a
// folder before handing it to the canonical folder repository.
type folderRec struct {
	ID         string
	Source     string
	GroupName  string
	FolderID   string
	FolderPath string
	SourceURL  string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// clipFolderWriter is the narrow canonical folder-write port the Drive
// scanner consumes. *imagesregistry.ClipsRepository implements it; the
// concrete type is deliberately not imported here so the operator tool
// depends on the port rather than the storage package.
type clipFolderWriter interface {
	UpsertFolder(ctx context.Context, folder *detail.ClipFolder) error
}

func RunListDriveFolder(args []string) error {
	fs := flag.NewFlagSet("list-drive-folder", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	folder := fs.String("folder", config.DefaultMediaRootFolderID, "Drive folder ID to list (defaults to the canonical media root)")
	syncDB := fs.Bool("sync-db", true, "Sync the discovered folders to the database")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	root, _, rootCleanup, err := wiring.InitComposition(cfg, log)
	if err != nil {
		log.Fatal("Failed to initialize composition root", zap.Error(err))
	}
	defer rootCleanup()

	if root.Drive == nil || root.Drive.Reader == nil {
		return fmt.Errorf("drive reader port is not available")
	}

	// Wave B (June 2026): split the legacy `root.Drive.DriveUploader`
	// (single concrete *drive.Uploader) into the canonical Pattern 0
	// port pair — drive.Admin for folder/file lifecycle ops, drive.Reader
	// for read-only listing ops (passed to scanFolders). Both reference
	// the same concrete uploader in production (Bundle.Admin and
	// Bundle.Reader are populated side-by-side in BuildDriveBundle).
	driveReader := root.Drive.Reader
	ctx := cli.CmdContext()

	fmt.Printf("=== Scanning Google Drive Folder Hierarchy ===\n")
	fmt.Printf("Root Folder ID: %s\n", *folder)
	fmt.Printf("Sync to Database: %t\n\n", *syncDB)

	// The clips repository is only required on the write path. A
	// read-only listing (--sync-db=false) must not fail just because
	// the DB is unavailable.
	var folderWriter clipFolderWriter
	if *syncDB {
		if root.Repos == nil || root.Repos.ClipsRepo == nil {
			return fmt.Errorf("list-drive-folder: canonical clips repository is required for --sync-db")
		}
		folderWriter = root.Repos.ClipsRepo
	}

	scanned, synced, err := scanFolders(ctx, driveReader, folderWriter, *folder, "", "", *syncDB, log)
	if err != nil {
		return fmt.Errorf("scan failed: %w", err)
	}

	// Report scanned and synced separately: a read-only run
	// (--sync-db=false) legitimately scans folders without writing any
	// row, so the old single counter printed "0 folders" for a
	// perfectly successful scan.
	fmt.Printf("\nDone! Scanned %d folder(s), synced %d to DB.\n", scanned, synced)
	return nil
}

// scanFolders recursively walks a Drive folder hierarchy, prints entries
// and (when syncDB is true) upserts each discovered folder into the
// canonical `clip_folders` SQLite table.
//
// Replaces the legacy `clips.Repository.UpsertClipFolder` call, which
// which lived in the deleted internal/repository/clips package. The
// upward-compatible column shape (id, source, source_url, folder_id,
// folder_path, group_name, search_key, created_at, updated_at) matches
// migration 011_create_characters.sql.
// scanFolders now takes the canonical drive.Reader port (Wave B,
// June 2026) — ListFiles is a Reader method. Caller (runListDriveFolder)
// passes root.Drive.Reader from the DriveBundle composition.
//
// The recursive `scanFolders` passes the same `drive.Reader` along,
// so a single declared port threads the whole subtree traversal.
// scanFolders returns (scanned, synced, err) where `scanned` is the
// number of Drive folders visited and `synced` the number of catalog
// rows written. The two are distinct so a read-only run reports the
// truthful scan count even though it writes nothing.
func scanFolders(
	ctx context.Context,
	reader drive.Reader,
	writer clipFolderWriter,
	folderID, currentPath, source string,
	syncDB bool,
	log *zap.Logger,
) (int, int, error) {
	if reader == nil {
		return 0, 0, fmt.Errorf("drive reader port not available")
	}
	files, err := reader.ListFiles(ctx, folderID)
	if err != nil {
		return 0, 0, err
	}

	scanned := 0
	synced := 0
	for _, file := range files {
		if file.MimeType != "application/vnd.google-apps.folder" {
			continue
		}

		childSource := source
		childPath := file.Name
		if currentPath != "" {
			childPath = currentPath + "/" + file.Name
		} else {
			// Level 1: map root folder names to their lowercase sources
			nameLower := strings.ToLower(strings.TrimSpace(file.Name))
			switch nameLower {
			case "stock":
				childSource = "stock"
			case "clips":
				childSource = "youtube"
			case "books":
				childSource = "books"
			case "ai images":
				childSource = "videoai"
			case "outro":
				childSource = "outro"
			case "avatar ai":
				childSource = "avatar_ai"
			case "effetti suoni online":
				childSource = "sound_effects"
			case "immagini":
				childSource = "images"
			case "voiceover":
				childSource = "voiceover"
			case "copertine":
				childSource = "copertine"
			default:
				childSource = nameLower
			}
		}

		link := file.WebViewLink
		if link == "" {
			link = "https://drive.google.com/drive/folders/" + file.ID
		}

		fmt.Printf("Folder: %s (%s) [source: %s, path: %s, link: %s]\n", file.Name, file.ID, childSource, childPath, link)
		scanned++

		if syncDB && writer != nil {
			now := time.Now().UTC()
			cf := folderRec{
				ID:         file.ID,
				Source:     childSource,
				GroupName:  file.Name,
				FolderID:   file.ID,
				FolderPath: childPath,
				SourceURL:  link,
				CreatedAt:  now,
				UpdatedAt:  now,
			}
			if err := upsertClipFolder(ctx, writer, cf); err != nil {
				log.Warn("Failed to upsert folder in DB", zap.String("name", file.Name), zap.Error(err))
			} else {
				fmt.Printf("  -> Saved in DB\n")
				synced++
			}
		}

		subScanned, subSynced, err := scanFolders(ctx, reader, writer, file.ID, childPath, childSource, syncDB, log)
		if err != nil {
			log.Warn("Failed to scan subfolder", zap.String("name", file.Name), zap.Error(err))
		}
		scanned += subScanned
		synced += subSynced
	}
	return scanned, synced, nil
}

// upsertClipFolder writes a single folderRec through the canonical folder
// repository.
//
// MEDIA-SSOT (POSTGRES-MEDIA-CUTOVER): the raw `clip_folders` INSERT OR
// REPLACE is gone. The repository owns the operational row AND mirrors it
// into the PostgreSQL folder projection (search_key is derived canonically
// from group + folder_path rather than passed in).
func upsertClipFolder(ctx context.Context, writer clipFolderWriter, cf folderRec) error {
	return writer.UpsertFolder(ctx, &detail.ClipFolder{
		ID:         cf.ID,
		Source:     cf.Source,
		SourceURL:  cf.SourceURL,
		FolderID:   cf.FolderID,
		FolderPath: cf.FolderPath,
		Group:      cf.GroupName,
		Metadata:   "{}",
		CreatedAt:  cf.CreatedAt,
		UpdatedAt:  cf.UpdatedAt,
	})
}
