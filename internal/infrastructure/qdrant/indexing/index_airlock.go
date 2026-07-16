// Package indexing — index_airlock.go: AssetData → IndexDocument airlock.
//
// Extracted from payload_mapper_document.go (July 2026).
// Owns: assetToIndexDocumentNoValidate, domainAssetLifecycle, AssetToIndexDocument.
package indexing

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	assetpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
)

// assetToIndexDocumentNoValidate is the unguarded AssetData →
// IndexDocument airlock. Used by the legacy BuildPayload wrapper path
// AND as the substrate for the Mapper.AssetToIndexDocument validation
// path. Returns an IndexDocument whose Embeddings map is populated
// for BOTH sparse channels (Values=nil) AND dense channels (Values
// captured when present).
//
// Lifecycle state is normalised via domainAssetLifecycle (empty falls
// back to ACTIVE post-migration 101). Title/Description are populated
// from the parsed metadata_json bag.
//
// ModelVersion is the OBSERVED provenance (PR 6 verdict §11) and
// defaults to spec.ModelVersion since AssetData carries no separate
// observed source today; future PRs that source observed-from-
// metadata_json flip the source without touching this helper. The
// validation pass (dim/NaN) lives in AssetToIndexDocument; this helper
// deliberately skips validation so producers without vectors (legacy
// AssetData shims, debug replay paths) still emit a complete-per-
// schema payload.
func assetToIndexDocumentNoValidate(asset *AssetData, schema *schema.IndexSchema) *IndexDocument {
	if asset == nil {
		return &IndexDocument{}
	}
	parseMetadataJSON(asset)

	title := asset.Title
	if title == "" {
		title = assetpkg.MetadataString(asset.Metadata, "title")
	}
	if title == "" {
		title = asset.Name
	}
	name := asset.Name
	if name == "" {
		name = title
	}
	description := asset.Description
	if description == "" {
		description = assetpkg.MetadataString(asset.Metadata, "description")
	}
	summary := asset.Summary
	if summary == "" {
		summary = assetpkg.MetadataString(asset.Metadata, "summary")
	}
	if summary == "" {
		summary = assetpkg.MetadataString(asset.Metadata, "clip_summary")
	}
	sourceURL := asset.SourceURL
	if sourceURL == "" {
		sourceURL = assetpkg.MetadataString(asset.Metadata, "source_url")
	}
	if sourceURL == "" {
		sourceURL = asset.YouTubeURL
	}
	sourceVideoID := asset.SourceVideoID
	if sourceVideoID == "" {
		sourceVideoID = parseSourceVideoID(assetpkg.MetadataString(asset.Metadata, "source_url"), assetpkg.MetadataString(asset.Metadata, "source_video_id"))
	}
	if sourceVideoID == "" {
		sourceVideoID = parseSourceVideoID(asset.YouTubeURL, asset.YouTubeVideoID)
	}
	destination := asset.Destination
	if destination == "" {
		destination = assetpkg.MetadataString(asset.Metadata, "destination")
	}
	// PR 6 (July 2026): removed the legacy fall-back to `string(asset.Source)`
	// for destination / source_provider / origin. The pre-PR-6 airlock
	// silently filled these from asset.Source as a "make sure we have
	// SOMETHING" default — exactly the godlike/07 NO-FAKE-AVAILABILITY
	// placeholder-string anti-pattern the new spec forbids. Callers that
	// want destination / source_provider / origin MUST set them
	// explicitly via top-level AssetData fields or MetadataJSON.
	sourceProvider := asset.SourceProvider
	if sourceProvider == "" {
		sourceProvider = assetpkg.MetadataString(asset.Metadata, "source_provider")
	}
	if sourceProvider == "" {
		sourceProvider = assetpkg.MetadataString(asset.Metadata, "origin_provider")
	}
	origin := asset.Origin
	if origin == "" {
		origin = assetpkg.MetadataString(asset.Metadata, "origin")
	}
	event := asset.Event
	if event == "" {
		event = assetpkg.MetadataString(asset.Metadata, "event")
	}
	round := asset.Round
	if round == 0 {
		round = assetpkg.MetadataInt(asset.Metadata, "round")
	}
	scene := asset.Scene
	if scene == "" {
		scene = assetpkg.MetadataString(asset.Metadata, "scene")
	}
	subject := asset.Subject
	if subject == "" {
		subject = assetpkg.MetadataString(asset.Metadata, "subject")
	}
	semanticTitle := assetpkg.MetadataString(asset.Metadata, "semantic_title")
	if semanticTitle == "" {
		semanticTitle = buildSemanticTitle(title, event, round, scene, subject)
	}
	embeddingText := assetpkg.MetadataString(asset.Metadata, "embedding_text")
	topics := assetpkg.MetadataStringSlice(asset.Metadata, "topics")
	speakers := assetpkg.MetadataStringSlice(asset.Metadata, "speakers")
	mentionedPeople := assetpkg.MetadataStringSlice(asset.Metadata, "mentioned_people")
	people := assetpkg.MetadataStringSlice(asset.Metadata, "people")
	sourceTags := assetpkg.MetadataStringSlice(asset.Metadata, "source_tags")
	clipTags := assetpkg.MetadataStringSlice(asset.Metadata, "clip_tags")
	searchKeywords := assetpkg.MetadataStringSlice(asset.Metadata, "search_keywords")
	entities := asset.Entities
	if len(entities) == 0 {
		entities = assetpkg.MetadataStringSlice(asset.Metadata, "entities")
	}
	if len(entities) == 0 {
		entities = mergeStringSlices(nil, people, speakers, mentionedPeople)
	}
	durationSec := 0
	if asset.DurationMs > 0 {
		durationSec = int(asset.DurationMs / 1000)
	}
	policyVersion := asset.PolicyVersion
	if policyVersion == "" {
		policyVersion = assetpkg.MetadataString(asset.Metadata, "policy_version")
	}
	if policyVersion == "" {
		policyVersion = asset.IndexVersion
	}
	drivePath := cleanDrivePath(assetpkg.MetadataString(asset.Metadata, "drive_path"))
	folderID := asset.FolderID
	if folderID == "" {
		folderID = assetpkg.MetadataString(asset.Metadata, "folder_id")
	}
	folderPath := asset.FolderPath
	if folderPath == "" {
		folderPath = assetpkg.MetadataString(asset.Metadata, "folder_path")
	}
	driveLink := firstNonEmpty(asset.DriveLink, assetpkg.MetadataString(asset.Metadata, "drive_link"))
	// PR-CATALOG-MULTILINGUA step 6 (July 2026): SemanticHash carries
	// the canonical pre-resolved value computed by AssetStore with the
	// godlike/06 SSOT precedence rule
	// (asset_visual_summaries.source_hash ∪ media_assets.semantic_hash).
	// The airlock trusts AssetStore as the single canonical owner of
	// the precedence and just forwards the resolved value. Empty when
	// neither source has populated a hash yet (godlike/07
	// NO-FAKE-AVAILABILITY: empty = "no hash yet", NOT a placeholder).
	currentSemanticHash := asset.SemanticHash
	// PR-CATALOG-MULTILINGUA step 6 (July 2026): Transcripts carries
	// the multilingual transcript slice populated by AssetStore from
	// asset_text_tracks rows where text_kind='transcript' AND
	// is_current=1. The composer iterates the slice and renders the
	// IsOriginal row as bare text first, then each remaining row as
	// `transcript ({lang}): {text}`. Empty when no is_current=1
	// transcript rows exist for the asset.
	//
	// legacyTranscript is the pre-step-6 single-string fallback for
	// backward compat with old callers (admin CLI, fixtures) that
	// haven't yet surfaced the TextTrackQuerier pipeline. AssetStore
	// v1 reads `transcript` from metadata_json ONLY as a one-time
	// bridge; producers that have an is_current row revert to the
	// canonical slice and the single-string key becomes vestigial.
	legacyTranscript := strings.TrimSpace(assetpkg.MetadataString(asset.Metadata, "transcript"))
	indexingStatus := assetpkg.MetadataString(asset.Metadata, "indexing_status")
	// PR-TIMESTAMP-FOLDER-LINK (July 2026): parent timestamp Drive
	// folder metadata. Read from top-level AssetData first, fall
	// back to metadata_json for backward compat with old callers
	// that haven't yet surfaced these fields explicitly.
	timestampDriveFolderLink := asset.TimestampDriveFolderLink
	if timestampDriveFolderLink == "" {
		timestampDriveFolderLink = assetpkg.MetadataString(asset.Metadata, "timestamp_drive_folder_link")
	}
	timestampFolderID := asset.TimestampFolderID
	if timestampFolderID == "" {
		timestampFolderID = assetpkg.MetadataString(asset.Metadata, "timestamp_folder_id")
	}

	// EmbeddingArtifact population needs the dimensional vectors; resolve
	// them once so we don't recurse via the Mapper from a free function.
	// The legacy build path uses AssetData vectors directly; the Mapper
	// airlock does dimension/NaN validation for the new BuildPayload
	// survivors. Both paths converge on the same per-channel artifacts.
	doc := &IndexDocument{
		AssetID:        asset.ID,
		WorkspaceID:    asset.WorkspaceID,
		LifecycleState: domainAssetLifecycle(asset.LifecycleState),
		SourceVersion:  asset.SourceVersion,
		ContentHash:    asset.ContentHash,
		SearchText:     asset.SearchText, // legacy pass-through (BuildPayload entry); mapper receivers override via AssetToIndexDocument
		Metadata: IndexedMetadata{
			Summary:          summary,
			Name:             name,
			Title:            title,
			Description:      description,
			Tags:             asset.Tags,
			Source:           asset.Source,
			MediaType:        asset.MediaType,
			AssetRole:        firstNonEmpty(asset.AssetRole, assetpkg.MetadataString(asset.Metadata, "asset_role")),
			NormalizedGroup:  firstNonEmpty(asset.NormalizedGroup, assetpkg.MetadataString(asset.Metadata, "normalized_group")),
			HasDialogue:      metadataBoolPtr(asset.Metadata, asset.HasDialogue),
			AudioProfile:     firstNonEmpty(asset.AudioProfile, assetpkg.MetadataString(asset.Metadata, "audio_profile")),
			Language:         asset.Language,
			Category:         asset.Category,
			Style:            asset.Style,
			DurationMs:       asset.DurationMs,
			DurationSec:      durationSec,
			SourceURL:        sourceURL,
			YouTubeID:        asset.YouTubeVideoID,
			YouTubeURL:       asset.YouTubeURL,
			SourceVideoID:    sourceVideoID,
			StartTime:        asset.StartTime,
			EndTime:          asset.EndTime,
			YouTubeChan:      asset.ChannelID,
			License:          asset.License,
			IndexVersion:     asset.IndexVersion,
			SourceProvider:   sourceProvider,
			Origin:           origin,
			Destination:      destination,
			SemanticTitle:    semanticTitle,
			EmbeddingText:    embeddingText,
			Event:            event,
			Round:            round,
			Scene:            scene,
			Subject:          subject,
			Topics:           topics,
			Speakers:         speakers,
			MentionedPeople:  mentionedPeople,
			People:           people,
			SourceTags:       sourceTags,
			ClipTags:         clipTags,
			SearchKeywords:   searchKeywords,
			Entities:         entities,
			Hook:             assetpkg.MetadataString(asset.Metadata, "hook"),
			SearchVisibility: assetpkg.MetadataString(asset.Metadata, "search_visibility"),
			JobID:            firstNonEmpty(asset.JobID, assetpkg.MetadataString(asset.Metadata, "job_id")),
			WorkflowID:       firstNonEmpty(asset.WorkflowID, assetpkg.MetadataString(asset.Metadata, "workflow_id")),
			RunFingerprint:   firstNonEmpty(asset.RunFingerprint, assetpkg.MetadataString(asset.Metadata, "run_fingerprint")),
			ChunkIndex:       intOrFallback(asset.ChunkIndex, assetpkg.MetadataInt(asset.Metadata, "chunk_index")),
			TotalChunks:      intOrFallback(asset.TotalChunks, assetpkg.MetadataInt(asset.Metadata, "total_chunks")),
			PolicyVersion:    policyVersion,
			DrivePath:        drivePath,
			FolderID:         folderID,
			FolderPath:       folderPath,
			// DriveLink is now a canonical payload key (no longer
			// forbidden on IndexDocument, see PR 6 doctrine update).
			DriveLink: driveLink,
			// CurrentSemanticHash is the canonical semantic block
			// fingerprint (SSOT: media_assets.semantic_hash +
			// asset_visual_summaries.source_hash precedence,
			// see semantic_hash precedence rule in the airlock
			// docstring).
			CurrentSemanticHash: currentSemanticHash,
			// Transcript (single string) is the pre-step-6
			// backward-compat fallback field. The canonical
			// multilingual composer reads Transcripts (slice) instead.
			// Producers that haven't yet adopted the TextTrackQuerier
			// pipeline still surface a non-empty Transcript verbatim.
			Transcript:     legacyTranscript,
			Transcripts:    asset.Transcripts,
			IndexingStatus: indexingStatus,
			// PR-TIMESTAMP-FOLDER-LINK (July 2026): parent timestamp
			// folder metadata propagated through the airlock (top-level
			// first, metadata_json fallback).
			TimestampDriveFolderLink: timestampDriveFolderLink,
			TimestampFolderID:        timestampFolderID,
			CreatedAt:                asset.CreatedAt,
			UpdatedAt:                asset.UpdatedAt,
			DeletedAt:                asset.DeletedAt,
			// ── Text track projection ──
			OriginalLanguage:    assetpkg.MetadataString(asset.Metadata, "original_language"),
			AvailableLanguages:  assetpkg.MetadataStringSlice(asset.Metadata, "available_languages"),
			TranscriptAvailable: metadataBool(asset.Metadata, "transcript_available"),
			TextTracksVersion:   assetpkg.MetadataString(asset.Metadata, "text_tracks_version"),

			// ── VLM visual summary block (FASE-9 + visual-summary reindex) ────
			// Verbatim copy from AssetData (top-level first-class fields; the
			// visual-summary reindex service populates these when it runs a
			// new VLM pass; the payload builder emits them with omitempty).
			VisualSummary:              asset.VisualSummary,
			VisibleActions:             asset.VisibleActions,
			VisibleEntities:            asset.VisibleEntities,
			VisualPreprocessingVersion: asset.VisualPreprocessingVersion,
			VisualModelName:            asset.VisualModelName,
			VisualModelVersion:         asset.VisualModelVersion,
		},
		Embeddings: map[VectorChannel]EmbeddingArtifact{},
	}

	// Sparse channels: each declared sparse channel becomes an
	// EmbeddingArtifact (Model from spec, Values=nil — Qdrant does
	// server-side inference). schema.SparseSpec carries no ModelVersion or
	// PreprocessVer — those fields live on schema.EmbeddingSpec (dense) only.
	for _, spec := range schema.SparseVectors {
		channel := VectorChannel(spec.Channel)
		model := spec.Model
		if model == "" {
			model = "qdrant/bm25"
		}
		doc.Embeddings[channel] = EmbeddingArtifact{
			Channel:    channel,
			Model:      model,
			Dimensions: -1, // sparse: Qdrant infers server-side
		}
	}

	// Dense channels: each schema-declared channel becomes an
	// EmbeddingArtifact. Values is populated when the AssetData has a
	// vector for that channel; nil when absent (IndexDocumentToPoint
	// drops the channel from the wire). ModelVersion is the OBSERVED
	// provenance (verdict §11); the validation pass lives in
	// AssetToIndexDocument.
	for _, spec := range schema.DenseVectors {
		channel := VectorChannel(spec.Channel)
		doc.Embeddings[channel] = EmbeddingArtifact{
			Channel:       channel,
			Model:         spec.Model,
			ModelVersion:  spec.ModelVersion, // OBSERVED (write-only provenance; defaults to schema today)
			PreprocessVer: spec.PreprocessVer,
			Dimensions:    spec.Dimensions,
		}
	}
	return doc
}

// domainAssetLifecycle converts a media_assets.lifecycle_state string
// to the canonical asset.LifecycleState. Empty → ACTIVE (legacy rows
// post-migration 101 fall through to ACTIVE; canonical fallback).
func domainAssetLifecycle(raw string) assetpkg.LifecycleState {
	const fallback = "ACTIVE"
	if raw == "" {
		return assetpkg.LifecycleState(fallback)
	}
	return assetpkg.LifecycleState(raw)
}

func buildSemanticTitle(title, event string, round int, scene, subject string) string {
	if title != "" {
		return strings.Join(dedupTrimmedStrings(title), " ")
	}
	parts := make([]string, 0, 4)
	if event != "" && event != title {
		parts = append(parts, event)
	}
	if round > 0 {
		parts = append(parts, "round "+strconv.Itoa(round))
	}
	if scene != "" && scene != title {
		parts = append(parts, scene)
	}
	if subject != "" && subject != title {
		parts = append(parts, subject)
	}
	return strings.Join(dedupTrimmedStrings(parts...), " ")
}

func dedupTrimmedStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		key := strings.ToLower(v)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, v)
	}
	return out
}

func mergeStringSlices(slices ...[]string) []string {
	out := make([]string, 0)
	seen := make(map[string]struct{})
	for _, slice := range slices {
		for _, v := range slice {
			v = strings.TrimSpace(v)
			if v == "" {
				continue
			}
			key := strings.ToLower(v)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, v)
		}
	}
	return out
}

func parseSourceVideoID(sourceURL, fallback string) string {
	sourceURL = strings.TrimSpace(sourceURL)
	if sourceURL == "" {
		return strings.TrimSpace(fallback)
	}
	if strings.Contains(sourceURL, "youtube.com") || strings.Contains(sourceURL, "youtu.be") {
		if idx := strings.LastIndex(sourceURL, "v="); idx >= 0 {
			id := sourceURL[idx+2:]
			if amp := strings.IndexByte(id, '&'); amp >= 0 {
				id = id[:amp]
			}
			id = strings.TrimSpace(id)
			if id != "" {
				return id
			}
		}
		if idx := strings.LastIndex(sourceURL, "/"); idx >= 0 && idx < len(sourceURL)-1 {
			id := sourceURL[idx+1:]
			if q := strings.IndexByte(id, '?'); q >= 0 {
				id = id[:q]
			}
			id = strings.TrimSpace(id)
			if id != "" {
				return id
			}
		}
	}
	return strings.TrimSpace(fallback)
}

func cleanDrivePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.ToSlash(path)
}

// metadataBool reads a boolean value from the metadata map.
// Returns false when the key is absent or not a boolean.
func metadataBool(meta map[string]any, key string) bool {
	if meta == nil {
		return false
	}
	v, ok := meta[key]
	if !ok {
		return false
	}
	b, ok := v.(bool)
	return b && ok
}

func metadataBoolPtr(meta map[string]any, top *bool) *bool {
	if top != nil {
		return top
	}
	if meta == nil {
		return nil
	}
	v, ok := meta["has_dialogue"].(bool)
	if !ok {
		return nil
	}
	return &v
}

// intOrFallback returns the top-level AssetData field if non-zero,
// otherwise the MetadataJSON-derived fallback (godlike/06 SSOT
// airlock precedence contract for int fields). The canonical
// firstNonEmpty helper (in payload_builder.go) handles the same
// contract for strings; the int counterpart uses 0 as the "not set"
// sentinel.
//
// Caveat (forward-pointer PR-ASSETDATA-INT-SENTINEL): for int
// fields where 0 is a legitimate value (e.g. ChunkIndex=0 for the
// first chunk), a caller that explicitly sets top-level=0 and also
// has a stale Metadata entry will get the Metadata value. Acceptable
// for the current state; a future PR can swap to a pointer-based
// sentinel if the conflict becomes real.
func intOrFallback(top, fallback int) int {
	if top != 0 {
		return top
	}
	return fallback
}

// AssetToIndexDocument is the canonical Mapper airlock (PR 6). Builds
// an IndexDocument from a SQL-fetch AssetData and validates the
// per-channel vector dimensions / NaN / Inf before the wire is
// constructed. Returns the same typed errors as AssetToPoint
// (transport.ErrEmptyVector / transport.ErrVectorDimensionMismatch / transport.ErrNaNOrInf) so the
// upstream IndexWriter fail-closed at the type assertion already
// caught in BuildProcessBundle (`var _ clipindexer.VectorStoreIndexer
// = (*qdrant.IndexWriter)(nil)`) keeps behaving identically.
//
// The airlock strips Status / DriveLink / LocalPath at the
// IndexDocument boundary; the IndexDocument struct has no such fields,
// so the wire-shape invariant is enforced statically (frozen test in
// composition_test.go::TestComposition_FrozenQdrantIndexDocument
// CanonicalTypes) AND dynamically (wire-shape test in
// payload_mapper_test.go::TestBuildPayloadFromDocument_NoForbidden
// LocatorKeys).
func (m *PayloadMapper) AssetToIndexDocument(ctx context.Context, asset *AssetData, schema *schema.IndexSchema) (*IndexDocument, error) {
	if asset == nil {
		return nil, fmt.Errorf("asset is nil")
	}
	if asset.ID == "" {
		return nil, fmt.Errorf("asset ID must not be empty")
	}
	doc := assetToIndexDocumentNoValidate(asset, schema)
	// Route BM25 search-text through the canonical SearchTextBuilder
	// (registered via SetSearchTextBuilder at composition root). The
	// helper falls back to asset.SearchText when the builder is nil
	// or returns empty — the contract preserves the pre-existing DB
	// pre-build path for legacy rows.
	doc.SearchText = m.resolveSearchText(ctx, asset)
	for _, spec := range schema.DenseVectors {
		channel := VectorChannel(spec.Channel)
		vec := m.getVectorForChannel(asset, spec.Channel)
		if spec.Channel == "visual" && len(vec) > 0 && len(vec) != spec.Dimensions {
			if normalized, err := resampleFloat32Vector(vec, spec.Dimensions); err == nil {
				vec = normalized
			}
		}

		// Task 4 (July 2026): route through the canonical 5-step
		// validation helper instead of inline ad-hoc checks.
		// validateDenseVector returns nil for:
		//   - valid vectors (all checks pass)
		//   - optional-channel nil vectors (silent skip)
		// Returns typed error for:
		//   - required-channel nil → ErrMissingRequiredVector
		//   - zero-length vector   → ErrEmptyVector
		//   - dimension mismatch   → ErrVectorDimensionMismatch
		//   - NaN/Inf             → ErrNaNOrInf
		if err := validateDenseVector(spec.Channel, vec, spec.Dimensions, asset.ID); err != nil {
			return nil, err
		}
		if vec == nil {
			continue // optional channel, absent is allowed
		}

		doc.Embeddings[channel] = EmbeddingArtifact{
			Channel:       channel,
			Values:        vec,
			Model:         spec.Model,
			ModelVersion:  spec.ModelVersion,
			PreprocessVer: spec.PreprocessVer,
			Dimensions:    spec.Dimensions,
			GeneratedAt:   time.Now(),
		}
	}
	return doc, nil
}
