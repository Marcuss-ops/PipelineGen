// Package app — wire_script_sources.go.
//
// FASE 2.A PR2 (June 2026) split: the source-resolver adapter
// types moved out of wire_script.go. Each adapter bridges a
// concrete-infrastructure dependency (Qdrant Searcher, SQLite
// ClipsRepository, ports.ClipSearchPort) into the typed-port
// shape that source-resolver constructors in
// internal/application/scripts/usecase (NewTextSourceResolver,
// NewClipsSourceResolver, NewSearchSourceResolver,
// NewCurateSourceResolver) consume.
//
// Package boundary: same `package app` as wire_script.go. The
// types here are constructed inline in wireScriptFlow (the
// composition root call site), and the bridge signatures are
// tightly coupled to specific catalogue search / clip-search /
// port-bridge responsibilities — promoting them to a sub-package
// would force wire_script.go to import a new symbol while
// preserving the same construction order. Keeping them in
// `package app` mirrors the pattern of adapters_infra.go and
// clips_adapters_*.go (composition-root-local adapter structs
// that bridge infrastructure to application-layer ports).
//
// Cross-references:
//   - internal/app/wire_script.go: the caller (wireScriptFlow
//     constructs & uses every type here inline).
//   - internal/application/scripts/usecase: the typed-port
//     shapes each adapter implements (SemanticSearchPort,
//     ClipSearchPort).
//   - internal/application/scripts/ports: ClipSearchQuery
//     (curate-resolver search input shape).
//   - internal/kernel/script: SearchResultItem
//     (typed curatior-resolver search output shape).
//   - internal/infrastructure/qdrant: Searcher + IndexWriter +
//     NewTextEmbedderAdapter (Qdrant-backed engine ports).
//   - internal/infrastructure/database/sqlite/assets:
//     *ClipsRepository (SQLite-backed clip index).
package app

import (
	"context"
	"fmt"
	"strings"

	scriptapi "github.com/Marcuss-ops/PipelineGen/internal/api/script"
	usecase "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"

	sqassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	qdrantschema "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/search"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"go.uber.org/zap"
)

// qdrantSemanticSearchPort implements usecase.SemanticSearchPort
// directly against the Qdrant Searcher (via SearchByText), bypassing
// the removed realtime package. SourceSearch sources resolve through
// Qdrant semantic search. Replaces the dead semanticSearchAdapter
// which depended on the nil root.Domains.RealtimeSearch.
//
// Wiring contract (see wire_script.go::wireScriptFlow):
//
//	searchPort := &qdrantSemanticSearchPort{
//	    searcher:   root.Process.QdrantSearcher,
//	    embedder:   ollamaEmbedder,
//	    vectorName: "text",
//	    log:        log,
//	}
//	sourceReg.Register(scriptpkg.SourceSearch,
//	    usecase.NewSearchSourceResolver(searchPort, clipSourceBuilder, log))
//
// Nil-tolerance: the SearchByText method short-circuits to
// (nil, nil) when the receiver or any of its constructors are
// nil. This preserves the pre-typed-port behavior where a missing
// Qdrant client quietly omitted the SourceSearch resolver from
// the registry instead of failing composition.
type qdrantSemanticSearchPort struct {
	searcher *search.Searcher
	embedder search.TextEmbedder
	// hydrator is deliberately the canonical SQLite resolver. Qdrant is
	// only a projection: a point is never accepted as a clip until the
	// exact payload asset_id exists in media_assets.
	hydrator interface {
		ResolveByMediaAssetID(context.Context, string) (*asset.Asset, error)
	}
	vectorName string
	log        *zap.Logger
}

// SearchByText implements usecase.SemanticSearchPort. The output
// shape (SemanticSearchResult) uses ClipID as the asset-key
// carrier — the field-rename (Payload["asset_id"] → ClipID) is
// the single seam between Qdrant-shaped payloads and the
// source-resolver typed-port contract.
func (p *qdrantSemanticSearchPort) SearchByText(ctx context.Context, query string, limit int, language string) ([]usecase.SemanticSearchResult, error) {
	if p == nil || p.searcher == nil || p.embedder == nil || p.hydrator == nil {
		return nil, fmt.Errorf("qdrant semantic search: canonical SQLite hydration is unavailable")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return []usecase.SemanticSearchResult{}, nil
	}
	if limit <= 0 {
		limit = 10
	}
	// Do not apply a second ANN score threshold here.  The shared sampler
	// owns the acceptance floor after SQLite hydration; keeping the Qdrant
	// request unthresholded lets the resolver over-fetch stale/remote-only
	// rows and still discover a renderable local clip in the result window.
	minScore := 0.0

	// Overfetch because the shared media index also contains voiceover
	// assets. Keep semantic clip search restricted to published media and
	// exclude voiceover rows before the canonical sampler ranks candidates.
	vec, err := p.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("qdrant semantic search: embed query: %w", err)
	}
	searchLimit := limit * 3
	if searchLimit < 30 {
		searchLimit = 30
	}
	results, err := p.searcher.HybridSearch(ctx, qdrantschema.HybridSearchRequest{
		DenseVector:      vec,
		DenseVectorName:  p.vectorName,
		SparseVectorName: "bm25_text",
		SparseText:       query,
		SparseModel:      qdrantschema.DefaultSparseModel,
		Limit:            searchLimit,
		MinScore:         minScore,
		Filter: map[string]any{
			// Older clip projections use media_type=clip; the current
			// canonical media registry uses media_type=video. Both are
			// video clip evidence and must enter the same hydration gate.
			"should": map[string]any{
				"conditions": []any{
					map[string]any{"key": "media_type", "match": map[string]any{"value": "clip"}},
					map[string]any{"key": "media_type", "match": map[string]any{"value": "video"}},
				},
				"min_count": 1,
			},
			// Clip evidence is indexed as ACTIVE while stock/media
			// projections are commonly PUBLISHED. Keep the source
			// resolver aligned with the canonical search lifecycle
			// allow-list used by /api/media/search.
			"min_should": map[string]any{
				"conditions": []any{
					map[string]any{"key": "lifecycle_state", "match": map[string]any{"value": "ACTIVE"}},
					map[string]any{"key": "lifecycle_state", "match": map[string]any{"value": "PUBLISHED"}},
				},
				"min_count": 1,
			},
			"must_not": []any{
				map[string]any{"key": "source", "match": map[string]any{"value": "voiceover"}},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("qdrant semantic search: %w", err)
	}

	out := make([]usecase.SemanticSearchResult, 0, len(results))
	hydrationFailed := 0
	pointWithoutAsset := 0
	for _, r := range results {
		assetID := qdrantPayloadStr(r.Payload, "asset_id")
		if assetID == "" {
			pointWithoutAsset++
			hydrationFailed++
			continue
		}
		canonical, resolveErr := p.hydrator.ResolveByMediaAssetID(ctx, assetID)
		if resolveErr != nil || canonical == nil || strings.TrimSpace(canonical.ID) != assetID {
			hydrationFailed++
			continue
		}
		out = append(out, usecase.SemanticSearchResult{
			ClipID: assetID,
			Name:   firstQdrantPayloadString(r.Payload, "name", "title", "semantic_title"),
			Score:  r.Score,
			// Qdrant payloads from the current media projection use
			// embedding_text; older projections used search_text. Keep
			// the compatibility resolution here, at the single wire seam.
			Transcript:    firstQdrantPayloadString(r.Payload, "search_text", "transcript"),
			VisualSummary: firstQdrantPayloadString(r.Payload, "description", "embedding_text", "semantic_title", "title", "name"),
			MediaType:     qdrantPayloadStr(r.Payload, "media_type"),
			DriveLink:     qdrantPayloadStr(r.Payload, "drive_link"),
			AvailableByIngest: isSearchableClipLifecycle(
				qdrantPayloadStr(r.Payload, "lifecycle_state"),
			),
			AnchorCoverageRatio: 1.0,
		})
	}
	if p.log != nil {
		p.log.Info("qdrant semantic search hydration gate",
			zap.Int("qdrant_results", len(results)),
			zap.Int("sqlite_hydrated", len(out)),
			zap.Int("hydration_failed", hydrationFailed),
			zap.Int("accepted_clips", len(out)),
			zap.Int("qdrant_point_without_sqlite_asset", pointWithoutAsset),
		)
	}
	return out, nil
}

func firstQdrantPayloadString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := qdrantPayloadStr(payload, key); value != "" {
			return value
		}
	}
	return ""
}

func isSearchableClipLifecycle(state string) bool {
	switch strings.ToUpper(strings.TrimSpace(state)) {
	case "ACTIVE", "PUBLISHED":
		return true
	default:
		return false
	}
}

// qdrantPayloadStr reads a string-valued key from a Qdrant payload
// map. Lives here (composition-root) because its only consumer is
// qdrantSemanticSearchPort.SearchByText; promoting it to the
// qdrant/ package would force a `map[string]any` accessor
// onto the infra layer for a single call site.
func qdrantPayloadStr(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	v, ok := payload[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

// ── ClipSearchPort type bridge ──────────────────────────────────────
//
// The qdrant-backed `scriptports.ClipSearchPort` produced by
// qdrant.NewClipSearchAdapter returns `[]ports.ClipSearchHit`.
// The typed `usecase.ClipSearchPort` expected by
// `curateResolver.SetClipSearchPort` returns
// `[]scriptpkg.SearchResultItem`. Field mapping is:
// AssetID → ClipID (the only rename); Name / Score / Source are
// 1:1; DriveLink has no source field so it stays empty.
//
// The bridge lives here (composition root, not the qdrant
// adapter) so the infra package stays oblivious to the
// usecase-typed SearchResultItem. Single bridge struct covers
// the two consumers (curateResolver + mediaCurator) per
// AGENTS.md Pattern 0 ("don't duplicate the seam in N places").
// ── ClipName search adapter (PR-FIX, June 2026) ─────────────────────
//
// Bridges the SQLite ClipsRepository → scriptapi.ClipSearcher for
// the lightweight GET /script/clips/search?q= clip-discovery
// endpoint. Direct SQL query (avoiding the MediaAssetColumns
// constant that references web_view_link — a column living in
// asset_locations, not in media_assets).
type clipsNameSearchAdapter struct {
	repo *sqassets.ClipsRepository
}

// SearchByName is the canonical scriptapi.ClipSearcher
// implementation for the lightweight clip-discovery endpoint.
// The SELECT avoids the MediaAssetColumns constant that pulls in
// web_view_link (which lives on asset_locations, not on
// media_assets) — selecting only the 4 columns the endpoint
// needs preserves the existing column-resolution contract.
func (a *clipsNameSearchAdapter) SearchByName(ctx context.Context, query string, limit int) ([]scriptapi.ClipSearchHit, error) {
	if a == nil || a.repo == nil {
		return nil, nil
	}
	ql := "%" + strings.ToLower(strings.TrimSpace(query)) + "%"
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	rows, err := a.repo.DB().QueryContext(ctx,
		`SELECT id, COALESCE(name,'') AS name, COALESCE(source,'') AS source, COALESCE(drive_file_id,'') AS drive_file_id
		 FROM media_assets
		 WHERE LOWER(name) LIKE ? AND `+asset.SoftDeleteFilter()+`
		 ORDER BY name
		 LIMIT ?`,
		ql, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]scriptapi.ClipSearchHit, 0, limit)
	for rows.Next() {
		var id, name, source, driveFileID string
		if err := rows.Scan(&id, &name, &source, &driveFileID); err != nil {
			return nil, err
		}
		driveLink := ""
		if driveFileID != "" {
			driveLink = "https://drive.google.com/file/d/" + driveFileID + "/view"
		}
		out = append(out, scriptapi.ClipSearchHit{
			ID:        id,
			Name:      name,
			Source:    source,
			DriveLink: driveLink,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Compile-time assertions: each adapter satisfies its declared
// typed port. Drift here breaks the build, not first-request.
// The ImageGenService assertion for imageGenSvcAdapter lives
// in wire_script_curation.go (that's where the type lives now).
var (
	_ usecase.SemanticSearchPort = (*qdrantSemanticSearchPort)(nil)
	_ scriptapi.ClipSearcher     = (*clipsNameSearchAdapter)(nil)
)
