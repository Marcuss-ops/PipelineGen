package images

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"go.uber.org/zap"
)

// downloadAndIngest is the canonical web-image download entry point.
//
// PR-SOURCESTAGER-CONSOLIDATE (July 2026): the inline `http.NewRequest
// + s.client.Do + StatusCode` path is retired. The download now routes
// through the canonical acquisition.SourceStager port (Prepare) so:
//   - status-code checks no longer leak into the processor,
//   - the staged LocalPath is deterministic from SourceRef.URL
//     (two requests for the same URL dedupe naturally on disk),
//   - the SHA256 is computed during the staging write so
//     callers do not pay a second read pass for dedup checks.
//
// godlike/07 NO-FAKE-AVAILABILITY: a nil stager fails closed with a
// typed error rather than silently falling back to inline http. The
// composition root MUST wire the stager at NewService time
// (pre-PR this was silently absent in tests).
func (s *ImageStorageService) downloadAndIngest(ctx context.Context, slug, imgURL, style, source, query, description string, tags []string) (*detail.ImageAsset, error) {
	if s.sourceStager == nil {
		return nil, fmt.Errorf("image ingest: source stager is nil (PR-SOURCESTAGER-CONSOLIDATE: composition root must wire acquisition.SourceStager)")
	}
	req := acquisition.PrepareRequest{
		Source:         acquisition.SourceRef{URL: imgURL},
		CallerRef:      "image-ingest",
		IdempotencyKey: acquisition.DeriveIdempotencyKey(acquisition.SourceRef{URL: imgURL}),
	}
	prepared, stageErr := s.sourceStager.Prepare(ctx, req)
	if stageErr != nil {
		return nil, fmt.Errorf("stage source %q: %w", imgURL, stageErr)
	}
	defer func() {
		// Best-effort release; staging release is idempotent so a
		// second call (e.g. on retry) is a no-op. We log on
		// failure rather than swallowing the error silently so
		// staging-dir pressure is observable in production.
		if releaseErr := s.sourceStager.Release(context.Background(), prepared.CleanupToken); releaseErr != nil && s.log != nil {
			s.log.Warn("downloadAndIngest: staged source release failed (best-effort)",
				zap.String("local_path", prepared.LocalPath),
				zap.String("source_url", imgURL),
				zap.Error(releaseErr))
		}
	}()

	f, openErr := os.Open(prepared.LocalPath)
	if openErr != nil {
		return nil, fmt.Errorf("open staged source %q: %w", prepared.LocalPath, openErr)
	}
	defer f.Close()
	return s.IngestImage(ctx, slug, style, "", f, filepath.Base(imgURL), imgURL, description, tags, false, false)
}

// IngestImage takes a streamed byte payload and writes it through
// the canonical image-ingest path (CommitAsset + repo dedup). It is
// the lower-level entry point used by downloadAndIngest (which
// already staged the bytes via sourceStager.Prepare) and by
// any caller that already holds a streamed body.
func (s *ImageStorageService) IngestImage(ctx context.Context, slug, style, genID string, data io.Reader, filename, sourceURL, description string, tags []string, skipDrive, skipMetadata bool) (*detail.ImageAsset, error) {
	if s.repo == nil {
		return nil, ErrImageRepositoryUnavailable
	}

	ingestCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
	defer cancel()

	content, err := io.ReadAll(data)
	if err != nil {
		return nil, err
	}
	contentHash := digest.SHA256Bytes(content)

	if existing, err := s.repo.GetImageByHash(ingestCtx, contentHash); err == nil && existing != nil {
		existingStyle := extractStyleFromPath(existing.PathRel)
		if style == "" || existingStyle == style {
			filePath := filepath.Join(s.imagesDir, existing.PathRel)
			if _, statErr := os.Stat(filePath); statErr == nil {
				s.log.Info("IngestImage: hash dedup hit, returning existing",
					zap.String("hash", contentHash), zap.String("style", existingStyle))
				return existing, nil
			}
		}
	}

	return s.ingestDirect(ingestCtx, slug, style, genID, content, filename, sourceURL, description, tags, contentHash, skipDrive, skipMetadata)
}
