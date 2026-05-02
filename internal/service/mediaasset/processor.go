package mediaasset

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/hashutil"
	"velox/go-master/pkg/media/downloader"
	"velox/go-master/pkg/media/ffmpeg"
)

type Processor struct {
	dl        *downloader.YTDLPDownloader
	ffmpeg    *ffmpeg.Processor
	driveSvc  *driveapi.Service
	log       *zap.Logger
	dataDir   string
	tempDir   string
	videoCfg  ffmpeg.NormalizeOptions
}

type ProcessorConfig struct {
	DataDir  string
	TempDir  string
	VideoCfg ffmpeg.NormalizeOptions
}

func NewProcessor(
	dl *downloader.YTDLPDownloader,
	ff *ffmpeg.Processor,
	driveSvc *driveapi.Service,
	log *zap.Logger,
	cfg ProcessorConfig,
) *Processor {
	return &Processor{
		dl:       dl,
		ffmpeg:   ff,
		driveSvc: driveSvc,
		log:      log,
		dataDir:  cfg.DataDir,
		tempDir:  cfg.TempDir,
		videoCfg: cfg.VideoCfg,
	}
}

func (p *Processor) DownloadProcessUpload(ctx context.Context, input AssetInput) (*AssetResult, error) {
	result := &AssetResult{
		ID:     input.ID,
		Status: "failed",
	}

	tmpDir := filepath.Join(p.dataDir, p.tempDir)
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		p.log.Error("failed to create temp directory", zap.String("dir", tmpDir), zap.Error(err))
		tmpDir = os.TempDir()
	}

	saveDir := input.OutputDir
	if saveDir == "" {
		saveDir = filepath.Join(p.dataDir, "mediaassets", SafeName(input.Term))
	}
	if err := os.MkdirAll(saveDir, 0755); err != nil {
		p.log.Error("failed to create save directory", zap.String("dir", saveDir), zap.Error(err))
		saveDir = tmpDir
	}

	rawPath := TmpPath(tmpDir, fmt.Sprintf("raw_%s.mp4", input.ID))
	finalFilename := SafeName(input.Name) + "_" + input.ID + ".mp4"
	processedPath := OutputPath(saveDir, finalFilename)

	// Build download request
	dlReq := &downloader.DownloadRequest{
		URL:             input.SourceURL,
		OutputPath:      rawPath,
		ForceKeyframes:  input.ForceKeyframes,
		DownloadSections: input.DownloadSections,
	}
	if len(input.DownloadSections) > 0 {
		dlReq.Format = "bv*[height<=1080][ext=mp4]+ba[ext=m4a]/b[height<=1080][ext=mp4]/best[height<=1080]"
		dlReq.MergeFormat = "mp4"
		dlReq.NoPlaylist = true
		dlReq.Timeout = 10 * time.Minute
	}

	p.log.Info("downloading asset", zap.String("id", input.ID), zap.String("url", input.SourceURL), zap.Strings("sections", input.DownloadSections))
	if err := p.dl.Download(ctx, dlReq); err != nil {
		result.Error = fmt.Sprintf("download failed: %v", err)
		return result, err
	}

	actualRawPath := ResolveDownloadedFile(rawPath)
	if actualRawPath != rawPath {
		p.log.Info("resolved actual download path", zap.String("expected", rawPath), zap.String("actual", actualRawPath))
	}

	// Determine normalize options
	shouldNormalize := input.Normalize == nil || *input.Normalize
	if shouldNormalize {
		opts := p.videoCfg
		opts.KeepAudio = input.KeepAudio
		opts.DisableDuration = input.DisableDuration

		p.log.Info("processing video", zap.String("id", input.ID), zap.String("output", processedPath), zap.Bool("disable_duration", opts.DisableDuration))
		if err := p.ffmpeg.Normalize(ctx, actualRawPath, processedPath, opts); err != nil {
			_ = os.Remove(actualRawPath)
			result.Error = fmt.Sprintf("process failed: %v", err)
			return result, err
		}
	} else {
		p.log.Info("skipping normalization as requested", zap.String("id", input.ID))
		processedPath = actualRawPath
	}

	p.log.Info("calculating file hash", zap.String("id", input.ID), zap.String("path", processedPath))
	fileHash, err := hashutil.MD5File(processedPath)
	if err != nil {
		_ = os.Remove(actualRawPath)
		_ = os.Remove(processedPath)
		result.Error = fmt.Sprintf("hash failed: %v", err)
		return result, err
	}
	result.FileHash = fileHash
	result.LocalPath = processedPath
	result.Filename = filepath.Base(processedPath)

	_ = os.Remove(actualRawPath)

	if p.driveSvc != nil && input.FolderID != "" {
		filename := filepath.Base(processedPath)
		p.log.Info("uploading to Drive", zap.String("id", input.ID), zap.String("filename", filename))
		uploader := &drive.Uploader{Service: p.driveSvc, Log: p.log}
		uploadResult, err := uploader.UploadFile(ctx, processedPath, input.FolderID, filename)
		if err != nil {
			result.Error = fmt.Sprintf("upload failed: %v", err)
			return result, err
		}
		result.DriveLink = uploadResult.WebViewLink
		result.DownloadLink = "https://drive.google.com/uc?id=" + uploadResult.FileID
		p.log.Info("drive upload success", zap.String("id", input.ID), zap.String("file_id", uploadResult.FileID))
	}

	result.Status = "processed"
	return result, nil
}
