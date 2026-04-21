package coordinator

import (
	"context"
	"sync"
	"time"

	"velox/go-master/internal/pipeline"
	"velox/go-master/internal/pipeline/store"
	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

type Engine struct {
	store      *store.PipelineStore
	fetcher    pipeline.Fetcher
	analyzer   pipeline.Analyzer
	downloader pipeline.Downloader
	
	maxWorkers int
	wg         sync.WaitGroup
	stopCh     chan struct{}
}

func NewEngine(s *store.PipelineStore, f pipeline.Fetcher, a pipeline.Analyzer, d pipeline.Downloader, workers int) *Engine {
	if workers <= 0 { workers = 3 }
	return &Engine{
		store: s,
		fetcher: f,
		analyzer: a,
		downloader: d,
		maxWorkers: workers,
		stopCh: make(chan struct{}),
	}
}

func (e *Engine) Start(ctx context.Context) {
	logger.Info("Velox Pipeline Engine started", zap.Int("workers", e.maxWorkers))
	
	for i := 0; i < e.maxWorkers; i++ {
		e.wg.Add(1)
		go e.worker(ctx, i)
	}
}

func (e *Engine) Stop() {
	close(e.stopCh)
	e.wg.Wait()
	logger.Info("Velox Pipeline Engine stopped")
}

func (e *Engine) worker(ctx context.Context, id int) {
	defer e.wg.Done()
	
	for {
		select {
		case <-ctx.Done(): return
		case <-e.stopCh: return
		default:
			// Preleva un job dalla coda SQLite (con lease di 10 minuti)
			videoID, err := e.store.PopNextJob(ctx, 10*time.Minute)
			if err != nil {
				// Se il database ?? locked, aspetta un po' prima di riprovare
				time.Sleep(2 * time.Second)
				continue
			}
			
			if videoID == "" {
				time.Sleep(10 * time.Second) // Coda vuota, aspetta
				continue
			}
			
			logger.Info("Worker processing video", zap.Int("worker", id), zap.String("video_id", videoID))
			if err := e.processVideo(ctx, videoID); err != nil {
				logger.Error("Video processing failed", zap.String("video_id", videoID), zap.Error(err))
			}
			
			// Piccola pausa tra un video e l'altro per non sovraccaricare yt-dlp
			time.Sleep(5 * time.Second)
		}
	}
}

func (e *Engine) processVideo(ctx context.Context, videoID string) error {
	// Stage 1: Fetch
	info, err := e.fetcher.FetchMetadata(ctx, videoID)
	if err != nil { return err }
	
	transcript, err := e.fetcher.FetchTranscript(ctx, videoID)
	if err != nil { 
		logger.Warn("Failed to fetch transcript, but continuing", zap.String("video_id", videoID), zap.Error(err))
		// Possiamo continuare anche senza trascrizione se vogliamo solo scaricare il video intero?
		// Per ora falliamo se l'obiettivo ?? trovare highlight
		return err 
	}
	
	// Stage 2: Analyze
	highlights, err := e.analyzer.Analyze(ctx, info, transcript)
	if err != nil { return err }
	
	// Stage 3: Download clips
	for _, h := range highlights {
		path, err := e.downloader.DownloadClip(ctx, videoID, h.StartSec, h.Duration)
		if err != nil {
			logger.Warn("Failed to download clip", zap.String("video_id", videoID), zap.Int("start", h.StartSec))
			continue
		}
		logger.Info("Clip downloaded", zap.String("path", path))
	}
	
	return nil
}
