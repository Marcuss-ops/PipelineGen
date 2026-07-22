package images

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	persistence "github.com/Marcuss-ops/PipelineGen/internal/application/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/defaults"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"

	"github.com/Marcuss-ops/PipelineGen/internal/application/images/destinations"
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

	// PR-IMAGES-REMOVE-DRIVE-STORE (July 2026): the legacy
	// Local path is computed inline via destinations.LocalPathFor.
	// The path computation has migrated into the destinations
	// package as destinations.LocalPathFor (PR-IMAGES-REMOVE-DRIVE-STORE
	// follow-up, July 2026) so the destinationResolver package
	// owns the canonical image-path shape (source-prefixed
	// `<source>/<slug>.<ext>`). The images package imports it
	// without taking a destination-resolver interface change.
	localPath, relativePath := destinations.LocalPathFor(s.imagesDir, slug, source, ext)
	if err := persistImageBytes(localPath, content); err != nil {
		return nil, err
	}

	generator := imageGeneratorLabel(source)
	provider := classifyImageProvider(source, generator)
	origin := classifyImageOrigin(source, generator)
	width, height := decodeImageDimensions(content)

	// PR-QDRANT-IMAGES-INDEX (July 2026): tagImageMetadata failure is
	// now non-fatal. On error we log a warning and continue with
	// metaResult=nil — the image is still downloaded to disk, uploaded
	// to Drive, and persisted in SQLite. The caller still gets a valid
	// *asset.ImageAsset back. Pre-PR this was a hard failure that
	// deleted the downloaded file and aborted the entire ingest.
	metaResult, err := s.meta.tagImageMetadata(ctx, description, style, generator, hash, localPath, width, height)
	if err != nil {
		if s.log != nil {
			s.log.Warn("tagImageMetadata failed; continuing with minimal metadata",
				zap.Error(err),
				zap.String("hash", hash),
				zap.String("source", source))
		}
		metaResult = nil
	}

	// PR-IMAGES-REMOVE-DRIVE-STORE (July 2026): the legacy
	// The legacy drive.Store upload bridge is RETIRED —
	// Drive upload now routes directly through s.publisher.Publish
	// (delivery.Publisher) with the canonical delivery.PublishRequest
	// shape. The override root is still sourced from
	// s.aiImageDriveRootForSource so the per-style folder routing
	// is preserved end-to-end.
	var driveFileID string
	storedSourceURL := source
	if s.publisher != nil && !skipDrive {
		overrideRoot := s.aiImageDriveRootForSource(source, style)
		if overrideRoot == "" {
			if s.log != nil {
				s.log.Debug("Drive upload skipped for image: no configured root folder",
					zap.String("source", source),
					zap.String("style", style),
					zap.String("slug", slug))
			}
		} else {
			// PR-WAVE-1-DRIVE-SSOT (July 2026): the per-style folder
			// override is now expressed through the canonical
			// DestinationFolderID field (the typed pre-resolved-folder
			// surface on PublishRequest), NOT the legacy
			// RootFolderOverride escape hatch. Per
			// delivery/types.go::DestinationFolderID docstring, when
			// non-empty DestinationFolderID takes precedence over the
			// registry's RootFolderID for the destination — preserving
			// the per-style folder routing end-to-end while satisfying
			// the godlike/06 SSOT forward-prevention gate. Behavior
			// unchanged from the caller's view.
			pubResult, uploadErr := s.publisher.Publish(ctx, delivery.PublishRequest{
				Destination:         delivery.DestinationImage,
				DestinationFolderID: overrideRoot,
				LocalPath:           localPath,
				Filename:            filepath.Base(localPath),
				Style:               style,
				Subject:             slug,
				Group:               slug,
				ConflictPolicy:      delivery.ConflictSkip,
			})
			if uploadErr != nil {
				s.log.Warn("Drive upload failed", zap.Error(uploadErr))
			} else {
				driveFileID = pubResult.FileID
				if strings.TrimSpace(pubResult.WebViewLink) != "" {
					storedSourceURL = pubResult.WebViewLink
				}
				if !skipMetadata && metaResult != nil {
					s.meta.uploadImageMetadata(ctx, style, slug, metaResult)
				}
			}
		}
	}

	builder := NewCanonicalImageMetadataBuilder(source, generator).
		WithBaseInfo(description, style, hash, tags, width, height).
		WithGenerator(generator)
	if metaResult != nil && metaResult.Payload != nil {
		payload := *metaResult.Payload
		payload.AssetID = hash
		builder.WithSemanticPayload(&payload)
		tags = uniqueAppend(tags, payload.Tags...)
	}
	metaJSONStr, builtOrigin, builtProvider := builder.Build()
	metaJSON := []byte(metaJSONStr)
	// The builder is the SSOT for origin/provider; align the asset
	// record so that MetadataJSON.origin matches asset.Origin.
	origin = builtOrigin
	provider = builtProvider

	result := &asset.ImageAsset{
		SubjectID:    slug,
		Hash:         hash,
		PathRel:      relativePath,
		LocalPath:    localPath,
		SourceURL:    storedSourceURL,
		Description:  description,
		DriveFileID:  driveFileID,
		Width:        width,
		Height:       height,
		SizeBytes:    int64(len(content)),
		Status:       "ready",
		MetadataJSON: string(metaJSON),
		Tags:         tags,
		Origin:       origin,
		Provider:     provider,
	}

	// ingestDirect uses a single atomic CommitAsset transaction so a
	// crash leaves zero rows in both stores. The previous
	// "unique constraint" handling is delegated to CommitAsset's
	// canonical idempotent upsert contract.
	if s.committer == nil {
		return nil, fmt.Errorf("%w: image ingest atomic commit refused", errImageIngestCommitterNil)
	}

	// Build the canonical CommitRequest from the resolved assets +
	// collected metadata. AssetID and ContentHash both equal `hash`
	// so the generated outbox event keeps its legacy aggregate_id ==
	// hash wire identity (preserves the downstream job handler's
	// dedup contract).
	commitReq := persistence.CommitRequest{
		AssetID:        hash,
		Source:         "image",
		Filename:       filename,
		MediaType:      string(asset.MediaTypeImage),
		ContentHash:    hash,
		LifecycleState: string(asset.StateStaging),
		IndexState:     string(asset.StateIndexPending),
		LocalPath:      localPath,
		FolderID:       s.driveFolderID,
		Title:          textutil.Truncate(description, 500),
		Description:    description,
		SourceURL:      storedSourceURL,
		Metadata: persistence.TypedMetadata{
			Title:         textutil.Truncate(description, 500),
			Description:   description,
			SourceVersion: defaults.VisualEmbeddingModelVersion,
			Tags:          tags,
			Slug:          slug,
			SizeBytes:     int64(len(content)),
			// width + height live in Extra because TypedMetadata has no
			// typed slots for them — mirror the JSON keys produced by
			// the metaJSON fallback above.
			SourceProvider: string(provider),
			Extra: map[string]any{
				"path_rel":                 result.PathRel,
				"width":                    width,
				"height":                   height,
				"origin":                   origin,
				"provider":                 provider,
				"generator":                generator,
				"style":                    style,
				"gen_id":                   genID,
				"drive_file_id":            driveFileID,
				"content_hash":             hash,
				"embedding_version_visual": defaults.VisualEmbeddingModelVersion,
			},
		},
		Locations:      buildImageIngestLocations(localPath, driveFileID, s.FormatDriveLink(driveFileID), hash, int64(len(content))),
		EmitIndexEvent: true,
	}

	if _, err := s.committer.CommitAsset(ctx, commitReq); err != nil {
		// CommitAsset is idempotent: a unique-collision on a
		// previously-committed (AssetID, ContentHash) row returns
		// a successful CommitResult with zero new RowsAffected, NOT
		// an error. If we DO see an error here, surface it as-is —
		// the caller can decide whether to retry. The previous
		// GetImageByHash fallback path was retired because the new
		// canonical writer handles dedup atomically.
		return nil, fmt.Errorf("image ingest atomic commit: %w", err)
	}
	if result.Origin == asset.ImageOriginRetrieved && s.repo != nil {
		detail := &asset.RetrievedImageDetail{
			AssetID: hash, SourceImageURL: source,
			Provider: string(result.Provider), RetrievedAt: time.Now().UTC().Format(time.RFC3339),
		}
		if v, ok := ctx.Value(PageURLKey).(string); ok {
			detail.SourcePageURL = v
		}
		if v, ok := ctx.Value(LicenseKey).(string); ok {
			detail.License = v
		}
		if v, ok := ctx.Value(AuthorKey).(string); ok {
			detail.Author = v
		}
		if v, ok := ctx.Value(SearchQueryKey).(string); ok {
			detail.SearchQuery = v
		}
		if err := s.repo.UpsertRetrievedDetails(ctx, detail); err != nil {
			// Older test/maintenance databases may predate migration 117;
			// the canonical media commit has already succeeded, so keep the
			// asset usable while making the missing projection observable.
			if !strings.Contains(strings.ToLower(err.Error()), "no such table") {
				return nil, fmt.Errorf("image retrieved provenance: %w", err)
			}
			if s.log != nil {
				s.log.Warn("retrieved_image_details table unavailable", zap.Error(err))
			}
		}
	}

	return result, nil
}

func imageGeneratorLabel(source string) string {
	if d := asset.DefaultProviderRegistry().Match(source); d != nil {
		return string(d.ID)
	}
	lower := strings.ToLower(strings.TrimSpace(source))
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return "web-download"
	}
	return source
}

// errImageIngestCommitterNil is the canonical typed sentinel returned
// when ImageStorageService.committer is nil at ingestDirect invocation.
// godlike/07 typed-error discipline: callers can errors.Is this to
// distinguish the fail-closed nil-committer case from the
// CommitAsset-call error case (the latter wraps the underlying
// persistence error directly without a sentinel).
var errImageIngestCommitterNil = errors.New("images: ingestDirect: asset committer is nil (require persistence.AssetCommitter wiring at composition time)")

// buildImageIngestLocations returns the canonical asset_locations
// rows for an image ingest. The local filesystem row is always
// present and marked primary. When the Drive upload succeeded
// (driveFileID non-empty), a second drive row is appended so the
// typed asset_locations table mirrors the metadata.Extra["drive_file_id"]
// shape — the godlike/06 SSOT for downstream readers of the location
// surface.
//
// The Drive row is NOT marked primary; the local row remains the
// primary so single-replica offline reads continue to work even when
// Drive is unreachable. Callers that need Drive-as-primary should
// layer their resolver on top of this canonical shape.
func buildImageIngestLocations(localPath, driveFileID, webLink, hash string, sizeBytes int64) []persistence.LocationCommit {
	locs := []persistence.LocationCommit{
		{
			Kind:          "local",
			Provider:      "filesystem",
			URI:           localPath,
			WebViewLink:   "",
			FileSizeBytes: sizeBytes,
			FileHash:      hash,
			IsPrimary:     true,
		},
	}
	if strings.TrimSpace(driveFileID) != "" {
		locs = append(locs, persistence.LocationCommit{
			Kind:          "drive",
			Provider:      "drive",
			ExternalID:    driveFileID,
			URI:           webLink,
			WebViewLink:   webLink,
			FileSizeBytes: sizeBytes,
			FileHash:      hash,
			IsPrimary:     false,
		})
	}
	return locs
}
