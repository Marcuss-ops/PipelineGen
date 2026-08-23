package images

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	persistence "github.com/Marcuss-ops/PipelineGen/internal/application/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/defaults"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"

	"github.com/Marcuss-ops/PipelineGen/internal/application/images/destinations"
	capimages "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images"
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
	provider := asset.ClassifyImageProvider(source, generator)
	// PR-IMG-RETRIEVER-PROVIDER-FIX (July 2026): when source is a
	// URL (imageGeneratorLabel returns "web-download") the canonical
	// classifier cannot resolve a concrete provider (provider =
	// ProviderUnknown). The retrieval-chain orchestrator
	// (searchAndDownloadInnerDetailed) sets provCtx[RetrieverKey]
	// to the canonical provider name ("wikipedia" | "duckduckgo" |
	// "searxng" | ...) on the URL it routes through. Honouring that
	// canonical provenance here closes the silent-fallback signature
	// godlike/07 forbids (would otherwise land
	// retrieved_image_details.provider = "unknown" for every retrieved
	// row whose source URL does not match the provider-name table).
	// Scope is intentionally narrow: only override when the
	// classifier returned Unknown AND RetrieverKey is set to a
	// canonical non-"unknown" provider. AI-generated, upload-origin,
	// and Drive short-circuit callers are unaffected because their
	// initial classification already returns a concrete provider.
	if provider == asset.ProviderUnknown {
		if v, ok := ctx.Value(RetrieverKey).(string); ok {
			retriever := strings.TrimSpace(v)
			if retriever != "" && retriever != string(asset.ProviderUnknown) {
				provider = asset.ClassifyImageProvider(retriever, imageGeneratorLabel(retriever))
			}
		}
	}
	origin := asset.ClassifyImageOrigin(source, generator)
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

	// Drive is deliberately not called from ingestDirect. When delivery is
	// requested, this transaction emits an image.drive_delivery.requested
	// intent and the outbox worker performs the external publish later.
	// This keeps SQLite ownership ahead of every durable Drive side effect.
	var driveFileID string
	storedSourceURL := source
	overrideRoot := ""
	if !skipDrive && s.publisher == nil {
		return nil, fmt.Errorf("image ingest: Drive delivery publisher is unavailable")
	}
	if !skipDrive {
		// AI sources may provide a style-specific root override. Retrieved
		// images intentionally leave this empty so the image destination
		// registry remains the authority for the Drive root.
		overrideRoot = s.aiImageDriveRootForSource(source, style)
	}

	// A retrieved download has a URL as its source, so classifying the URL
	// alone cannot recover the concrete registry provider. Feed the resolved
	// provider into the canonical builder when available; this keeps the
	// metadata JSON and media_assets.provider aligned for DDG, Wikipedia,
	// SearXNG and the other retrieved providers.
	builderSource := source
	if provider != asset.ProviderUnknown {
		builderSource = string(provider)
	}
	builder := asset.NewCanonicalImageMetadataBuilder(builderSource, generator).
		WithBaseInfo(description, style, hash, tags, width, height).
		WithGenerator(generator)
	if metaResult != nil && metaResult.Payload != nil {
		payload := *metaResult.Payload
		payload.AssetID = hash
		builder.WithExtra(semanticPayloadToMap(&payload))
		tags = uniqueAppend(tags, payload.Tags...)
	}
	metaJSONStr, builtOrigin, builtProvider := builder.Build()
	// Ensure the subject slug survives in metadata_json so that
	// ListImagesBySubject can find cached images by subject_id.
	metaJSONStr = asset.AppendImageMetadataField(metaJSONStr, "subject_id", slug)
	metaJSON := []byte(metaJSONStr)
	// The builder is the SSOT for origin/provider; align the asset
	// record so that MetadataJSON.origin matches asset.Origin.
	origin = builtOrigin
	provider = builtProvider

	deliveryStatus := "ready"
	if !skipDrive {
		deliveryStatus = "delivery_pending"
	}
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
		Status:       deliveryStatus,
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
	initLifecycle, initIndex := asset.NewIndexableAssetState()
	commitReq := persistence.CommitRequest{
		AssetID:        hash,
		Source:         "image",
		Filename:       filename,
		MediaType:      string(asset.MediaTypeImage),
		ContentHash:    hash,
		LifecycleState: string(initLifecycle),
		IndexState:     string(initIndex),
		LocalPath:      localPath,
		FolderID:       s.driveFolderID,
		Title:          textutil.Truncate(description, 500),
		Description:    description,
		SourceURL:      storedSourceURL,
		Metadata: persistence.TypedMetadata{
			Title:       textutil.Truncate(description, 500),
			Origin:      string(origin),
			Description: description,
			// The outbox supersede gate compares this value with the
			// canonical metadata_json.content_hash. Keep both on the
			// same asset fingerprint; the embedding model version is
			// recorded separately below.
			SourceVersion: hash,
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
				"delivery_status":          deliveryStatus,
				"content_hash":             hash,
				"embedding_version_visual": defaults.VisualEmbeddingModelVersion,
				"subject_id":               slug,
			},
		},
		Origin:         string(origin),
		Provider:       string(provider),
		Locations:      buildImageIngestLocations(localPath, driveFileID, s.FormatDriveLink(driveFileID), hash, int64(len(content))),
		EmitIndexEvent: true,
	}
	if !skipDrive {
		payload, marshalErr := json.Marshal(capimages.ImageDriveDeliveryPayload{
			AssetID: hash, ContentHash: hash, LocalPath: localPath,
			Filename: filepath.Base(localPath), DestinationFolderID: overrideRoot,
			Style: style, Subject: slug, Group: slug, SourceVersion: 1,
		})
		if marshalErr != nil {
			return nil, fmt.Errorf("image ingest: build Drive delivery outbox payload: %w", marshalErr)
		}
		commitReq.AdditionalOutboxEvents = []persistence.OutboxEvent{{
			EventType:   capimages.EventTypeImageDriveDeliveryRequested,
			AggregateID: hash, AggregateType: "media_asset", PayloadJSON: string(payload),
			EventKey: "image-drive-delivery:" + hash + ":" + style + ":" + overrideRoot,
		}}
	}

	if _, err := s.committer.CommitAsset(ctx, commitReq); err != nil {
		// CommitAsset is idempotent: a unique-collision on a
		// previously-committed (AssetID, ContentHash) row returns
		// a successful CommitResult with zero new RowsAffected, NOT
		// an error. If we DO see an error here, surface it as-is —
		// the caller can decide whether to retry. The previous
		// GetImageByHash fallback path was retired because the new
		// canonical writer handles dedup atomically.
		//
		// PR-IMAGES-OUTBOX-TERMINAL-FALLBACK (July 2026): when the
		// outbox event is suppressed by an existing terminal row
		// (e.g. superseded by a newer aggregate version), the asset
		// write has already succeeded inside the transaction. Recover
		// the already-persisted asset by hash so the caller can treat
		// the ingest as an idempotent cache hit rather than a 500.
		if errors.Is(err, persistence.ErrAssetCommitOutboxTerminal) {
			if existing, getErr := s.repo.GetImageByHash(ctx, hash); getErr == nil && existing != nil {
				if s.log != nil {
					s.log.Info("image ingest: recovered existing asset after outbox terminal conflict",
						zap.String("hash", hash),
						zap.String("source", source),
						zap.Error(err))
				}
				return existing, nil
			}
		}
		return nil, fmt.Errorf("image ingest atomic commit: %w", err)
	}
	// Origin and provider are now written atomically inside the same
	// CommitAsset transaction (via CommitRequest.Origin/Provider →
	// media_assets.origin/provider columns). The post-commit UpdateOrigin
	// second write has been removed so a CommitAsset success followed by
	// an origin-provider write failure can never leave persisted rows
	// with empty origin/provider.
	//
	// Drive identity and delivery status are written only by the post-commit
	// worker; provenance needed by indexing is already part of the committed
	// metadata and the image projection above.
	return result, nil
}

func imageGeneratorLabel(source string) string {
	if d, ok := asset.DefaultProviderRegistry().Match(source); ok {
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
			LegacyFileMD5: hash,
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
			LegacyFileMD5: hash,
			IsPrimary:     false,
		})
	}
	return locs
}
