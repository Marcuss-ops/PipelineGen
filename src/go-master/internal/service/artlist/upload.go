package artlist

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"

	"velox/go-master/pkg/models"
	"velox/go-master/pkg/pathutil"
)

func (s *Service) UploadClipToDrive(ctx context.Context, clipID string, req *UploadClipToDriveRequest) (*UploadClipToDriveResponse, error) {
	clip, err := s.clipsRepo.GetClip(ctx, clipID)
	if err != nil {
		return nil, err
	}

	resp := &UploadClipToDriveResponse{OK: true, ClipID: clipID}

	if s.driveService == nil {
		resp.Error = "drive service not configured"
		return resp, fmt.Errorf("drive service not configured")
	}

	folderID := req.FolderID
	if folderID == "" {
		folderID = s.driveService.GetDriveFolderID()
	}

	// Determine file path: use LocalPath if available, otherwise construct from name
	localPath := determineClipLocalPath(s, clip)

	// Open the local file for upload
	f, err := os.Open(localPath)
	if err != nil {
		resp.Error = fmt.Sprintf("failed to open local file: %v", err)
		s.log.Error("failed to open file for drive upload", zap.String("clip_id", clipID), zap.String("path", localPath), zap.Error(err))
		return resp, err
	}
	defer f.Close()

	file := &driveapi.File{Name: clip.Filename}
	if folderID != "" {
		file.Parents = []string{folderID}
	}

	if s.driveService == nil {
		err = fmt.Errorf("drive service not configured")
		return resp, err
	}
	driveClient := s.driveService.GetDriveClient()
	created, err := driveClient.Files.Create(file).Context(ctx).Media(f).Fields("id,webViewLink,md5Checksum").Do()
	if err != nil {
		resp.Error = err.Error()
		return resp, err
	}

	if created != nil {
		resp.DriveLink = created.WebViewLink
		resp.DownloadLink = "https://drive.google.com/uc?id=" + created.Id

		// Update clip with drive info
		clip.DriveLink = created.WebViewLink
		clip.UpdatedAt = time.Now().UTC()
		if created.Md5Checksum != "" {
			clip.FileHash = created.Md5Checksum
		}
		_ = s.clipsRepo.UpsertClip(ctx, clip)
	}

	return resp, nil
}

func determineClipLocalPath(s *Service, clip *models.Clip) string {
	localPath := strings.TrimSpace(clip.LocalPath)
	if localPath != "" {
		return localPath
	}

	saveDir := filepath.Join(s.cfg.Storage.DataDir, "artlist", pathutil.SafeFolderName(clip.Name))
	safeName := pathutil.SafeFolderName(clip.Name)
	return filepath.Join(saveDir, fmt.Sprintf("%s_%ds_%s.mp4", safeName, s.cfg.Video.Duration, clip.ID))
}
