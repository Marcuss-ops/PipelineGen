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

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	filesystem "github.com/Marcuss-ops/PipelineGen/internal/platform/filesystem"
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
func (s *Service) LoadManifest(manifestPath string) (*detail.ClipManifest, error) {
	manifest := &detail.ClipManifest{
		Clips: []detail.ClipManifestItem{},
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
func (s *Service) SaveManifest(manifestPath string, manifest *detail.ClipManifest) error {
	manifest.UpdatedAt = time.Now().UTC()
	return filesystem.WriteJSON(manifestPath, manifest, true)
}

// UpdateManifestTXT creates or updates the human-readable manifest TXT file
func (s *Service) UpdateManifestTXT(folder *detail.ClipFolder, manifest *detail.ClipManifest) error {
	if folder == nil || folder.ManifestTXTPath == "" || manifest == nil {
		return fmt.Errorf("folder, manifest or manifest path is nil")
	}

	var sb strings.Builder
	sb.WriteString("📋 CLIP FOLDER MANIFEST\n")
	sb.WriteString(strings.Repeat("=", 80) + "\n")
	fmt.Fprintf(&sb, "Folder ID:  %s\n", folder.ID)
	fmt.Fprintf(&sb, "Local:      %s\n", folder.LocalFolderPath)
	fmt.Fprintf(&sb, "Source:     %s\n", folder.SourceURL)
	if folder.VideoID != "" {
		fmt.Fprintf(&sb, "Video ID:   %s\n", folder.VideoID)
	}
	if folder.FolderPath != "" {
		fmt.Fprintf(&sb, "Drive:      %s\n", folder.FolderPath)
	}
	if folder.FolderID != "" {
		fmt.Fprintf(&sb, "Drive ID:   %s\n", folder.FolderID)
	}
	fmt.Fprintf(&sb, "📊 Totale clip: %d (Processate: %d, Fallite: %d, Saltate: %d)\n",
		manifest.Stats.ClipCount, manifest.Stats.ProcessedCount, manifest.Stats.FailedCount, manifest.Stats.SkippedCount)
	fmt.Fprintf(&sb, "Aggiornato: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	sb.WriteString("\n")

	for i, item := range manifest.Clips {
		fmt.Fprintf(&sb, "%d. [%s]\n", i+1, item.Name)
		fmt.Fprintf(&sb, "   ⏱️  Tempo:  %s - %s (%ds)\n", item.Start, item.End, item.DurationSeconds)
		fmt.Fprintf(&sb, "   📊 Stato:  %s\n", item.Status)
		if item.DriveLink != "" {
			fmt.Fprintf(&sb, "   🔗 Drive:  %s\n", item.DriveLink)
		}
		if item.LocalPath != "" {
			fmt.Fprintf(&sb, "   📁 File:   %s\n", filepath.Base(item.LocalPath))
		}
		if item.ContentSHA256 != "" {
			fmt.Fprintf(&sb, "   #  Hash:   %s\n", item.ContentSHA256)
		}
		sb.WriteString("\n")
	}

	return os.WriteFile(folder.ManifestTXTPath, []byte(sb.String()), 0644)
}

// ComputeManifestStats calculates clip counts from manifest items
func (s *Service) ComputeManifestStats(manifest *detail.ClipManifest) detail.ClipFolderStats {
	stats := detail.ClipFolderStats{}
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
func (s *Service) UpsertFolder(ctx context.Context, folder *detail.ClipFolder) error {
	if s.clipsRepo == nil {
		return fmt.Errorf("clips repository not available")
	}
	folder.UpdatedAt = time.Now().UTC()
	// ARCH-ALLOWLIST: clips-ssot-only
	return s.clipsRepo.UpsertFolder(ctx, folder)
}

// GetFolder retrieves a clip folder by ID
func (s *Service) GetFolder(ctx context.Context, folderID string) (*detail.ClipFolder, error) {
	if s.clipsRepo == nil {
		return nil, fmt.Errorf("clips repository not available")
	}
	return s.clipsRepo.GetFolder(ctx, folderID)
}
