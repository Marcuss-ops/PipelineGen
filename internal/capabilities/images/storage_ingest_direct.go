package images

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"path/filepath"
	"strings"

	persistence "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	coreembedding "github.com/Marcuss-ops/PipelineGen/internal/kernel/embedding"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"

	"go.uber.org/zap"
)

// ingestDirect persists one downloaded image through the canonical commit
// transaction. The body reads as the ordered phases (subject inputs, file
// placement, metadata, delivery intent, commit, local index); each phase lives
// in a helper so the invariants below stay visible instead of buried.
//
// Invariants preserved from the pre-split body:
//   - subject/tag extraction and metadata tagging are fail-open (warn + continue);
//   - a nil committer is a typed fail-closed error, never a silent write;
//   - Drive delivery is an outbox intent, never an inline upload;
//   - CommitAsset is the single atomic writer (the old GetImageByHash fallback
//     survives only for the outbox-terminal recovery path).
func (s *ImageStorageService) ingestDirect(ctx context.Context, slug, style, genID string, content []byte, filename, source, description string, tags []string, hash string, skipDrive, skipMetadata bool) (*detail.ImageAsset, error) {
	slug, tags = s.resolveIngestSubjectInputs(ctx, slug, source, description, tags)
	if err := s.ensureIngestSubject(ctx, slug); err != nil {
		return nil, err
	}

	localPath, relativePath, err := s.placeIngestFile(ctx, slug, source, filename, content)
	if err != nil {
		return nil, err
	}

	generator := imageGeneratorLabel(source)
	provider := resolveIngestProvider(ctx, source, generator)
	width, height := decodeImageDimensions(content)
	metaResult := s.tagIngestMetadata(ctx, description, style, generator, hash, localPath, width, height)

	overrideRoot, deliveryStatus, err := s.resolveIngestDelivery(skipDrive, source, style)
	if err != nil {
		return nil, err
	}

	metaJSON, tags, origin, provider := buildIngestMetadata(metaResult, slug, description, style, hash, tags, width, height, generator, source, provider)

	driveFileID := ""
	result := &detail.ImageAsset{
		SubjectID:    slug,
		Hash:         hash,
		PathRel:      relativePath,
		LocalPath:    localPath,
		SourceURL:    source,
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

	if s.committer == nil {
		return nil, fmt.Errorf("%w: image ingest atomic commit refused", errImageIngestCommitterNil)
	}
	commitReq := s.newImageCommitRequest(imageCommitInput{
		slug: slug, style: style, genID: genID,
		filename: filename, source: source, desc: description,
		hash: hash, localPath: localPath, rel: relativePath,
		tags:  tags,
		width: width, height: height, sizeBytes: int64(len(content)),
		generator: generator, origin: origin, provider: provider,
		driveFileID: driveFileID, deliveryStatus: deliveryStatus,
	})
	if !skipDrive {
		event, err := imageDriveDeliveryEvent(imageDeliveryInput{hash: hash, localPath: localPath, filename: filename, style: style, slug: slug, overrideRoot: overrideRoot})
		if err != nil {
			return nil, err
		}
		commitReq.AdditionalOutboxEvents = append(commitReq.AdditionalOutboxEvents, event)
	}

	recovered, err := s.commitIngestedImage(ctx, commitReq, hash, source)
	if err != nil {
		return nil, err
	}
	if recovered != nil {
		return recovered, nil
	}
	s.indexIngestedImage(ctx, hash, description, slug, tags)
	return result, nil
}

// imageCommitInput carries the resolved per-ingest values into the canonical
// CommitRequest. Keeping it a struct avoids a twelve-argument builder whose
// positional order is the kind of thing a silent swap can break.
type imageCommitInput struct {
	slug, style, genID     string
	filename, source, desc string
	hash, localPath, rel   string
	tags                   []string
	width, height          int
	sizeBytes              int64
	generator              string
	origin                 asset.ImageOrigin
	provider               asset.ImageProvider
	driveFileID            string
	deliveryStatus         string
}

type imageDeliveryInput struct {
	hash, localPath, filename, style, slug, overrideRoot string
}

// resolveIngestSubjectInputs owns the fail-open extraction contract: a broken
// SubjectTagsService must not abort an ingest that already has a slug or tags.
func (s *ImageStorageService) resolveIngestSubjectInputs(ctx context.Context, slug, source, description string, tags []string) (string, []string) {
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
	return slug, tags
}

func (s *ImageStorageService) ensureIngestSubject(ctx context.Context, slug string) error {
	if subject, err := s.repo.GetSubjectBySlugOrAlias(ctx, slug); err == nil && subject != nil {
		return nil
	}
	if _, err := s.repo.CreateSubject(ctx, &asset.Subject{Slug: slug, DisplayName: slug}); err != nil {
		return fmt.Errorf("create subject %q: %w", slug, err)
	}
	return nil
}

func (s *ImageStorageService) placeIngestFile(ctx context.Context, slug, source, filename string, content []byte) (string, string, error) {
	ext := filepath.Ext(filename)
	if ext == "" {
		ext = ".jpg"
	}
	localPath, relativePath := LocalPathFor(s.imagesDir, slug, ingestPathSource(ctx, source), ext)
	if err := persistImageBytes(localPath, content); err != nil {
		return "", "", err
	}
	return localPath, relativePath, nil
}

// ingestPathSource maps a source URL to the on-disk segment. A URL must never
// become a directory (`images/https:/...`): the retrieval provider is the
// canonical segment, with "retrieved" as the fallback.
func ingestPathSource(ctx context.Context, source string) string {
	lower := strings.ToLower(strings.TrimSpace(source))
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return source
	}
	if retriever, ok := ctx.Value(RetrieverKey).(string); ok && strings.TrimSpace(retriever) != "" {
		return strings.TrimSpace(retriever)
	}
	return "retrieved"
}

func resolveIngestProvider(ctx context.Context, source, generator string) asset.ImageProvider {
	provider := detail.ClassifyImageProvider(source, generator)
	if provider != detail.ProviderUnknown {
		return provider
	}
	v, ok := ctx.Value(RetrieverKey).(string)
	if !ok {
		return provider
	}
	retriever := strings.TrimSpace(v)
	if retriever == "" || retriever == string(detail.ProviderUnknown) {
		return provider
	}
	return detail.ClassifyImageProvider(retriever, imageGeneratorLabel(retriever))
}

// tagIngestMetadata is non-fatal by contract: the image is already on disk and
// the caller still gets a valid asset, just with minimal metadata.
func (s *ImageStorageService) tagIngestMetadata(ctx context.Context, description, style, generator, hash, localPath string, width, height int) *SemanticWriteResult {
	metaResult, err := s.meta.tagImageMetadata(ctx, description, style, generator, hash, localPath, width, height)
	if err != nil {
		if s.log != nil {
			s.log.Warn("tagImageMetadata failed; continuing with minimal metadata",
				zap.Error(err),
				zap.String("hash", hash),
				zap.String("source", localPath))
		}
		return nil
	}
	return metaResult
}

// resolveIngestDelivery derives the delivery intent without touching Drive.
// A delivery-requested ingest with no publisher is a hard error: the intent
// would otherwise be lost silently.
func (s *ImageStorageService) resolveIngestDelivery(skipDrive bool, source, style string) (string, string, error) {
	if skipDrive {
		return "", "ready", nil
	}
	if s.publisher == nil {
		return "", "", fmt.Errorf("image ingest: Drive delivery publisher is unavailable")
	}
	return s.aiImageDriveRootForSource(source, style), "delivery_pending", nil
}

func buildIngestMetadata(metaResult *SemanticWriteResult, slug, description, style, hash string, tags []string, width, height int, generator, source string, provider asset.ImageProvider) ([]byte, []string, asset.ImageOrigin, asset.ImageProvider) {
	// A retrieved download has a URL as its source, so classifying the URL
	// alone cannot recover the concrete registry provider. Feed the resolved
	// provider into the canonical builder so metadata JSON and
	// media_assets.provider stay aligned.
	builderSource := source
	if provider != detail.ProviderUnknown {
		builderSource = string(provider)
	}
	builder := detail.NewCanonicalImageMetadataBuilder(builderSource, generator).
		WithBaseInfo(description, style, hash, tags, width, height).
		WithGenerator(generator)
	if metaResult != nil && metaResult.Payload != nil {
		payload := *metaResult.Payload
		payload.AssetID = hash
		builder.WithExtra(semanticPayloadToMap(&payload))
		tags = uniqueAppend(tags, payload.Tags...)
	}
	metaJSON, origin, builtProvider := builder.Build()
	// Keep the subject slug in metadata_json so ListImagesBySubject can find
	// cached images by subject_id.
	metaJSON = detail.AppendImageMetadataField(metaJSON, "subject_id", slug)
	return []byte(metaJSON), tags, origin, builtProvider
}

// newImageCommitRequest builds the canonical CommitRequest. AssetID and
// ContentHash both equal `hash`: the generated outbox event keeps its legacy
// aggregate_id == hash wire identity (the downstream dedup contract).
func (s *ImageStorageService) newImageCommitRequest(in imageCommitInput) persistence.CommitRequest {
	initLifecycle, initIndex := asset.NewIndexableAssetState()
	return persistence.CommitRequest{
		AssetID:        in.hash,
		Source:         "image",
		Filename:       in.filename,
		MediaType:      string(asset.MediaTypeImage),
		ContentHash:    in.hash,
		LifecycleState: string(initLifecycle),
		IndexState:     string(initIndex),
		LocalPath:      in.localPath,
		FolderID:       s.driveFolderID,
		Title:          textutil.Truncate(in.desc, 500),
		Description:    in.desc,
		SourceURL:      in.source,
		Metadata: persistence.TypedMetadata{
			Title:       textutil.Truncate(in.desc, 500),
			Origin:      string(in.origin),
			Description: in.desc,
			// The outbox supersede gate compares this value with the
			// canonical metadata_json.content_hash. Keep both on the same
			// asset fingerprint; the embedding model version is separate.
			SourceVersion:  in.hash,
			Tags:           in.tags,
			Slug:           in.slug,
			SizeBytes:      in.sizeBytes,
			SourceProvider: string(in.provider),
			// width + height live in Extra because TypedMetadata has no typed
			// slots for them: mirror the JSON keys produced above.
			Extra: map[string]any{
				"path_rel":                 in.rel,
				"width":                    in.width,
				"height":                   in.height,
				"origin":                   in.origin,
				"provider":                 in.provider,
				"generator":                in.generator,
				"style":                    in.style,
				"gen_id":                   in.genID,
				"drive_file_id":            in.driveFileID,
				"delivery_status":          in.deliveryStatus,
				"content_hash":             in.hash,
				"embedding_version_visual": coreembedding.VisualEmbeddingModelVersion,
				"subject_id":               in.slug,
			},
		},
		Origin:         string(in.origin),
		Provider:       string(in.provider),
		Locations:      buildImageIngestLocations(in.localPath, in.driveFileID, s.FormatDriveLink(in.driveFileID), in.hash, in.sizeBytes),
		EmitIndexEvent: true,
	}
}

func imageDriveDeliveryEvent(in imageDeliveryInput) (persistence.OutboxEvent, error) {
	payload, err := json.Marshal(ImageDriveDeliveryPayload{
		AssetID: in.hash, ContentHash: in.hash, LocalPath: in.localPath,
		Filename: filepath.Base(in.localPath), DestinationFolderID: in.overrideRoot,
		Style: in.style, Subject: in.slug, Group: in.slug, SourceVersion: 1,
	})
	if err != nil {
		return persistence.OutboxEvent{}, fmt.Errorf("image ingest: build Drive delivery outbox payload: %w", err)
	}
	return persistence.OutboxEvent{
		EventType:   EventTypeImageDriveDeliveryRequested,
		AggregateID: in.hash, AggregateType: "media_asset", PayloadJSON: string(payload),
		EventKey: "image-drive-delivery:" + in.hash + ":" + in.style + ":" + in.overrideRoot,
	}, nil
}

// commitIngestedImage returns the already-persisted asset when the outbox
// terminal gate suppresses the event (the transaction succeeded anyway), and
// nil,nil on a normal commit. Any other error is surfaced as-is.
func (s *ImageStorageService) commitIngestedImage(ctx context.Context, req persistence.CommitRequest, hash, source string) (*detail.ImageAsset, error) {
	if _, err := s.committer.CommitAsset(ctx, req); err != nil {
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
	return nil, nil
}

func (s *ImageStorageService) indexIngestedImage(ctx context.Context, hash, description, slug string, tags []string) {
	if s.localIndex == nil {
		return
	}
	searchQuery, _ := ctx.Value(SearchQueryKey).(string)
	indexDocument := strings.Join([]string{
		description,
		searchQuery,
		slug,
		strings.Join(tags, " "),
	}, " ")
	if indexErr := s.localIndex.Index(ctx, hash, indexDocument); indexErr != nil && s.log != nil {
		s.log.Warn("resolved image persisted but local semantic indexing failed; live providers remain available",
			zap.String("asset_id", hash), zap.Error(indexErr))
	}
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
			ContentSHA256: hash,
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
			ContentSHA256: hash,
			IsPrimary:     false,
		})
	}
	return locs
}
