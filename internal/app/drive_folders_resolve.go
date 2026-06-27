// Package app — Drive folder resolution + legacy doc migration
// (PG-006, June 2026).
//
// Extracted from bootstrap.go so the in-scope bootstrap.go stays free
// of `internal/infrastructure/*` imports. These helpers operate on the
// raw Google Drive SDK (`google.golang.org/api/drive/v3`) to resolve
// dynamic root folders and migrate legacy Google Docs from the Scripts
// root into the Generate subfolder. Composition root is the only file
// tree allowed to import the SDK directly; the API layer only sees the
// Drive through typed ports elsewhere.
package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	sqassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"github.com/Marcuss-ops/PipelineGen/pkg/timeutil"

	gdrive "google.golang.org/api/drive/v3"

	"go.uber.org/zap"
)

// resolveDynamicDriveFolders lazily resolves Google Drive root folders
// for each content source (stock, clips, voiceover, scripts, etc.) by
// looking them up via the clips repository first, and falling back to
// GetOrCreateFolder on the Drive SDK. Resulting folder IDs are written
// into cfg.Drive.<X>RootFolder so subsequent bundles use cache-friendly
// short paths instead of re-resolving on every request.
//
// After main resolution, runs a legacy-docs migration that moves
// standalone Google Docs from the Scripts root folder into the Generate
// subfolder so the script-generation flow sees them as candidates
// without conflicting with the new Generate-only contract.
//
// PG-006 (June 2026): moved out of bootstrap.go so the entry-point
// file can stay strictly free of `internal/infrastructure/*` imports.
// Signature unchanged from the previous bootstrap.go version so all
// callers (notably WireServices via initCompositionMinimalWithContext)
// compile against it without further changes.
func resolveDynamicDriveFolders(ctx context.Context, db *sqassets.ClipsRepository, driveClient *gdrive.Service, cfg *config.Config, log *zap.Logger) {
	if cfg.Drive.MediaRootFolder == "" || driveClient == nil || db == nil {
		return
	}

	uploader := &drive.Uploader{Service: driveClient, Log: log}

	resolveFolder := func(name, source string) string {
		id, err := db.LookupDriveFolderIDBySourcePath(ctx, source, name)
		if err != nil {
			log.Warn("Drive folder lookup failed", zap.String("source", source), zap.String("name", name), zap.Error(err))
		}
		if id != "" {
			return id
		}

		id, err = uploader.GetOrCreateFolder(ctx, name, cfg.Drive.MediaRootFolder)
		if err != nil {
			log.Warn("Failed to resolve dynamic folder on Drive", zap.String("name", name), zap.Error(err))
			return ""
		}

		now := timeutil.FormatRFC3339(time.Now())
		if err := db.UpsertDriveFolder(ctx, sqassets.DriveFolderAttrs{
			Source:     source,
			SourceURL:  "https://drive.google.com/drive/folders/" + id,
			FolderID:   id,
			FolderPath: name,
			GroupName:  name,
			CreatedAt:  now,
			UpdatedAt:  now,
		}); err != nil {
			log.Warn("Failed to upsert Drive folder row", zap.String("source", source), zap.String("name", name), zap.Error(err))
		}
		return id
	}

	log.Info("Resolving Google Drive root folders dynamically from MediaRootFolder...", zap.String("root", cfg.Drive.MediaRootFolder))

	if cfg.Drive.StockRootFolder == "" {
		cfg.Drive.StockRootFolder = resolveFolder("Stock", "stock")
	}
	if cfg.Drive.ClipsRootFolder == "" {
		cfg.Drive.ClipsRootFolder = resolveFolder("Clips", "youtube")
	}
	if cfg.Drive.ImagesRootFolder == "" {
		cfg.Drive.ImagesRootFolder = resolveFolder("Immagini", "images")
	}
	if cfg.Drive.VoiceoverRootFolder == "" {
		cfg.Drive.VoiceoverRootFolder = resolveFolder("Voiceover", "voiceover")
	}
	if cfg.Drive.ArtlistRootFolder == "" {
		cfg.Drive.ArtlistRootFolder = resolveFolder("Artlist", "artlist")
	}
	if cfg.Drive.AvatarAIRootFolder == "" {
		cfg.Drive.AvatarAIRootFolder = resolveFolder("Avatar Ai ", "avatar_ai")
	}
	if cfg.Drive.BooksRootFolder == "" {
		cfg.Drive.BooksRootFolder = resolveFolder("Books", "books")
	}
	if cfg.Drive.ScriptsRootFolder == "" {
		cfg.Drive.ScriptsRootFolder = resolveFolder("Scripts", "scripts")
	}
	if cfg.Drive.ScriptsRootFolder != "" {
		resolveScriptsSubfolder := func(name, source string) string {
			id, err := db.LookupDriveFolderIDBySourcePath(ctx, source, name)
			if err != nil {
				log.Warn("Scripts folder lookup failed", zap.String("source", source), zap.String("name", name), zap.Error(err))
			}
			if id != "" {
				return id
			}
			id, err = uploader.GetOrCreateFolder(ctx, name, cfg.Drive.ScriptsRootFolder)
			if err != nil {
				log.Warn("Failed to create scripts subfolder on Drive", zap.String("name", name), zap.Error(err))
				return ""
			}
			now := timeutil.FormatRFC3339(time.Now())
			if err := db.UpsertDriveFolder(ctx, sqassets.DriveFolderAttrs{
				Source:     source,
				SourceURL:  "https://drive.google.com/drive/folders/" + id,
				FolderID:   id,
				FolderPath: name,
				GroupName:  name,
				CreatedAt:  now,
				UpdatedAt:  now,
			}); err != nil {
				log.Warn("Failed to upsert scripts folder row", zap.String("source", source), zap.String("name", name), zap.Error(err))
			}
			return id
		}
		if cfg.Drive.ScriptsGenerateFolder == "" {
			cfg.Drive.ScriptsGenerateFolder = resolveScriptsSubfolder("Generate", "scripts_generate")
		}
	}

	if cfg.Drive.CopertineRootFolder == "" {
		cfg.Drive.CopertineRootFolder = resolveFolder("Copertine", "copertine")
	}
	if cfg.Drive.SoundEffectsRootFolder == "" {
		cfg.Drive.SoundEffectsRootFolder = resolveFolder("Effetti Suoni Online", "sound_effects")
	}

	log.Info("Successfully resolved all dynamic root folders",
		zap.String("stock", cfg.Drive.StockRootFolder),
		zap.String("clips", cfg.Drive.ClipsRootFolder),
		zap.String("images", cfg.Drive.ImagesRootFolder),
		zap.String("voiceover", cfg.Drive.VoiceoverRootFolder),
	)
	migrateLegacyScriptDocs(ctx, driveClient, cfg, log)
}

// migrateLegacyScriptDocs moves standalone Google Docs from the Scripts
// root into the Generate subfolder. Pulled out of resolveDynamicDriveFolders
// so test or repair utilities can invoke migration without re-running the
// full folder resolution pass.
func migrateLegacyScriptDocs(ctx context.Context, driveClient *gdrive.Service, cfg *config.Config, log *zap.Logger) {
	rootID := strings.TrimSpace(cfg.Drive.ScriptsRootFolder)
	targetID := strings.TrimSpace(cfg.Drive.ScriptsGenerateFolder)
	if rootID == "" || targetID == "" || rootID == targetID || driveClient == nil {
		return
	}
	log.Info("checking for legacy docs in Scripts root folder", zap.String("root", rootID), zap.String("target", targetID))
	q := fmt.Sprintf("'%s' in parents and mimeType='application/vnd.google-apps.document' and trashed=false", rootID)
	files, err := driveClient.Files.List().Q(q).Fields("files(id, name)").PageSize(100).Context(ctx).Do()
	if err != nil {
		log.Warn("failed to list legacy docs in Scripts root", zap.Error(err))
		return
	}
	if len(files.Files) == 0 {
		log.Info("no legacy docs to migrate from Scripts root")
		return
	}
	migrated := 0
	for _, f := range files.Files {
		_, err := driveClient.Files.Update(f.Id, nil).RemoveParents(rootID).AddParents(targetID).Fields("id").Context(ctx).Do()
		if err != nil {
			log.Warn("failed to migrate legacy doc", zap.String("doc_id", f.Id), zap.String("name", f.Name), zap.Error(err))
			continue
		}
		migrated++
		log.Info("migrated legacy doc to Generate subfolder", zap.String("doc_id", f.Id), zap.String("name", f.Name))
	}
	log.Info("legacy doc migration complete", zap.Int("migrated", migrated), zap.Int("total", len(files.Files)))
}
