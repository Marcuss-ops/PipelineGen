package images

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
	pathutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	"go.uber.org/zap"
)

func (s *Service) downloadAndIngest(ctx context.Context, slug, imgURL, style, source, query, description string, tags []string) (*media.ImageAsset, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", imgURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bad status code: %d", resp.StatusCode)
	}

	return s.IngestImage(ctx, slug, style, "", resp.Body, filepath.Base(imgURL), imgURL, description, tags, false, false)
}

func (s *Service) IngestImage(ctx context.Context, slug, style, genID string, data io.Reader, filename, sourceURL, description string, tags []string, skipDrive, skipMetadata bool) (*media.ImageAsset, error) {
	// Use a detached context for all database and remote operations in IngestImage.
	// This ensures that once the image data is successfully acquired, the ingestion,
	// database recording, metadata writing, Drive upload, and vector store indexing
	// will complete successfully even if the parent request context is cancelled or timed out.
	ingestCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
	defer cancel()

	content, err := io.ReadAll(data)
	if err != nil {
		return nil, err
	}

	// Legacy dedup: SHA256 check per immagini già salvate col vecchio path
	hasher := sha256.New()
	hasher.Write(content)
	legacyHash := hex.EncodeToString(hasher.Sum(nil))

	if existing, err := s.repo.GetImageByHash(ingestCtx, legacyHash); err == nil && existing != nil {
		// Only reuse if the style matches (avoids returning realistic/ images for oil-painting requests, etc.)
		existingStyle := pathutil.ExtractStyleFromPath(existing.PathRel)
		if style == "" || existingStyle == style {
			// Verify the file actually exists on disk (may have been cleaned up)
			filePath := filepath.Join(s.imagesDir, existing.PathRel)
			if _, statErr := os.Stat(filePath); statErr == nil {
				s.log.Info("IngestImage: hash dedup hit, returning existing",
					zap.String("hash", legacyHash),
					zap.String("style", existingStyle),
				)
				return existing, nil
			}
		}
		s.log.Info("IngestImage: hash dedup skipped (style mismatch or stale)",
			zap.String("hash", legacyHash),
			zap.String("requested_style", style),
			zap.String("existing_style", existingStyle),
		)
	}

	s.log.Info("IngestImage: ingesting image",
		zap.String("slug", slug),
		zap.String("style", style),
		zap.String("gen_id", genID),
		zap.String("hash", legacyHash),
		zap.Bool("skip_drive", skipDrive),
	)

	return s.ingestDirect(ingestCtx, slug, style, genID, content, filename, sourceURL, description, tags, legacyHash, skipDrive, skipMetadata)
}
