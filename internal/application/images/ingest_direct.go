package images

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	storedrive "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	concurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	"go.uber.org/zap"
)

func (s *Service) ingestDirect(ctx context.Context, slug, style, genID string, content []byte, filename, source, description string, tags []string, hash string, skipDrive, skipMetadata bool) (*asset.ImageAsset, error) {
	// Enrich tags and subject from prompt if needed
	promptSubject, promptTags := extractSubjectAndTags(description)
	if slug == "" || slug == "unknown" {
		slug = textutil.Slugify(promptSubject)
	}
	if len(tags) == 0 {
		tags = promptTags
	}

	// 1. Trova Soggetto (o crealo)
	subject, err := s.repo.GetSubjectBySlugOrAlias(ctx, slug)
	if err != nil || subject == nil {
		subject = &asset.Subject{
			Slug:        slug,
			DisplayName: slug,
		}
		if _, err := s.repo.CreateSubject(ctx, subject); err != nil {
			return nil, fmt.Errorf("create subject %q: %w", slug, err)
		}
	}

	// 2. Prepara percorsi
	ext := filepath.Ext(filename)
	if ext == "" {
		ext = ".jpg"
	}

	// Create request for resolver
	// Source is a typed string (drive.SourceType = asset.SourceType);
	// the caller-supplied `source` is a plain string (e.g. "google-flow"),
	// so we explicitly cast to satisfy the literal.
	req := drive.AssetDestinationRequest{
		Source:       drive.SourceType(source), // Use the provided source (e.g. google-flow)
		MediaType:    drive.MediaTypeImage,
		Subject:      slug, // Prompt slug
		Hash:         hash,
		Ext:          ext,
		Style:        style, // Chosen style
		GenerationID: genID,
	}
	if aiDriveRoot := s.aiImageDriveRootForSource(source, style); aiDriveRoot != "" {
		req.DriveRootOverride = aiDriveRoot
	}

	dest, err := s.mediaStore.ResolveDest(req)
	if err != nil {
		return nil, fmt.Errorf("resolve destination: %w", err)
	}

	relPath := dest.RelativePath
	fullPath := dest.LocalPath
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return nil, fmt.Errorf("create directory: %w", err)
	}

	// 3. Salva il file fisico
	if err := os.WriteFile(fullPath, content, 0644); err != nil {
		s.log.Error("ingestDirect: failed to write file", zap.String("path", fullPath), zap.Error(err))
		return nil, fmt.Errorf("failed to write image file: %w", err)
	}
	s.log.Info("ingestDirect: file saved", zap.String("path", fullPath), zap.Int("bytes", len(content)))

	// 4. Resolve generator dynamically from source
	generator := source
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		if strings.Contains(source, "wikipedia.org") {
			generator = "wikipedia"
		} else if strings.Contains(source, "duckduckgo") {
			generator = "duckduckgo"
		} else {
			generator = "web-download"
		}
	}

	// 4b. Compute image dimensions once (needed for both metadata and DB)
	imgWidth, imgHeight := decodeImageDimensions(content)

	// 5. SINGLE tagger call — used for BOTH Drive metadata.json AND DB record.
	metaResult, taggerErr := s.tagImageMetadata(ctx, description, style, generator, hash, fullPath, imgWidth, imgHeight)
	if taggerErr != nil {
		s.log.Error("ingestDirect: tagImageMetadata validation or execution failed, deleting local file and aborting", zap.Error(taggerErr))
		_ = os.Remove(fullPath)
		return nil, taggerErr
	}

	// 6. Upload to Drive if configured (skip if disabled by fullimages pipeline)
	var driveFileID string
	if s.mediaStore != nil && !skipDrive {
		fileID, _, err := s.mediaStore.UploadToDrive(ctx, req, fullPath)
		if err != nil {
			s.log.Warn("Drive upload failed", zap.Error(err))
		} else {
			driveFileID = fileID
			s.log.Info("Drive upload successful", zap.String("file_id", fileID))

			if !skipMetadata && metaResult != nil {
				s.uploadImageMetadata(ctx, req, metaResult)
			}
		}
	}

	// 7. Build metaJSON for DB record from the SINGLE tagger result
	var metaJSON []byte
	if taggerErr == nil && metaResult != nil && metaResult.Payload != nil {
		payloadCopy := *metaResult.Payload
		payloadCopy.AssetID = hash
		metaJSON, _ = json.Marshal(payloadCopy)
		tags = uniqueAppend(tags, payloadCopy.Tags...)
	} else {
		// Fallback to basic metadata if tagger fails or writer not configured
		metaMap := map[string]any{
			"prompt":    description,
			"style":     style,
			"generator": generator,
		}
		metaJSON, _ = json.Marshal(metaMap)
	}

	// 8. Crea record DB con dimensioni reali
	asset := &asset.ImageAsset{
		SubjectID:    slug,
		Hash:         hash,
		PathRel:      relPath,
		SourceURL:    source,
		Description:  description,
		DriveFileID:  driveFileID,
		Width:        imgWidth,
		Height:       imgHeight,
		SizeBytes:    int64(len(content)),
		Status:       "ready",
		MetadataJSON: string(metaJSON),
		Tags:         tags,
	}

	if _, err := s.repo.AddImage(ctx, asset); err != nil {
		// UNIQUE constraint on hash — file is already saved at the correct
		// style path on disk. Return the existing DB record so callers
		// get a valid, persisted asset.
		errMsg := strings.ToLower(err.Error())
		if strings.Contains(errMsg, "unique") || strings.Contains(errMsg, "constraint") {
			s.log.Info("ingestDirect: hash exists in DB, returning existing asset",
				zap.String("hash", hash),
				zap.String("path", relPath),
				zap.String("style", style),
			)
			existing, getErr := s.repo.GetImageByHash(ctx, hash)
			if getErr == nil && existing != nil {
				return existing, nil
			}
			return asset, nil
		}
		return nil, fmt.Errorf("failed to add image to repository: %w", err)
	}

	// Determine search text for vector indexing
	searchText := description
	if taggerErr == nil && metaResult != nil && metaResult.Payload != nil && metaResult.Payload.SearchText != "" {
		searchText = metaResult.Payload.SearchText
	}

	// PG-034 (June 2026): vector indexing call site kept as a no-op
	// (s.indexAssetInVectorStore is now a no-op stub). The DB-side
	// embedding JSON was already persisted earlier in the pipeline via
	// UpdateEmbeddingData, so the canonical metadata survives in SQLite
	// even without a vector-store backend. The function call is
	// preserved so the legacy image-vector-indexing goroutine contract
	// continues to hold — callers may add new behaviors behind the
	// indexAssetInVectorStore seam without changing this site.
	asyncCtx, asyncCancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	concurrent.SafeGo("image-vector-indexing", func() {
		defer asyncCancel()
		driveLink := ""
		if driveFileID != "" {
			driveLink = storedrive.FileURLFromID(driveFileID)
		}
		s.indexAssetInVectorStore(asyncCtx, hash, source, searchText, relPath, driveLink, style, "image", searchText, tags)
	})

	return asset, nil
}

// extractSubjectAndTags is a temporary stub returning ("", nil). The
// inference logic — which previously parsed `description` into a slug-ish
// subject plus a comma/semicolon separated tag list — is being reworked in
// a follow-up PR that will land the proper subject/tag extractor behind a
// dedicated service. Today this no-op keeps the direct ingestion callsite
// compiling while we rewire the pipeline. This stub MUST be replaced
// before the asset pipeline is considered feature-complete; behaviour of
// tags-only / unknown-slug paths will degrade silently otherwise.
func extractSubjectAndTags(description string) (string, []string) {
	// The real parser derives subject (slug + alias-tolerant) and tags
	// (tokenized against textutil.TermsFromText). Today the build needs a
	// no-op so direct ingestion callsites compile; a follow-up PR will
	// reintroduce the SubjectTagsService and route this call through it.
	_ = description
	return "", nil
}
