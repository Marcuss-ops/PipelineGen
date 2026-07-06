// Package indexing — payload_mapper_searchtext.go: search text helpers.
//
// Extracted from payload_mapper.go (July 2026, PR-PAYLOAD-MAPPER-SPLIT).
// Owns: parseMetadataJSON, buildSearchTextInput, resolveSearchText.
package indexing

import (
	"context"
	"encoding/json"

	"go.uber.org/zap"

	appsearchtext "github.com/Marcuss-ops/PipelineGen/internal/application/indexing/searchtext"
	assetpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// parseMetadataJSON lazily parses metadata JSON on first access.
func parseMetadataJSON(asset *AssetData) {
	if asset.Metadata != nil || asset.MetadataJSON == "" {
		return
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(asset.MetadataJSON), &m); err == nil {
		asset.Metadata = m
	}
}

// buildSearchTextInput maps an AssetData row to the canonical
// SearchTextInput that the per-source strategies consume. The mapping
// is intentionally permissive: every field on SearchTextInput is a
// zero-or-more-value contract; strategies only read the subset they
// document (per `internal/infrastructure/indexing/searchtext/strategies.go`).
//
// AssetData field priorities:
//   - Title         ← asset.Name (canonical human-readable label)
//   - Tags          ← asset.Tags (already-typed []string)
//   - Category      ← asset.Category
//   - Channel       ← asset.ChannelID (YouTube channel name)
//   - Language      ← asset.Language (BCP-47)
//   - Description   ← metadata_json.$.description (when parsed)
//   - Transcript    ← metadata_json.$.transcript ONLY (no asset.SearchText
//     fallback — search_text is the BM25 OUTPUT, never the
//     raw transcript; falling back would feed the assembled
//     search text back into the YouTube strategy's
//     transcript slot and double-count it).
//   - Caption       ← metadata_json.$.caption
//   - Prompt        ← metadata_json.$.prompt
//   - DetectedEntities ← metadata_json.$.detected_entities
//   - OriginProvider   ← metadata_json.$.origin_provider
//
// The function returns a SearchTextInput that ALWAYS carries the
// canonical identifiers (AssetID, Source, MediaType) so the registry
// can dispatch on Source even when the freetext bag is empty.
func buildSearchTextInput(asset *AssetData) appsearchtext.SearchTextInput {
	if asset == nil {
		return appsearchtext.SearchTextInput{}
	}
	parseMetadataJSON(asset)
	return appsearchtext.SearchTextInput{
		AssetID:          asset.ID,
		Source:           asset.Source,
		MediaType:        asset.MediaType,
		Title:            asset.Name,
		Description:      assetpkg.MetadataString(asset.Metadata, "description"),
		Transcript:       assetpkg.MetadataString(asset.Metadata, "transcript"),
		Prompt:           assetpkg.MetadataString(asset.Metadata, "prompt"),
		Caption:          assetpkg.MetadataString(asset.Metadata, "caption"),
		Tags:             asset.Tags,
		Category:         asset.Category,
		Language:         asset.Language,
		Channel:          asset.ChannelID,
		DetectedEntities: assetpkg.MetadataStringSlice(asset.Metadata, "detected_entities"),
		OriginProvider:   assetpkg.MetadataString(asset.Metadata, "origin_provider"),
	}
}

// resolveSearchText is the canonical call site for search-text
// construction at indexing time. It owns the precedence order:
//
//  1. SearchTextBuilder (when wired via SetSearchTextBuilder) — returns
//     the per-source canonical text. A nil error is treated as "ok"
//     even when Build returns empty (legitimate empty strategies).
//     A non-nil error is logged at DEBUG and falls through — we
//     never block the IndexDocument on a search-text builder
//     failure (godlike/07 no-fake-availability contract).
//  2. asset.SearchText — the DB-stored pre-built value. Always the
//     graceful fallback when the builder is absent OR returns empty.
//
// Always returns a non-nil string (possibly "") so callers can write
// doc.SearchText directly without nil-checks.
//
// IMPORTANT: returns asset.SearchText UNCHANGED when the builder is nil
// — this is the byte-for-byte contract the pre-existing
// TestAssetToPoint_SparseVector_HasServerSideShape and other tests
// (which call NewPayloadMapper(store, log) WITHOUT SetSearchTextBuilder)
// pin. Production code paths (NewRuntime) wire the registry; admin
// CLI / tests / fixtures that bypass NewRuntime see the legacy
// pass-through behavior.
func (m *PayloadMapper) resolveSearchText(ctx context.Context, asset *AssetData) string {
	if asset == nil {
		return ""
	}
	if m.searchTextBuild != nil {
		input := buildSearchTextInput(asset)
		text, err := m.searchTextBuild.Build(ctx, input)
		if err != nil {
			if m.log != nil {
				// godlike/07 no-fake-availability: surface strategy
				// failures at Warn so operator dashboards catch a
				// panicking or misconfigured strategy. The mapper
				// still degrades gracefully (falls through to
				// asset.SearchText) so a single broken strategy
				// does not block the IndexDocument, but the
				// degradation is visible.
				m.log.Warn("SearchTextBuilder.Build error; falling back to asset.SearchText",
					zap.String("asset_id", asset.ID),
					zap.Error(err))
			}
			// Fall through to asset.SearchText — never block
			// the IndexDocument on a search-text builder issue.
		} else if text != "" {
			return text
		}
	}
	return asset.SearchText
}
