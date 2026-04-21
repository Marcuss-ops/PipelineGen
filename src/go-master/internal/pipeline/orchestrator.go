package pipeline

import (
	"context"
	"fmt"

	"go.uber.org/zap"
	"velox/go-master/pkg/logger"
)

// Orchestrator compatta tutte le fasi della pipeline in una singola interfaccia.
type Orchestrator struct {
	Fetcher    Fetcher
	Analyzer   Analyzer
	Downloader Downloader
	Uploader   Uploader
	Store      StateStore
}

// StateStore definisce come l'orchestratore aggiorna i record persistenti.
type StateStore interface {
	MarkAsProcessed(ctx context.Context, videoID string, status string) error
	SaveClipInfo(ctx context.Context, videoID, driveID string, start, dur int) error
}

func NewOrchestrator(f Fetcher, a Analyzer, d Downloader, u Uploader, s StateStore) *Orchestrator {
	return &Orchestrator{
		Fetcher:    f,
		Analyzer:   a,
		Downloader: d,
		Uploader:   u,
		Store:      s,
	}
}

// Run esegue l'intero flusso: Fetch -> Analyze -> Download -> Upload -> Update DB.
func (o *Orchestrator) Run(ctx context.Context, videoID string) error {
	logger.Info("Starting compact pipeline run", zap.String("video_id", videoID))

	info, err := o.Fetcher.FetchMetadata(ctx, videoID)
	if err != nil {
		return fmt.Errorf("step fetch failed: %w", err)
	}

	transcript, err := o.Fetcher.FetchTranscript(ctx, videoID)
	if err != nil {
		logger.Warn("Continuing without transcript", zap.String("video_id", videoID), zap.Error(err))
	}

	highlights, err := o.Analyzer.Analyze(ctx, info, transcript)
	if err != nil {
		return fmt.Errorf("step analyze failed: %w", err)
	}

	for i, h := range highlights {
		logger.Info("Processing highlight", zap.Int("index", i), zap.Int("start", h.StartSec))

		localPath, err := o.Downloader.DownloadClip(ctx, videoID, h.StartSec, h.Duration)
		if err != nil {
			logger.Error("Download failed", zap.Error(err))
			continue
		}

		driveID := ""
		if o.Uploader != nil {
			driveID, err = o.Uploader.Upload(ctx, localPath, "")
			if err != nil {
				logger.Warn("Upload failed", zap.Error(err))
			}
		}

		if o.Store != nil {
			_ = o.Store.SaveClipInfo(ctx, videoID, driveID, h.StartSec, h.Duration)
		}
	}

	if o.Store != nil {
		_ = o.Store.MarkAsProcessed(ctx, videoID, "completed")
	}

	return nil
}
