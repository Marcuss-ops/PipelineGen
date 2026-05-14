package artlist

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"

	"velox/go-master/pkg/models"
)

// ClipProcessorService gestisce il processing dei clip Artlist
type ClipProcessorService struct {
	svc *Service
}

// NewClipProcessorService crea un nuovo processore di clip
func NewClipProcessorService(svc *Service) *ClipProcessorService {
	return &ClipProcessorService{svc: svc}
}

// ProcessClip elabora un singolo clip
func (p *ClipProcessorService) ProcessClip(ctx context.Context, clip *models.Clip, dest *DestinationInfo) error {
	if clip == nil {
		return fmt.Errorf("clip is nil")
	}
	if dest == nil {
		return fmt.Errorf("destination is nil")
	}

	if err := p.ensureClipFile(ctx, clip); err != nil {
		return fmt.Errorf("failed to ensure clip file: %w", err)
	}

	if err := p.uploadToDrive(ctx, clip, dest.FolderID); err != nil {
		return fmt.Errorf("failed to upload to drive: %w", err)
	}

	if err := p.indexClip(ctx, clip); err != nil {
		p.svc.log.Warn("clip indexing failed", zap.String("clip_id", clip.ID), zap.Error(err))
	}

	return nil
}

func (p *ClipProcessorService) ensureClipFile(ctx context.Context, clip *models.Clip) error {
	if clip.LocalPath != "" {
		if _, err := os.Stat(clip.LocalPath); err == nil {
			return nil
		}
	}

	if clip.ExternalURL == "" {
		return fmt.Errorf("clip has no external URL")
	}

	downloadDir := filepath.Join(p.svc.cfg.Storage.DataDir, "artlist")
	if err := os.MkdirAll(downloadDir, 0755); err != nil {
		return fmt.Errorf("failed to create download dir: %w", err)
	}

	filename := fmt.Sprintf("%s.mp4", clip.ID)
	localPath := filepath.Join(downloadDir, filename)

	if _, err := os.Stat(localPath); err == nil {
		clip.LocalPath = localPath
		return nil
	}

	cmd := exec.CommandContext(ctx, "yt-dlp", "-o", localPath, "--cookies-from-browser", "chrome", clip.ExternalURL)
	cmd.Dir = p.svc.nodeScraperDir
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("yt-dlp failed: %w", err)
	}

	clip.LocalPath = localPath
	return nil
}

func (p *ClipProcessorService) uploadToDrive(ctx context.Context, clip *models.Clip, folderID string) error {
	if clip.LocalPath == "" {
		return fmt.Errorf("clip has no local path")
	}

	file, err := os.Open(clip.LocalPath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	f := &driveapi.File{
		Name:    filepath.Base(clip.LocalPath),
		Parents: []string{folderID},
	}

	uploaded, err := p.svc.driveSvc.Files.Create(f).Media(file).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to upload: %w", err)
	}

	clip.DriveLink = fmt.Sprintf("https://drive.google.com/file/d/%s/view", uploaded.Id)
	clip.DriveFileID = uploaded.Id
	return nil
}

func (p *ClipProcessorService) indexClip(ctx context.Context, clip *models.Clip) error {
	if p.svc.clipIndexer == nil {
		return nil
	}
	return p.svc.clipIndexer.IndexClip(ctx, clip.ID)
}
