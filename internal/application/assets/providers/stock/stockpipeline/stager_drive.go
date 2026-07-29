package stockpipeline

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

func isDriveURL(rawURL string) bool {
	return strings.Contains(rawURL, "drive.google.com")
}

func extractDriveFileID(rawURL string) (string, error) {
	return urlutil.FileIDFromDriveLink(rawURL)
}

func (s *StockStager) stageFromDrive(ctx context.Context, ref assets.SourceRef, outputPath string) (*assets.StagedAsset, error) {
	if s.driveReader == nil {
		return nil, fmt.Errorf("stock stager: drive reader not wired")
	}

	fs, fsErr := s.fs()
	if fsErr != nil {
		return nil, fsErr
	}

	fileID, fileErr := extractDriveFileID(ref.URL)
	if fileErr != nil || fileID == "" {
		folderID := urlutil.FolderIDFromDriveLink(ref.URL)
		if folderID == "" {
			return nil, fmt.Errorf("stock stager: could not extract Drive file or folder ID from %q", ref.URL)
		}
		files, listErr := s.driveReader.ListFiles(ctx, folderID)
		if listErr != nil {
			return nil, fmt.Errorf("stock stager: list drive folder %q: %w", folderID, listErr)
		}
		for _, f := range files {
			if strings.HasPrefix(f.MimeType, "video/") {
				fileID = f.ID
				break
			}
		}
		if fileID == "" {
			return nil, fmt.Errorf("stock stager: no video file found in Drive folder %q", folderID)
		}
	}

	body, _, dlErr := s.driveReader.DownloadFile(ctx, fileID)
	if dlErr != nil {
		return nil, fmt.Errorf("stock stager: drive download file %q: %w", fileID, dlErr)
	}
	defer body.Close()

	f, createErr := fs.Create(outputPath)
	if createErr != nil {
		return nil, fmt.Errorf("stock stager: create output file: %w", createErr)
	}

	if _, copyErr := io.Copy(f, body); copyErr != nil {
		f.Close()
		return nil, fmt.Errorf("stock stager: write downloaded file: %w", copyErr)
	}

	if closeErr := f.Close(); closeErr != nil {
		return nil, fmt.Errorf("stock stager: close output file: %w", closeErr)
	}

	fi, statErr := fs.Stat(outputPath)
	if statErr != nil {
		return nil, fmt.Errorf("stock stager: stat downloaded file: %w", statErr)
	}

	return &assets.StagedAsset{
		LocalPath: outputPath,
		Bytes:     fi.Size(),
	}, nil
}
