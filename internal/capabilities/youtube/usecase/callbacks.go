// Package youtube — ExtractionCallbacks implementation (CPR-CC-6 split, June 2026).
//
// This file holds the methods that satisfy the extraction.ExtractionCallbacks
// interface, plus the private helper methods those callbacks delegate to:
// generateClipMetadata, triggerAutoIndexing, classifyCategory, checkExistingClip,
// enrichSkippedClip.
//
// Before CPR-CC-6 these were scattered across 6 files:
// service_orchestrator.go (callback stubs), ollama_calls.go, indexing.go,
// extractor_classify.go, segment_cache.go, enrichment_skipped.go.
package usecase

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/lifecycle"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ai/classifier"
)

// ── ExtractionCallbacks interface satisfaction ────────────────────────────
// These methods satisfy the extraction.ExtractionCallbacks interface so
// the extraction capability service can delegate external operations back
// to the root orchestrator. Each method delegates to the appropriate
// capability service or port.

func (s *Service) EnrichClip(ctx context.Context, clipID string, ym *youtubeports.DownloaderMetadata, force bool) {
	s.enrichClip(ctx, clipID, ym, force)
}

func (s *Service) ClassifyCategory(ctx context.Context, title string) string {
	return s.classifyCategory(ctx, title)
}

func (s *Service) CheckExistingClip(ctx context.Context, req *youtubetypes.ExtractRequest, clipID string, item *youtubetypes.ExtractItem, outDir string) bool {
	return s.checkExistingClip(ctx, req, clipID, item, outDir)
}

// ProcessLifecycle satisfies ExtractionCallbacks.ProcessLifecycle.
// Inlined from adapters/segment_processor.go to avoid a usecase→adapters
// import cycle (adapters/test files import usecase).
//
// Phase 1c closure (June 2026): the prior lifecycle-helper extraction
// marker was a structural refactor proposal, NOT a correctness blocker.
// The current inlined implementation IS the real implementation — it
// correctly delegates to s.lifecycleService.ProcessAsset and writes
// LifecycleResult back onto the ExtractItem. The structural refactor
// (move helper into a dedicated leaf package) is a follow-up tracked
// in CHANGELOG.md under `### Deferred` — not inlined here to avoid
// fake-tracking via a comment referencing an unlanded YAML ticket
// (godlike/07).
func (s *Service) ProcessLifecycle(ctx context.Context, metadata *lifecycle.FinalizeInput, localPath, fileHash string, item *youtubetypes.ExtractItem) {
	if s.lifecycleService == nil {
		item.LocalPath = localPath
		item.DriveLink = ""
		item.DriveFileID = ""
		item.DownloadLink = ""
		item.Status = "processed"
		return
	}

	lifecycleResult, err := s.lifecycleService.ProcessAsset(ctx, metadata, fileHash)
	if err != nil {
		item.Status = "failed"
		item.Error = fmt.Sprintf("lifecycle failed: %v", err)
		return
	}
	item.LocalPath = localPath
	item.DriveLink = lifecycleResult.DriveLink
	item.DriveFileID = lifecycleResult.DriveFileID
	item.DownloadLink = lifecycleResult.DownloadLink
	item.Status = "processed"
}

func (s *Service) TriggerAutoIndexing(ctx context.Context, clipID string) {
	s.triggerAutoIndexing(ctx, clipID)
}

// IndexClip is a best-effort callback (ExtractionCallbacks) that delegates
// to the ClipIndexerPort. When no indexer is wired the call returns nil
// (no-op) — indexing is a value-add, not a correctness gate. Callers that
// require indexing must check IsEnabled() before calling.
func (s *Service) IndexClip(ctx context.Context, clipID string) error {
	if isUnavailablePort(s.indexer) {
		return nil
	}
	return s.indexer.IndexClip(ctx, clipID)
}

func (s *Service) EnrichSkippedClip(ctx context.Context, clipID, videoURL, videoID string) {
	s.enrichSkippedClip(ctx, clipID, videoURL, videoID)
}

// SliceSubtitles is the public ExtractionCallbacks entry point that
// delegates to the SubtitleFetcherPort.
func (s *Service) SliceSubtitles(ctx context.Context, videoID string, startSec, endSec int, outputPath string) error {
	if isUnavailablePort(s.subtitleFetcher) {
		return fmt.Errorf("youtube: subtitle fetcher port not wired")
	}
	return s.subtitleFetcher.SliceSubtitles(ctx, videoID, startSec, endSec, outputPath)
}

// TranscribeAudio is a best-effort callback (ExtractionCallbacks) that
// delegates to the WhisperTranscriberPort. When Whisper is not wired the
// call returns ("", nil) — an empty transcript signals "no transcription
// available" rather than a hard failure. Callers downstream treat an empty
// string as a missing transcript.
func (s *Service) TranscribeAudio(ctx context.Context, localPath string) (string, error) {
	if isUnavailablePort(s.whisper) {
		return "", nil
	}
	return s.whisper.TranscribeAudio(ctx, localPath)
}

func (s *Service) DriveUploadFileIfChanged(ctx context.Context, localPath, folderID, filename, group, subject string) (*youtubeports.UploadResultDTO, bool, error) {
	if isUnavailablePort(s.driveFolderMgr) {
		return &youtubeports.UploadResultDTO{}, false, fmt.Errorf("youtube: drive folder manager not wired")
	}
	return s.driveFolderMgr.UploadFileIfChanged(ctx, localPath, folderID, filename, group, subject)
}

func (s *Service) DriveGetOrCreateFolder(ctx context.Context, name, parentID string) (string, error) {
	if isUnavailablePort(s.driveFolderMgr) {
		return "", fmt.Errorf("youtube: drive folder manager not wired")
	}
	return s.driveFolderMgr.GetOrCreateFolder(ctx, name, parentID)
}

func (s *Service) OllamaSimpleGenerate(ctx context.Context, model, prompt string, timeoutSec int, opts map[string]any) (string, error) {
	if isUnavailablePort(s.ollama) {
		return "", fmt.Errorf("youtube: ollama port not wired")
	}
	return s.ollama.SimpleGenerate(ctx, model, prompt, time.Duration(timeoutSec)*time.Second, opts)
}

func (s *Service) AcquireVideoExtractSem(ctx context.Context) (release func()) {
	select {
	case s.videoExtractSem <- struct{}{}:
		return func() { <-s.videoExtractSem }
	case <-ctx.Done():
		return nil
	}
}

func (s *Service) AcquireOllamaSem(ctx context.Context) (release func()) {
	select {
	case s.ollamaSem <- struct{}{}:
		return func() { <-s.ollamaSem }
	case <-ctx.Done():
		return nil
	}
}

// ── Private callback helpers ────────────────────────────────────────────
// Merged from ollama_calls.go, indexing.go, extractor_classify.go,
// segment_cache.go, enrichment_skipped.go (CPR-CC-6, June 2026).

// triggerAutoIndexing is preserved as an interface method for ExtractionCallbacks
// compatibility, but the body is intentionally a no-op as of Commit F (June 2026).
//
// Rationale: the canonical durable indexing pipeline is:
//
//	process_segment.go::Step 9 → youtubeports.ClipAtomicWriter.CommitClipAndIndexEvent
//	  → outbox_events row (event_key = clipID)
//	    → outboxevents.Pool.ClaimNext → IndexingHandler.Handle
//	      → IndexClip → Qdrant upsert
//
// This handler chain is async (worker), idempotent on aggregate_id (UNIQUE
// event_key), retryable via outbox backoff, and terminal failures dead-letter
// via MaxAttempts (godlike/07 §"no fake availability"). The legacy
// fire-and-forget path that this method used to take — concurrent.SafeGoFunc
// + direct indexer.IndexClip — duplicated work the canonical pipeline already
// covers, with a guaranteed-to-lose race against the worker's MarkFailed path on
// transient Qdrant errors (the goroutine would log+swallow; the worker would
// also retry, leading to two concurrent IndexClip calls + duplicate outbox
// events if EnrichClip was reached via this path, since metadata.EnrichClip
// emits its own asset.index.requested outbox event).
//
// Eliminating the SafeGoFunc + direct IndexClip here removes the
// duplicate/double-emit hazard. Callers that invoke the method (e.g.
// enrichSkippedClip, the ExtractionCallbacks interface) compile against the
// unchanged signature; the contract is now "covered by the durable pipeline".
//
// Idempotency proof: directory_test.go commit F regression
// (internal/application/jobs/outbox/durable_indexing_test.go) verifies that
// 2 enqueues of the SAME event_key produce 1 outbox row and 1 IndexClip
// callback — structure-driven (UNIQUE constraint), no goroutine required.
func (s *Service) triggerAutoIndexing(ctx context.Context, clipID string) {
	if s.log == nil {
		return
	}
	s.log.Debug("triggerAutoIndexing is a no-op: durable indexing is owned by the canonical outbox pipeline (ClipAtomicWriter → IndexingHandler → IndexClip)",
		zap.String("clip_id", clipID))
	// Explicit no-op body. The function remains on the struct solely to
	// preserve the ExtractionCallbacks interface contract — callers
	// (e.g. enrichSkippedClip) compile against the unchanged signature
	// but no longer fork a goroutine + direct indexer.IndexClip call.
}

// youtubeCategoryCache implements classifier.CategoryCache backed by the cache service.
type youtubeCategoryCache struct {
	svc *Service
}

func (c *youtubeCategoryCache) Get(ctx context.Context, title string) (string, bool) {
	if c.svc.cache == nil {
		return "", false
	}
	return c.svc.cache.GetCategory(ctx, title)
}

func (c *youtubeCategoryCache) Set(ctx context.Context, title, category string) error {
	// Best-effort: category cache is a performance optimization, not a correctness
	// requirement. Nil cache service means the classification won't be persisted,
	// but the caller still receives the category result for immediate use.
	if c.svc.cache == nil {
		return nil
	}
	c.svc.cache.SetCategory(ctx, title, category)
	return nil
}

// classifyCategory classifies the video title using the shared classifier with SQLite cache.
func (s *Service) classifyCategory(ctx context.Context, title string) string {
	if s.ollama == nil {
		return "general"
	}
	return classifier.CachedClassify(ctx, s.log, s.ollama, title, classifier.Options{
		DataDir:          s.cfg.DataDir,
		Model:            s.cfg.OllamaModel,
		FallbackCategory: "general",
		ExcludeCategories: []string{
			"interviews", "general", "other", "clips", "youtube", "videos",
		},
		EnsureCategories:  []string{"rap", "music"},
		DefaultCategories: []string{"boxe", "crime", "discovery", "rap", "music"},
		Cache:             &youtubeCategoryCache{svc: s},
		Semaphore:         s.ollamaSem,
	})
}

// checkExistingClip implements the cache strategy policy for segment extraction.
// Returns true when the existing DB record (and on-disk file) matches and the
// caller should skip the cut/download steps.
func (s *Service) checkExistingClip(ctx context.Context, req *youtubetypes.ExtractRequest, clipID string, item *youtubetypes.ExtractItem, outDir string) bool {
	if req.Strategy == "replace" || s.clips == nil {
		return false
	}

	existingClip, err := s.clips.GetClip(ctx, clipID)
	if err != nil || existingClip == nil {
		return false
	}

	expectedPath := filepath.Join(outDir, item.Filename)
	if existingClip.LocalPath() != expectedPath {
		s.log.Info("cached clip local path mismatch, forcing re-processing for new folder",
			zap.String("clip_id", clipID),
			zap.String("existing_path", existingClip.LocalPath()),
			zap.String("expected_path", expectedPath))
		return false
	}

	if req.Strategy == "skip" {
		item.LocalPath = existingClip.LocalPath()
		item.DriveLink = existingClip.DriveLink()
		item.DriveFileID = existingClip.DriveFileID()
		item.DownloadLink = existingClip.DownloadLink()
		item.Status = "skipped"
		return true
	}

	// Default strategy: verify file exists
	if s.clipFiles != nil {
		if ok, clipErr := s.clipFiles.UsableCachedClip(existingClip.LocalPath()); clipErr == nil && ok {
			item.LocalPath = existingClip.LocalPath()
			item.DriveLink = existingClip.DriveLink()
			item.DriveFileID = existingClip.DriveFileID()
			item.DownloadLink = existingClip.DownloadLink()
			item.Status = "skipped"
			return true
		}
	}

	// Stale record — clean up before reprocessing
	s.log.Warn("stale youtube clip record detected, removing it before reprocessing",
		zap.String("clip_id", clipID),
		zap.String("local_path", existingClip.LocalPath()))
	if existingClip.LocalPath() != "" {
		_ = os.Remove(existingClip.LocalPath())
	}
	_ = s.clips.DeleteClip(ctx, clipID)
	return false
}

// enrichSkippedClip enriches a clip that was found in cache (skipped) but lacks YouTube metadata.
// This handles the case where a clip was downloaded in a previous session without metadata
// (e.g., before the yt-dlp metadata fetch was fixed).
func (s *Service) enrichSkippedClip(ctx context.Context, clipID, videoURL, videoID string) {
	// Check if clip needs enrichment
	existing, err := s.clips.GetClip(ctx, clipID)
	if err != nil || existing == nil {
		return
	}
	// If already has YouTube metadata, skip
	if existing.YouTubeTitle() != "" {
		return
	}

	s.log.Info("enriching skipped YouTube clip with metadata",
		zap.String("clip_id", clipID),
		zap.String("video_id", videoID))

	// Fetch YouTube metadata directly via the metaFetcher port
	if s.metaFetcher == nil {
		return
	}
	ym, err := s.metaFetcher.GetVideoMetadata(ctx, videoURL)
	if err != nil {
		s.log.Warn("failed to fetch YouTube metadata for skipped clip",
			zap.String("clip_id", clipID),
			zap.Error(err))
		return
	}

	// Pass DownloaderMetadata directly to the enrichment pipeline (P0.3)
	s.enrichClip(ctx, clipID, ym, false)

	// Also trigger auto-indexing now that the clip has rich search_text
	s.triggerAutoIndexing(ctx, clipID)
}
