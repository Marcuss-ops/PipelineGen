package mediapipeline

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"velox/go-master/pkg/media/downloader"
	"velox/go-master/pkg/media/ffmpeg"
)

func NewService(
	ytdlpDownloader *downloader.YTDLPDownloader,
	ffmpegProcessor *ffmpeg.Processor,
	opts ...Option,
) *Service {
	s := &Service{
		ytdlpDownloader:  ytdlpDownloader,
		ffmpegProcessor:  ffmpegProcessor,
		idGenerator:      &stableIDGenerator{},
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
