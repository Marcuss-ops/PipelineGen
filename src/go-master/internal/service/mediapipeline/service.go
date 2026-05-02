package mediapipeline

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/service/drivedestination"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/media/downloader"
	"velox/go-master/pkg/media/ffmpeg"
)

func NewService(
	ytdlpDownloader *downloader.YTDLPDownloader,
	ffmpegProcessor *ffmpeg.Processor,
	driveUploader *drive.Uploader,
	driveDestination *drivedestination.Service,
	clipsRepo *clips.Repository,
	opts ...Option,
) *Service {
	s := &Service{
		ytdlpDownloader:  ytdlpDownloader,
		ffmpegProcessor:  ffmpegProcessor,
		driveUploader:    driveUploader,
		driveDestination: driveDestination,
		clipsRepo:        clipsRepo,
		idGenerator:      &defaultIDGenerator{},
		downloadOutputDir: "/tmp/mediapipeline/downloads",
		processOutputDir:  "/tmp/mediapipeline/processed",
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

type Option func(*Service)

func WithOutputDirs(downloadDir, processDir string) Option {
	return func(s *Service) {
		if downloadDir != "" {
			s.downloadOutputDir = downloadDir
		}
		if processDir != "" {
			s.processOutputDir = processDir
		}
	}
}

func (s *Service) Run(ctx context.Context, req *PipelineRequest) (*PipelineResponse, error) {
	var resolvedDest *ResolvedDestination

	if req.UploadDrive && s.driveDestination != nil {
		dest, err := s.resolveDestination(ctx, req.Destination)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve destination: %w", err)
		}
		resolvedDest = dest
	}

	items := s.expandWorkItems(req)

	for _, item := range items {
		if err := s.validateItem(item); err != nil {
			item.Fail(err)
			continue
		}

		if err := s.downloadOrCut(ctx, item, req); err != nil {
			item.Fail(err)
			continue
		}

		if err := s.process(ctx, item, req.Processing); err != nil {
			item.Fail(err)
			continue
		}

		if err := s.hashItem(item); err != nil {
			item.Fail(err)
			continue
		}

		if req.UploadDrive && s.driveUploader != nil && resolvedDest != nil {
			if err := s.uploadItem(ctx, item, resolvedDest); err != nil {
				item.Fail(err)
				continue
			}
		}

		if req.SaveDB && s.clipsRepo != nil {
			if err := s.persistItem(ctx, item, req, resolvedDest); err != nil {
				item.Fail(err)
				continue
			}
		}

		item.Status = "processed"
	}

	return &PipelineResponse{Items: items}, nil
}

func (s *Service) expandWorkItems(req *PipelineRequest) []*WorkItem {
	var items []*WorkItem

	if len(req.Segments) > 0 {
		for _, seg := range req.Segments {
			item := &WorkItem{
				ID:          s.idGenerator.GenerateID(seg.SourceURL, req),
				Name:        seg.Name,
				SourceURL:   seg.SourceURL,
				SegmentSpec: &seg,
				Tags:        seg.Tags,
				Status:      "pending",
			}
			items = append(items, item)
		}
	} else if len(req.URLs) > 0 {
		for _, url := range req.URLs {
			item := &WorkItem{
				ID:        s.idGenerator.GenerateID(url.URL, req),
				Name:      url.Name,
				SourceURL: url.URL,
				Tags:      req.Tags,
				Status:    "pending",
			}
			items = append(items, item)
		}
	}

	return items
}

func (s *Service) resolveDestination(ctx context.Context, dest DestinationSpec) (*ResolvedDestination, error) {
	if s.driveDestination == nil {
		return nil, fmt.Errorf("drive destination resolver not configured")
	}

	req := &drivedestination.Request{
		Group:           dest.Group,
		FolderID:        dest.FolderID,
		FolderPath:      dest.FolderPath,
		SubfolderName:   dest.SubfolderName,
		CreateSubfolder: dest.CreateSubfolder,
	}

	resolved, err := s.driveDestination.Resolve(ctx, req)
	if err != nil {
		return nil, err
	}

	return &ResolvedDestination{
		FolderID:   resolved.FolderID,
		FolderPath: resolved.FolderPath,
		Group:      resolved.Group,
	}, nil
}

func (s *Service) validateItem(item *WorkItem) error {
	if item.SourceURL == "" {
		return fmt.Errorf("source URL is required")
	}
	return nil
}

type defaultIDGenerator struct{}

func (g *defaultIDGenerator) GenerateID(sourceURL string, req *PipelineRequest) string {
	return uuid.New().String()
}
