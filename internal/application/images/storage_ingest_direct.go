package images

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	"go.uber.org/zap"
)

func (s *ImageStorageService) ingestDirect(ctx context.Context, slug, style, genID string, content []byte, filename, source, description string, tags []string, hash string, skipDrive, skipMetadata bool) (*asset.ImageAsset, error) {
	// PR C9 (July 2026): replace silent-fake `extractSubjectAndTags` stub
	// with the typed SubjectTagsService port. On error we log a warning
	// and fall back to empty values (the caller still has its own
	// slug + tags from upstream), preserving the pre-existing fail-open
	// behavior but with explicit visibility (godlike/07 no-fake-availability).
	promptSubject, promptTags, err := s.subjectTags.ExtractSubjectAndTags(ctx, description)
	if err != nil {
		if s.log != nil {
			s.log.Warn("subject/tags extraction failed; falling back to caller-supplied values",
				zap.Error(err),
				zap.String("source", source),
				zap.String("description", description))
		}
		promptSubject = ""
		promptTags = nil
	}
	if slug == "" || slug == "unknown" {
		slug = textutil.Slugify(promptSubject)
	}
	if len(tags) == 0 {
		tags = promptTags
	}

	subject, err := s.repo.GetSubjectBySlugOrAlias(ctx, slug)
	if err != nil || subject == nil {
		subject = &asset.Subject{Slug: slug, DisplayName: slug}
		if _, err := s.repo.CreateSubject(ctx, subject); err != nil {
			return nil, fmt.Errorf("create subject %q: %w", slug, err)
		}
	}

	ext := filepath.Ext(filename)
	if ext == "" {
		ext = ".jpg"
	}
	req := drive.AssetDestinationRequest{
		Source:       drive.SourceType(source),
		MediaType:    drive.MediaTypeImage,
		Subject:      slug,
		Hash:         hash,
		Ext:          ext,
		Style:        style,
		GenerationID: genID,
	}
	if root := s.aiImageDriveRootForSource(source, style); root != "" {
		req.DriveRootOverride = root
	}

	dest, err := s.mediaStore.ResolveDest(req)
	if err != nil {
		return nil, fmt.Errorf("resolve destination: %w", err)
	}
	if err := persistImageBytes(dest.LocalPath, content); err != nil {
		return nil, err
	}

	generator := imageGeneratorLabel(source)
	width, height := decodeImageDimensions(content)

	// PR-QDRANT-IMAGES-INDEX (July 2026): tagImageMetadata failure is
	// now non-fatal. On error we log a warning and continue with
	// metaResult=nil — the image is still downloaded to disk, uploaded
	// to Drive, and persisted in SQLite. The caller still gets a valid
	// *asset.ImageAsset back. Pre-PR this was a hard failure that
	// deleted the downloaded file and aborted the entire ingest.
	metaResult, err := s.meta.tagImageMetadata(ctx, description, style, generator, hash, dest.LocalPath, width, height)
	if err != nil {
		if s.log != nil {
			s.log.Warn("tagImageMetadata failed; continuing with minimal metadata",
				zap.Error(err),
				zap.String("hash", hash),
				zap.String("source", source))
		}
		metaResult = nil
	}

	var driveFileID string
	if s.mediaStore != nil && !skipDrive {
		fileID, _, uploadErr := s.publishToDrive(ctx, req, dest.LocalPath)
		if uploadErr != nil {
			s.log.Warn("Drive upload failed", zap.Error(uploadErr))
		} else {
			driveFileID = fileID
			if !skipMetadata && metaResult != nil {
				s.meta.uploadImageMetadata(ctx, style, slug, metaResult)
			}
		}
	}

	var metaJSON []byte
	if metaResult != nil && metaResult.Payload != nil {
		payload := *metaResult.Payload
		payload.AssetID = hash
		metaJSON, _ = json.Marshal(payload)
		tags = uniqueAppend(tags, payload.Tags...)
	} else {
		metaJSON, _ = json.Marshal(map[string]any{
			"prompt": description, "style": style, "generator": generator,
		})
	}

	result := &asset.ImageAsset{
		SubjectID:    slug,
		Hash:         hash,
		PathRel:      dest.RelativePath,
		SourceURL:    source,
		Description:  description,
		DriveFileID:  driveFileID,
		Width:        width,
		Height:       height,
		SizeBytes:    int64(len(content)),
		Status:       "ready",
		MetadataJSON: string(metaJSON),
		Tags:         tags,
		Origin:       classifyImageOrigin(source, generator),
		Provider:     classifyImageProvider(source, generator),
	}

	if _, err := s.repo.AddImage(ctx, result); err != nil {
		message := strings.ToLower(err.Error())
		if strings.Contains(message, "unique") || strings.Contains(message, "constraint") {
			if existing, readErr := s.repo.GetImageByHash(ctx, hash); readErr == nil && existing != nil {
				return existing, nil
			}
			return result, nil
		}
		return nil, fmt.Errorf("add image: %w", err)
	}

	// PR-QDRANT-IMAGES-INDEX (July 2026): after AddImage succeeds,
	// emit an asset.index.requested outbox event so the Qdrant
	// IndexingHandler indexes the image in the vector store.
	//
	// Two-transaction gap note: AddImage commits in its own tx,
	// then EnqueueAndIndex opens a new tx. If the process crashes
	// between them, the image is in SQLite but not Qdrant. The
	// admin reconcile tool (cmd/admin/reconcile_qdrant.go) can
	// repair this gap — forward-pointer PR-QDRANT-IMAGES-RECONCILE.
	if s.dispatcher != nil {
		dispAsset := &asset.Asset{
			ID:             hash,
			Name:           textutil.Truncate(description, 500),
			Source:         "image",
			MediaType:      asset.MediaTypeImage,
			LifecycleState: asset.StateStaging,
			CreatedAt:      time.Now(),
		}
		dispAsset.SetDriveFileID(driveFileID)
		dispAsset.SetDriveLink(s.FormatDriveLink(driveFileID))
		dispAsset.SetLocalPath(dest.RelativePath)
		dispAsset.SetFileHash(hash)

		if err := s.dispatcher.EnqueueAndIndex(ctx, dispAsset, hash); err != nil {
			if s.log != nil {
				s.log.Warn("Qdrant indexing enqueue failed for retrieved image",
					zap.Error(err),
					zap.String("hash", hash),
					zap.String("source", source))
			}
		}
	} else if s.log != nil {
		s.log.Debug("Qdrant indexing skipped for retrieved image (dispatcher not wired)",
			zap.String("hash", hash))
	}

	return result, nil
}

func imageGeneratorLabel(source string) string {
	lower := strings.ToLower(source)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return source
	}
	switch {
	case strings.Contains(lower, "wikipedia.org"):
		return "wikipedia"
	case strings.Contains(lower, "duckduckgo"):
		return "duckduckgo"
	default:
		return "web-download"
	}
}
