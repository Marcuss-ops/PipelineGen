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

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	pathutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	"go.uber.org/zap"
)

func (s *ImageStorageService) downloadAndIngest(ctx context.Context, slug, imgURL, style, source, query, description string, tags []string) (*asset.ImageAsset, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imgURL, nil)
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

func (s *ImageStorageService) IngestImage(ctx context.Context, slug, style, genID string, data io.Reader, filename, sourceURL, description string, tags []string, skipDrive, skipMetadata bool) (*asset.ImageAsset, error) {
	ingestCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
	defer cancel()

	content, err := io.ReadAll(data)
	if err != nil {
		return nil, err
	}
	hashBytes := sha256.Sum256(content)
	contentHash := hex.EncodeToString(hashBytes[:])

	if s.repo == nil {
		s.log.Warn("IngestImage: repo is nil, returning mock asset")
		return &asset.ImageAsset{
			SlugID: slug, Description: description, SourceURL: sourceURL,
			Hash: contentHash, Status: "ready", Origin: asset.ImageOriginRetrieved,
			Provider: asset.ProviderUnknown,
		}, nil
	}

	if existing, err := s.repo.GetImageByHash(ingestCtx, contentHash); err == nil && existing != nil {
		existingStyle := pathutil.ExtractStyleFromPath(existing.PathRel)
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
