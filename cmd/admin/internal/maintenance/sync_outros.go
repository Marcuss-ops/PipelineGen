package maintenance

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"go.uber.org/zap"
)

var supportedLanguages = []string{
	"italiano", "fra", "de", "pt", "es", "ru", "tr", "ind", "eng", "Polacco",
}

func RunSyncOutros(args []string) error {
	fs := flag.NewFlagSet("sync-outros", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apply := fs.Bool("apply", false, "Actually create missing folders on Drive and write to DB (default: dry-run only)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	root, _, coreCleanup, err := wiring.InitComposition(cfg, log)
	if err != nil {
		log.Fatal("Failed to initialize core services", zap.Error(err))
	}
	defer coreCleanup()

	if root.Drive == nil || root.Drive.Reader == nil || root.Drive.Admin == nil {
		return fmt.Errorf("drive admin/reader ports are not available")
	}

	ctx := cli.CmdContext()

	outroRootID := cfg.Drive.OutroFolder()
	if outroRootID == "" {
		outroRootID = "1MB9pTRjvHUdMXUtGOMBcvgRc-MZG2rA4" // user specified folder
	}

	if *apply {
		fmt.Printf("=== Starting Outros Synchronization (APPLY Mode) ===\n")
	} else {
		fmt.Printf("=== Starting Outros Synchronization (DRY RUN - use --apply to write) ===\n")
	}
	fmt.Printf("Outro Root Folder ID: %s\n\n", outroRootID)

	// Step 1: List subfolders of the Outro root folder
	query := fmt.Sprintf("'%s' in parents and mimeType = 'application/vnd.google-apps.folder' and trashed = false", outroRootID)
	list, err := root.Drive.Reader.SearchFiles(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to list outro folders: %w", err)
	}

	fmt.Printf("Found %d base outro folders on Drive.\n", len(list))

	for _, folder := range list {
		fmt.Printf("\nProcessing folder: %s (%s)\n", folder.Name, folder.ID)

		// Sync this base folder to clip_folders
		if *apply {
			err := upsertFolderToDB(ctx, root, folder.ID, folder.Name, "outro", "outro", "")
			if err != nil {
				log.Error("failed to upsert base folder to DB", zap.String("folder", folder.Name), zap.Error(err))
			} else {
				fmt.Printf("  ✅ Synced base folder to DB\n")
			}
		}

		// List current children of this folder
		childQuery := fmt.Sprintf("'%s' in parents and trashed = false", folder.ID)
		childList, err := root.Drive.Reader.SearchFiles(ctx, childQuery)
		if err != nil {
			log.Error("failed to list children", zap.String("folder", folder.Name), zap.Error(err))
			continue
		}

		// Map existing folders by lowercase name
		existingFolders := make(map[string]string) // name -> id
		for _, f := range childList {
			if f.MimeType == "application/vnd.google-apps.folder" {
				existingFolders[strings.ToLower(f.Name)] = f.ID
			}
		}

		// For each target language, ensure folder exists
		for _, lang := range supportedLanguages {
			langLower := strings.ToLower(lang)
			langFolderID, exists := existingFolders[langLower]
			if !exists {
				if *apply {
					// Wave C (June 2026): idempotent lookup-or-create replaces strict Files.Create.
					// GetOrCreateFolder returns the existing folder if a folder with the same
					// name already exists under parent — semantically slightly more lenient than
					// the previous strict create (which would 409 on duplicates). The call is
					// already gated behind `if !exists` so the semantic shift is benign.
					created, err := root.Drive.Admin.GetOrCreateFolder(ctx, lang, folder.ID)
					if err != nil {
						log.Error("failed to create language folder on Drive", zap.String("lang", lang), zap.Error(err))
						continue
					}
					langFolderID = created
					fmt.Printf("  📁 Created language folder: %s (%s)\n", lang, langFolderID)
				} else {
					fmt.Printf("  [DRY RUN] Would create language folder: %s\n", lang)
				}
			} else {
				fmt.Printf("  📁 Language folder already exists: %s (%s)\n", lang, langFolderID)
			}

			// Sync language folder to DB
			if *apply && langFolderID != "" {
				err := upsertFolderToDB(ctx, root, langFolderID, folder.Name+"_"+lang, "outro", folder.Name, lang)
				if err != nil {
					log.Error("failed to upsert language folder to DB", zap.String("lang", lang), zap.Error(err))
				} else {
					fmt.Printf("    ✅ Synced folder %s to DB\n", lang)
				}

				// Scan files inside this language folder and add them to media_assets
				fileQuery := fmt.Sprintf("'%s' in parents and mimeType != 'application/vnd.google-apps.folder' and trashed = false", langFolderID)
				fileList, err := root.Drive.Reader.SearchFiles(ctx, fileQuery)
				if err == nil {
					for _, file := range fileList {
						err := upsertFileToDB(ctx, root, file.ID, file.Name, folder.Name, lang, file.WebViewLink, file.WebContentLink)
						if err != nil {
							log.Error("failed to upsert file to DB", zap.String("file", file.Name), zap.Error(err))
						} else {
							fmt.Printf("      🎬 Synced file: %s\n", file.Name)
						}
					}
				}
			} else if !*apply {
				// Dry run: list existing files inside the existing language folder if it exists
				if langFolderID != "" {
					fileQuery := fmt.Sprintf("'%s' in parents and mimeType != 'application/vnd.google-apps.folder' and trashed = false", langFolderID)
					fileList, err := root.Drive.Reader.SearchFiles(ctx, fileQuery)
					if err == nil && len(fileList) > 0 {
						for _, file := range fileList {
							fmt.Printf("      [DRY RUN] Would sync file: %s\n", file.Name)
						}
					}
				}
			}
		}
	}

	fmt.Println("\nSynchronization complete.")
	return nil
}

// upsertFolderToDB writes a clip folder through the canonical folder
// repository. MEDIA-SSOT (POSTGRES-MEDIA-CUTOVER): the raw `clip_folders`
// INSERT OR REPLACE is gone — the repository owns the operational row AND
// mirrors it into the PostgreSQL folder projection, so the catalog-sync
// folder list (which reads PostgreSQL) sees this folder.
//
// The canonical writer derives search_key from (group + folder_path) rather
// than the ad-hoc `lang` value the legacy statement stored; the canonical
// derivation is the SSOT and is shared with every other folder writer.
func upsertFolderToDB(ctx context.Context, root *wiring.ComposeRoot, folderID, path, source, groupName, lang string) error {
	if root == nil || root.Repos == nil || root.Repos.ClipsRepo == nil {
		return fmt.Errorf("sync-outros: canonical clips repository is required")
	}
	now := time.Now().UTC()
	meta := map[string]any{
		"is_folder": true,
	}
	if lang != "" {
		meta["language"] = lang
	}
	metaJSON, _ := json.Marshal(meta)

	return root.Repos.ClipsRepo.UpsertFolder(ctx, &detail.ClipFolder{
		ID:         "clipfolder_outro_" + folderID,
		Source:     source,
		FolderID:   folderID,
		FolderPath: path,
		Group:      groupName,
		Metadata:   string(metaJSON),
		CreatedAt:  now,
		UpdatedAt:  now,
	})
}

func upsertFileToDB(ctx context.Context, root *wiring.ComposeRoot, fileID, name, groupName, lang, driveLink, downloadLink string) error {
	if root == nil || root.CanonicalAssetWriter == nil {
		return fmt.Errorf("sync-outros: canonical asset writer is required")
	}
	committer, ok := root.CanonicalAssetWriter.(persistence.AssetCommitter)
	if !ok || committer == nil {
		return fmt.Errorf("sync-outros: canonical asset committer is unavailable")
	}
	// Setup tags
	tags := []string{"outro", groupName, lang}
	tagsNorm := strings.Join(tags, " ")

	_, err := committer.CommitAsset(ctx, persistence.AssetCommitRequest{
		AssetID: fileID, Source: "outro", Name: name, Filename: name,
		MediaType: "video", Category: "outro", GroupName: groupName,
		ContentHash: "", LifecycleState: "ACTIVE", IndexState: "",
		LocalPath: "", FolderID: "", FolderPath: "", ThumbnailURL: "",
		DownloadLink: downloadLink, SourceURL: driveLink, Metadata: asset.TypedMetadata{
			Category: "outro", SourceProvider: "drive", Extra: map[string]any{
				"language": lang, "group_name": groupName, "drive_link": driveLink,
				"download_link": downloadLink, "tags_norm": tagsNorm,
			}, Tags: tags,
		}, Locations: []persistence.LocationCommit{{Kind: "drive", ExternalID: fileID, URI: "drive://" + fileID, WebViewLink: driveLink, DownloadURL: downloadLink, IsPrimary: true}},
		EmitIndexEvent: false, RequestedAt: time.Now().UTC(),
	})
	return err
}
