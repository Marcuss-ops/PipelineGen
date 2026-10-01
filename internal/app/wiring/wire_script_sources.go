// Package wiring contains composition-root adapters for the ScriptFlow media
// read surfaces. PostgreSQL + pgvector is the only media catalog authority;
// no Qdrant or SQLite media mirror participates in these adapters.
package wiring

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	appsearch "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/maps"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptapi "github.com/Marcuss-ops/PipelineGen/internal/capabilities/script"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	usecase "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	coreasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	"go.uber.org/zap"
)

// postgresSemanticSearchPort bridges the script SourceSearch contract to the
// canonical pgvector MediaSearcher. The searcher already hydrates metadata
// from media_assets in the same PostgreSQL SSOT, so there is no second
// Qdrant→SQLite validation/hydration leg.
type postgresSemanticSearchPort struct {
	searcher appsearch.VectorStorePort
	embedder coreasset.Embedder
	log      *zap.Logger
}

func (p *postgresSemanticSearchPort) SearchByText(ctx context.Context, query string, limit int, _ string) ([]usecase.SemanticSearchResult, error) {
	if p == nil || p.searcher == nil || p.embedder == nil {
		return nil, fmt.Errorf("postgres semantic search: media search dependencies are unavailable")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return []usecase.SemanticSearchResult{}, nil
	}
	if limit <= 0 {
		limit = 10
	}

	embedding, err := p.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("postgres semantic search: embed query: %w", err)
	}
	if len(embedding.Vector) == 0 {
		return nil, fmt.Errorf("postgres semantic search: embed query returned an empty vector")
	}
	searchLimit := limit * 3
	if searchLimit < 30 {
		searchLimit = 30
	}
	results, err := p.searcher.HybridSearch(ctx, appsearch.HybridSearchRequest{
		DenseVector:     embedding.Vector,
		DenseVectorName: appsearch.ChannelText,
		SparseText:      query,
		Limit:           searchLimit,
		MinScore:        0,
		LifecycleState:  append([]string(nil), appsearch.SearchableLifecycleStates...),
		IsSystem:        true,
	})
	if err != nil {
		return nil, fmt.Errorf("postgres semantic search: %w", err)
	}

	out := make([]usecase.SemanticSearchResult, 0, len(results))
	for _, result := range results {
		id := strings.TrimSpace(result.AssetID)
		mediaType := strings.ToLower(strings.TrimSpace(result.MediaType))
		if id == "" || (mediaType != "video" && mediaType != "clip") {
			continue
		}
		out = append(out, usecase.SemanticSearchResult{
			ClipID:              id,
			Name:                result.Name,
			Score:               result.Score,
			Transcript:          result.SearchText,
			VisualSummary:       result.SearchText,
			MediaType:           result.MediaType,
			AvailableByIngest:   true,
			AnchorCoverageRatio: 1.0,
		})
	}
	if p.log != nil {
		p.log.Info("postgres semantic search",
			zap.Int("postgres_results", len(results)),
			zap.Int("accepted_clips", len(out)),
		)
	}
	return out, nil
}

// postgresAssetSearchPort implements the canonical scripts AssetSearchPort on
// the same pgvector MediaSearcher used by SourceSearch. It intentionally
// rejects Qdrant-only filter shapes instead of silently ignoring them.
type postgresAssetSearchPort struct {
	searcher appsearch.VectorStorePort
	embedder coreasset.Embedder
}

func (p *postgresAssetSearchPort) SearchAssets(ctx context.Context, q scriptports.AssetSearchQuery) ([]scriptports.AssetSearchHit, error) {
	if p == nil || p.searcher == nil || p.embedder == nil {
		return nil, fmt.Errorf("postgres asset search: media search dependencies are unavailable")
	}
	query := strings.TrimSpace(q.Query)
	if query == "" {
		return []scriptports.AssetSearchHit{}, nil
	}
	if strings.TrimSpace(q.FolderNormalizedGroup) != "" || len(q.ExcludeRightsStatuses) > 0 || len(q.ExcludeReviewStatuses) > 0 {
		return nil, fmt.Errorf("postgres asset search: legacy Qdrant-only folder/rights filters are not supported on the canonical media port")
	}

	source := strings.TrimSpace(q.Source)
	limit := q.Limit
	if limit <= 0 {
		limit = 20
		if strings.EqualFold(source, "stock") {
			limit = 5
		}
	}
	minScore := q.MinScore
	if minScore <= 0 {
		minScore = 0.5
		if strings.EqualFold(source, "stock") {
			minScore = 0.3
		}
	}
	mediaType := strings.TrimSpace(q.MediaType)
	if mediaType == "" {
		mediaType = "video"
	}
	lifecycleStates := append([]string(nil), appsearch.SearchableLifecycleStates...)
	if q.RequireActiveLifecycle {
		lifecycleStates = []string{"ACTIVE"}
	}

	embedding, err := p.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("postgres asset search: embed query: %w", err)
	}
	if len(embedding.Vector) == 0 {
		return nil, fmt.Errorf("postgres asset search: embed query returned an empty vector")
	}
	results, err := p.searcher.Search(ctx, appsearch.VectorSearchRequest{
		QueryVector:    embedding.Vector,
		VectorName:     appsearch.ChannelText,
		Limit:          limit,
		MinScore:       minScore,
		Source:         source,
		Category:       strings.TrimSpace(q.Category),
		MediaType:      mediaType,
		LifecycleState: lifecycleStates,
		WorkspaceID:    strings.TrimSpace(q.WorkspaceID),
		IsSystem:       q.IsSystem,
	})
	if err != nil {
		return nil, fmt.Errorf("postgres asset search: %w", err)
	}

	out := make([]scriptports.AssetSearchHit, 0, len(results))
	for _, result := range results {
		if strings.TrimSpace(result.AssetID) == "" {
			continue
		}
		out = append(out, scriptports.AssetSearchHit{
			AssetID: result.AssetID,
			Name:    result.Name,
			Score:   result.Score,
			Source:  result.Source,
		})
	}
	return out, nil
}

// clipsNameSearchAdapter bridges the lightweight script clip-discovery
// endpoint to MediaSearcher.SearchClipsByName. PostgreSQL owns both lookup and
// media identity; SQLite media_assets is deliberately absent from this path.
type clipsNameSearchAdapter struct {
	searcher *pgmedia.MediaSearcher
}

func newClipsNameSearchAdapter(db *sql.DB) *clipsNameSearchAdapter {
	if db == nil {
		return nil
	}
	return &clipsNameSearchAdapter{searcher: pgmedia.NewMediaSearcher(db)}
}

func (a *clipsNameSearchAdapter) SearchByName(ctx context.Context, query string, limit int) ([]scriptapi.ClipSearchHit, error) {
	if a == nil || a.searcher == nil {
		return nil, nil
	}
	hits, err := a.searcher.SearchClipsByName(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	out := make([]scriptapi.ClipSearchHit, 0, len(hits))
	for _, hit := range hits {
		driveLink := ""
		if hit.DriveFileID != "" {
			driveLink = "https://drive.google.com/file/d/" + hit.DriveFileID + "/view"
		}
		out = append(out, scriptapi.ClipSearchHit{
			ID:        hit.AssetID,
			Name:      hit.Name,
			Source:    hit.Source,
			DriveLink: driveLink,
		})
	}
	return out, nil
}

// catalogSearchLimit bounds one SourceCatalog lexical query. The retired
// SQLite keyword lookup capped its result set at 50, so this preserves the
// bound rather than inventing a new one.
const catalogSearchLimit = 50

// postgresCatalogPort implements search.LocalCatalogPort on the PostgreSQL
// media SSOT.
//
// WHY THIS EXISTS (P2-9 read migration). SourceCatalog used to resolve through
// *catalog.Repository, whose SearchAll ran keyword SQL against the operational
// SQLite media_assets + clip_search_terms mirror. PostgreSQL + pgvector is the
// sole durable media authority and the canonical writer no longer populates
// that mirror, so the read could only grade a stale catalog. SearchLocal answers
// the same question on the SSOT with the same keyword semantics the retired
// clip_search_terms AND-lookup provided (name / search_text / search_terms), so
// this is a port swap, not a rewrite.
type postgresCatalogPort struct {
	searcher *pgmedia.MediaSearcher
}

func (p *postgresCatalogPort) SearchAll(ctx context.Context, query string) ([]appsearch.CatalogSearchResult, error) {
	if p == nil || p.searcher == nil {
		return nil, fmt.Errorf("postgres catalog search: media search dependencies are unavailable")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return []appsearch.CatalogSearchResult{}, nil
	}
	records, err := p.searcher.SearchLocal(ctx, pgmedia.LocalMediaSearchRequest{
		Text:  query,
		Limit: catalogSearchLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("postgres catalog search: %w", err)
	}
	out := make([]appsearch.CatalogSearchResult, 0, len(records))
	for _, rec := range records {
		if strings.TrimSpace(rec.ID) == "" {
			continue
		}
		out = append(out, appsearch.CatalogSearchResult{
			ID:   rec.ID,
			Name: rec.Name,
			Type: rec.MediaType,
		})
	}
	return out, nil
}

var (
	_ usecase.SemanticSearchPort  = (*postgresSemanticSearchPort)(nil)
	_ scriptports.AssetSearchPort = (*postgresAssetSearchPort)(nil)
	_ scriptapi.ClipSearcher      = (*clipsNameSearchAdapter)(nil)
	_ appsearch.LocalCatalogPort  = (*postgresCatalogPort)(nil)
)

// mapPlateMediaType is the only raster type the manifest certifies, so the
// content address the planner publishes is always a PNG.
const mapPlateMediaType = "image/png"

// mapPlateResolver adapts the certified manifest onto the planner's port. A
// nil manifest resolves nothing, which is the fail-closed default: no map
// item is emitted rather than a fabricated one.
type mapPlateResolver struct {
	manifest *maps.Manifest
}

// ResolvePlate returns the most detailed certified plate covering the point.
// ok is false when no plate covers it, and the planner then emits no map.
func (r mapPlateResolver) ResolvePlate(latitude, longitude float64) (capabilityoverlay.MapPlate, bool) {
	if r.manifest == nil {
		return capabilityoverlay.MapPlate{}, false
	}
	plate, ok := r.manifest.Resolve(latitude, longitude)
	if !ok {
		return capabilityoverlay.MapPlate{}, false
	}
	return capabilityoverlay.MapPlate{
		ID:          plate.ID,
		License:     plate.License,
		Attribution: plate.Attribution,
		Center: capabilityoverlay.MapCenter{
			Latitude:  plate.Center.Latitude,
			Longitude: plate.Center.Longitude,
		},
		Zoom:   plate.Zoom,
		Width:  plate.Width,
		Height: plate.Height,
		Window: plate.Window(),
		// URL stays empty on purpose: the only way the raster reaches the
		// renderer is through the content-addressed prefetch of the local
		// plate, so no deployment can turn a map into a network fetch by
		// filling in a URL.
		Asset: capabilityoverlay.NewOverlayAssetRef(
			asset.New(plate.ID, plate.SHA256(), mapPlateMediaType, 0),
			"",
			plate.Path(),
		),
	}, true
}

// ResolveFlyover chooses complete offline raster coverage for the route. The
// coarsest certified window is the base plate; subsequent zooms become ordered
// LOD assets only when their provenance matches the base exactly.
func (r mapPlateResolver) ResolveFlyover(from, to capabilityoverlay.MapCenter, canvasWidth, canvasHeight int) (capabilityoverlay.MapPlate, bool) {
	if canvasWidth <= 0 || canvasHeight <= 0 {
		return capabilityoverlay.MapPlate{}, false
	}
	if r.manifest == nil {
		return capabilityoverlay.MapPlate{}, false
	}
	plates, ok := r.manifest.ResolveFlyover(from.Latitude, from.Longitude, to.Latitude, to.Longitude, canvasWidth, canvasHeight)
	if !ok || len(plates) < 2 {
		return capabilityoverlay.MapPlate{}, false
	}
	base := plates[0]
	resolved := mapPlateFromManifest(base)
	for _, plate := range plates[1:] {
		if plate.License != base.License || plate.Attribution != base.Attribution {
			return capabilityoverlay.MapPlate{}, false
		}
		lod := mapPlateFromManifest(plate)
		lod.LODs = nil
		resolved.LODs = append(resolved.LODs, lod)
	}
	return resolved, true
}

func mapPlateFromManifest(plate maps.Plate) capabilityoverlay.MapPlate {
	return capabilityoverlay.MapPlate{
		ID: plate.ID, License: plate.License, Attribution: plate.Attribution,
		Center: capabilityoverlay.MapCenter{Latitude: plate.Center.Latitude, Longitude: plate.Center.Longitude},
		Zoom:   plate.Zoom, Width: plate.Width, Height: plate.Height, Window: plate.Window(),
		Asset: capabilityoverlay.NewOverlayAssetRef(
			asset.New(plate.ID, plate.SHA256(), mapPlateMediaType, 0), "", plate.Path(),
		),
	}
}

// mapPlateWiringTarget is the port wireMapPlates needs: the runner's map-source
// setter and nothing else. Narrowing it keeps the wiring testable without a
// fully populated Runner.
type mapPlateWiringTarget interface {
	SetMapPlateResolver(capabilityoverlay.PlateResolver)
}

// wireMapPlates loads the operator basemap manifest when one is configured and
// wires it as the runner's ONLY map source. An unconfigured path is a valid
// deployment (no maps at all); a configured path that cannot be certified is a
// hard error, because silently running without the plates the operator
// declared would drop every map from the run without saying so.
func wireMapPlates(runner mapPlateWiringTarget, manifestPath string, log *zap.Logger) error {
	path := strings.TrimSpace(manifestPath)
	if path == "" {
		log.Info("map plates not wired: no operator basemap manifest configured (no map overlays will be emitted)")
		return nil
	}
	manifest, err := maps.LoadManifest(path)
	if err != nil {
		return fmt.Errorf("wire map plates: %w", err)
	}
	plates := manifest.Plates()
	runner.SetMapPlateResolver(mapPlateResolver{manifest: manifest})
	log.Info("operator basemap plates wired",
		zap.String("manifest", path),
		zap.Int("plates", len(plates)),
	)
	return nil
}
