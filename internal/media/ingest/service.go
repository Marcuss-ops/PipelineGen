package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"

	"velox/go-master/internal/config"
	"velox/go-master/internal/core/lifecycle"
	"velox/go-master/internal/pkg/hashutil"
	"velox/go-master/internal/upload/drive"
)

type Pipeline struct {
	Kind          Kind
	DefaultSource string
	RootFolderID  string
	RootFolder    func(*Request) string
	Lifecycle     *lifecycle.Service
}

type Service struct {
	cfg       *config.Config
	log       *zap.Logger
	client    *http.Client
	driveUp   *drive.Uploader
	pipelines map[Kind]*Pipeline
	imagesDir string
	tempDir   string
}

func NewService(cfg *config.Config, log *zap.Logger, driveSvc *gdrive.Service, pipelines map[Kind]*Pipeline) *Service {
	return &Service{
		cfg:       cfg,
		log:       log,
		client:    &http.Client{Timeout: 90 * time.Second},
		driveUp:   &drive.Uploader{Service: driveSvc, Log: log},
		pipelines: pipelines,
		imagesDir: cfg.Storage.ImagesPath(),
		tempDir:   cfg.Storage.TempPath(),
	}
}

func (s *Service) Ingest(ctx context.Context, req *Request) (*Result, error) {
	if req == nil {
		return nil, fmt.Errorf("request is required")
	}

	kind := normalizeKind(req.Kind)
	if kind == "" {
		return nil, fmt.Errorf("kind is required")
	}

	pipeline := s.pipelines[kind]
	if pipeline == nil || pipeline.Lifecycle == nil {
		return nil, fmt.Errorf("ingest pipeline not configured for kind: %s", kind)
	}

	localPath, filename, cleanup, err := s.acquireLocalPath(ctx, kind, req)
	if err != nil {
		return nil, err
	}
	if cleanup != nil {
		defer cleanup()
	}

	if kind == KindImage {
		localPath, filename, cleanup, err = s.materializeImage(localPath, filename, req)
		if err != nil {
			return nil, err
		}
		if cleanup != nil {
			defer cleanup()
		}
	}

	fileHash, err := hashutil.MD5File(localPath)
	if err != nil {
		return nil, fmt.Errorf("failed to hash media file: %w", err)
	}

	source := strings.TrimSpace(req.Source)
	if source == "" {
		source = pipeline.DefaultSource
	}
	if source == "" {
		source = string(kind)
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = strings.TrimSpace(req.Filename)
	}
	if name == "" {
		name = strings.TrimSpace(filename)
	}
	if name == "" {
		name = strings.TrimSpace(req.SourceID)
	}
	if name == "" {
		name = strings.TrimSpace(fileHash)
	}

	if filename == "" {
		filename = strings.TrimSpace(req.Filename)
	}
	if filename == "" {
		filename = filepath.Base(localPath)
	}
	if filename == "" {
		filename = name
	}

	sourceID := strings.TrimSpace(req.SourceID)
	if sourceID == "" {
		sourceID = strings.TrimSpace(req.URL)
	}
	if sourceID == "" {
		sourceID = strings.TrimSpace(req.LocalPath)
	}
	if sourceID == "" {
		sourceID = fileHash
	}

	rootFolderID := pipeline.RootFolderID
	if pipeline.RootFolder != nil {
		rootFolderID = pipeline.RootFolder(req)
	}
	resolvedFolderID, resolvedFolderPath, err := s.resolveDriveFolder(ctx, kind, rootFolderID, req)
	if err != nil {
		return nil, err
	}

	metadata := mergeMetadata(req.Metadata, map[string]any{
		"kind":          string(kind),
		"source":        source,
		"source_id":     sourceID,
		"filename":      filename,
		"local_path":    localPath,
		"folder_id":     resolvedFolderID,
		"folder_path":   resolvedFolderPath,
		"file_hash":     fileHash,
		"content_hash":  fileHash,
		"source_url":    req.URL,
		"drive_link":    req.DriveLink,
		"drive_file_id": req.DriveFileID,
		"download_link": req.DownloadLink,
		"tags":          req.Tags,
	})
	metaJSON, _ := json.Marshal(metadata)

	id := buildAssetID(kind, fileHash)
	input := &lifecycle.FinalizeInput{
		ID:           id,
		Name:         name,
		Filename:     filename,
		Kind:         toAssetKind(kind),
		Source:       source,
		SourceID:     sourceID,
		Group:        strings.TrimSpace(req.Group),
		Subfolder:    strings.TrimSpace(req.Subfolder),
		LocalPath:    localPath,
		FolderID:     resolvedFolderID,
		FolderPath:   resolvedFolderPath,
		DriveLink:    strings.TrimSpace(req.DriveLink),
		DriveFileID:  strings.TrimSpace(req.DriveFileID),
		DownloadLink: strings.TrimSpace(req.DownloadLink),
		FileHash:     fileHash,
		Metadata:     string(metaJSON),
		Duration:     req.Duration,
		RequireLocal: true,
		RequireHash:  true,
		RequireDrive: resolvedFolderID != "",
		VerifyDB:     true,
	}

	result, err := pipeline.Lifecycle.ProcessAsset(ctx, input, fileHash)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("empty ingest result")
	}
	if !result.OK {
		return &Result{
			OK:           false,
			Status:       result.Status,
			Kind:         string(kind),
			ID:           id,
			Source:       source,
			SourceID:     sourceID,
			Name:         name,
			Filename:     filename,
			FolderID:     resolvedFolderID,
			FolderPath:   resolvedFolderPath,
			LocalPath:    localPath,
			DriveLink:    result.DriveLink,
			DriveFileID:  result.DriveFileID,
			DownloadLink: result.DownloadLink,
			FileHash:     fileHash,
			ContentHash:  fileHash,
			Metadata:     metadata,
		}, nil
	}

	status := result.Status
	if status == "" {
		status = "processed"
	}

	return &Result{
		OK:               true,
		Status:           status,
		Kind:             string(kind),
		ID:               id,
		Source:           source,
		SourceID:         sourceID,
		Name:             name,
		Filename:         filename,
		FolderID:         resolvedFolderID,
		FolderPath:       resolvedFolderPath,
		LocalPath:        localPath,
		DriveLink:        result.DriveLink,
		DriveFileID:      result.DriveFileID,
		DownloadLink:     result.DownloadLink,
		FileHash:         fileHash,
		ContentHash:      fileHash,
		SkippedDuplicate: status == "skipped_duplicate" || status == "would_skip_duplicate",
		Metadata:         metadata,
	}, nil
}
