package foldermemory

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/filesystem"
	assets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/channels"
)

type Service struct {
	log       *zap.Logger
	clipsRepo *assets.ClipsRepository
}

func NewService(log *zap.Logger, repo *assets.ClipsRepository) *Service {
	return &Service{
		log:       log,
		clipsRepo: repo,
	}
}

// LoadManifest loads clip manifest from JSON file
func (s *Service) LoadManifest(manifestPath string) (*asset.ClipManifest, error) {
	manifest := &asset.ClipManifest{
		Clips: []asset.ClipManifestItem{},
	}
	if manifestPath == "" {
		return manifest, nil
	}
	if err := filesystem.ReadJSON(manifestPath, manifest); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return manifest, nil
		}
		return nil, fmt.Errorf("failed to parse manifest JSON: %w", err)
	}
	return manifest, nil
}

// SaveManifest saves clip manifest to JSON file
func (s *Service) SaveManifest(manifestPath string, manifest *asset.ClipManifest) error {
	manifest.UpdatedAt = time.Now().UTC()
	return filesystem.WriteJSON(manifestPath, manifest, true)
}

// UpdateManifestTXT creates or updates the human-readable manifest TXT file
func (s *Service) UpdateManifestTXT(folder *asset.ClipFolder, manifest *asset.ClipManifest) error {
	if folder == nil || folder.ManifestTXTPath == "" || manifest == nil {
		return fmt.Errorf("folder, manifest or manifest path is nil")
	}

	var sb strings.Builder
	sb.WriteString("📋 CLIP FOLDER MANIFEST\n")
	sb.WriteString(strings.Repeat("=", 80) + "\n")
	sb.WriteString(fmt.Sprintf("Folder ID:  %s\n", folder.ID))
	sb.WriteString(fmt.Sprintf("Local:      %s\n", folder.LocalFolderPath))
	sb.WriteString(fmt.Sprintf("Source:     %s\n", folder.SourceURL))
	if folder.VideoID != "" {
		sb.WriteString(fmt.Sprintf("Video ID:   %s\n", folder.VideoID))
	}
	if folder.FolderPath != "" {
		sb.WriteString(fmt.Sprintf("Drive:      %s\n", folder.FolderPath))
	}
	if folder.FolderID != "" {
		sb.WriteString(fmt.Sprintf("Drive ID:   %s\n", folder.FolderID))
	}
	sb.WriteString(fmt.Sprintf("📊 Totale clip: %d (Processate: %d, Fallite: %d, Saltate: %d)\n",
		manifest.Stats.ClipCount, manifest.Stats.ProcessedCount, manifest.Stats.FailedCount, manifest.Stats.SkippedCount))
	sb.WriteString(fmt.Sprintf("Aggiornato: %s\n", time.Now().Format("2006-01-02 15:04:05")))
	sb.WriteString("\n")

	for i, item := range manifest.Clips {
		sb.WriteString(fmt.Sprintf("%d. [%s]\n", i+1, item.Name))
		sb.WriteString(fmt.Sprintf("   ⏱️  Tempo:  %s - %s (%ds)\n", item.Start, item.End, item.DurationSeconds))
		sb.WriteString(fmt.Sprintf("   📊 Stato:  %s\n", item.Status))
		if item.DriveLink != "" {
			sb.WriteString(fmt.Sprintf("   🔗 Drive:  %s\n", item.DriveLink))
		}
		if item.LocalPath != "" {
			sb.WriteString(fmt.Sprintf("   📁 File:   %s\n", filepath.Base(item.LocalPath)))
		}
		if item.LegacyFileMD5 != "" {
			sb.WriteString(fmt.Sprintf("   #  Hash:   %s\n", item.LegacyFileMD5))
		}
		sb.WriteString("\n")
	}

	return os.WriteFile(folder.ManifestTXTPath, []byte(sb.String()), 0644)
}

// ComputeManifestStats calculates clip counts from manifest items
func (s *Service) ComputeManifestStats(manifest *asset.ClipManifest) asset.ClipFolderStats {
	stats := asset.ClipFolderStats{}
	if manifest == nil {
		return stats
	}

	processedCount := 0
	failedCount := 0
	skippedCount := 0

	for _, item := range manifest.Clips {
		switch item.Status {
		case "processed":
			processedCount++
		case "skipped_existing":
			skippedCount++
		case "failed", "upload_failed", "download_failed", "hash_failed", "process_failed":
			failedCount++
		default:
			skippedCount++
		}
	}

	stats.ClipCount = len(manifest.Clips)
	stats.ProcessedCount = processedCount
	stats.FailedCount = failedCount
	stats.SkippedCount = skippedCount
	return stats
}

// UpsertFolder creates or updates a clip folder in DB
func (s *Service) UpsertFolder(ctx context.Context, folder *asset.ClipFolder) error {
	if s.clipsRepo == nil {
		return fmt.Errorf("clips repository not available")
	}
	folder.UpdatedAt = time.Now().UTC()
	// ARCH-ALLOWLIST: clips-ssot-only
	return s.clipsRepo.UpsertFolder(ctx, folder)
}

// GetFolder retrieves a clip folder by ID
func (s *Service) GetFolder(ctx context.Context, folderID string) (*asset.ClipFolder, error) {
	if s.clipsRepo == nil {
		return nil, fmt.Errorf("clips repository not available")
	}
	return s.clipsRepo.GetFolder(ctx, folderID)
}
