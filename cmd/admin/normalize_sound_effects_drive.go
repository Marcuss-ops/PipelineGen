package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
)

// runNormalizeSoundEffectsDrive verifies the actual remote audio duration and
// replaces only remote files longer than two seconds in their current folder.
func runNormalizeSoundEffectsDrive(args []string) error {
	rootFolder := soundEffectsDriveFolderID
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		rootFolder = strings.TrimSpace(args[0])
	}
	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()
	root, _, rootCleanup, err := app.InitComposition(cfg, log)
	if err != nil {
		return fmt.Errorf("initialize composition: %w", err)
	}
	defer rootCleanup()
	if root == nil || root.Drive == nil || root.Drive.Reader == nil || root.Drive.Publisher == nil {
		return fmt.Errorf("Drive reader and publisher are required")
	}
	adminUpload, err := delivery.NewAdminUploadService(root.Drive.Publisher)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Hour)
	defer cancel()
	tempDir, err := os.MkdirTemp("", "velox-sfx-drive-")
	if err != nil {
		return fmt.Errorf("create temporary directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	files, err := listDriveAudioRecursive(ctx, root.Drive.Reader, rootFolder)
	if err != nil {
		return err
	}
	checked, changed := 0, 0
	for _, item := range files {
		checked++
		localPath := filepath.Join(tempDir, fmt.Sprintf("%s%s", item.file.ID, filepath.Ext(item.file.Name)))
		body, _, err := root.Drive.Reader.DownloadFile(ctx, item.file.ID)
		if err != nil {
			return fmt.Errorf("download %q: %w", item.file.Name, err)
		}
		out, err := os.Create(localPath)
		if err == nil {
			_, err = io.Copy(out, body)
		}
		closeErr := body.Close()
		if err == nil {
			err = closeErr
		}
		if err == nil {
			err = out.Close()
		} else {
			_ = out.Close()
		}
		if err != nil {
			return fmt.Errorf("save %q: %w", item.file.Name, err)
		}
		duration, err := probeSoundEffectDuration(ctx, localPath)
		if err != nil {
			return fmt.Errorf("probe remote %q: %w", item.file.Name, err)
		}
		if duration <= 2*time.Second {
			continue
		}
		target := trimTargetSeconds(localPath, 2)
		if err := trimSoundEffect(ctx, localPath, target); err != nil {
			return fmt.Errorf("trim remote %q: %w", item.file.Name, err)
		}
		newDuration, err := probeSoundEffectDuration(ctx, localPath)
		if err != nil || newDuration > 2*time.Second {
			return fmt.Errorf("remote trim validation failed for %q: duration=%.3fs err=%v", item.file.Name, newDuration.Seconds(), err)
		}
		if _, err := adminUpload.Publish(ctx, delivery.AdminUploadCommand{
			LocalPath: localPath,
			FolderID:  item.folderID,
			Filename:  item.file.Name,
		}); err != nil {
			return fmt.Errorf("update remote %q: %w", item.file.Name, err)
		}
		changed++
		fmt.Printf("fixed %.3fs -> %.3fs: %s\n", duration.Seconds(), newDuration.Seconds(), item.file.Name)
	}
	fmt.Printf("Remote SFX checked=%d fixed=%d max_seconds=2.00\n", checked, changed)
	return nil
}

type driveAudioEntry struct {
	file     drive.DriveFileInfo
	folderID string
}

func listDriveAudioRecursive(ctx context.Context, reader drive.Reader, folderID string) ([]driveAudioEntry, error) {
	files, err := reader.ListFiles(ctx, folderID)
	if err != nil {
		return nil, fmt.Errorf("list Drive folder %s: %w", folderID, err)
	}
	result := make([]driveAudioEntry, 0, len(files))
	for _, file := range files {
		if file.MimeType == "application/vnd.google-apps.folder" {
			children, err := listDriveAudioRecursive(ctx, reader, file.ID)
			if err != nil {
				return nil, err
			}
			result = append(result, children...)
			continue
		}
		result = append(result, driveAudioEntry{file: file, folderID: folderID})
	}
	return result, nil
}
