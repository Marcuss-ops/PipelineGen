// Package search — search_backend_pg.go is the PostgreSQL replacement for the
// legacy SQLite localSearchBackend (search_backend_local.go). MEDIA-SSOT P1-6:
// local/hash/keyword media search MUST read the same PostgreSQL media_assets
// SSOT as semantic search, never a SQLite mirror. The legacy backend stays
// registered only when the media plane is intentionally disabled.
package search

import (
	"context"
	"strings"

	search "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	"go.uber.org/zap"
)

// PostgresLocalSearchPort is the canonical PostgreSQL lexical/hash media read
// surface. *pgmedia.MediaSearcher (the single media read authority)
// implements it.
type PostgresLocalSearchPort interface {
	SearchLocal(ctx context.Context, req pgmedia.LocalMediaSearchRequest) ([]pgmedia.MediaAssetRecord, error)
	FindAllByHash(ctx context.Context, hash string, limit int) ([]pgmedia.MediaAssetRecord, error)
}

type pgLocalSearchBackend struct {
	store PostgresLocalSearchPort
	log   *zap.Logger
}

var _ search.SearchBackend = (*pgLocalSearchBackend)(nil)

func (b *pgLocalSearchBackend) Name() string { return "local" }

func (b *pgLocalSearchBackend) Capabilities() []search.Capability {
	return []search.Capability{
		search.CapVideo,
		search.CapImage,
		search.CapAudio,
		search.CapMusic,
	}
}

// Universe reports SearchCatalog: the backend reads the canonical catalog (no
// live provider call).
func (b *pgLocalSearchBackend) Universe() search.SearchUniverse {
	return search.SearchCatalog
}

func (b *pgLocalSearchBackend) Search(ctx context.Context, q search.Query) ([]search.Candidate, error) {
	if q.Hash != "" {
		return b.searchByHash(ctx, q)
	}
	return b.searchByText(ctx, q)
}

func (b *pgLocalSearchBackend) searchByHash(ctx context.Context, q search.Query) ([]search.Candidate, error) {
	hits, err := b.store.FindAllByHash(ctx, q.Hash, q.Limit)
	if err != nil {
		return nil, err
	}
	out := make([]search.Candidate, 0, len(hits))
	for i := range hits {
		rec := &hits[i]
		out = append(out, search.Candidate{
			AssetID:      rec.ID,
			Source:       rec.Source,
			SourceRef:    rec.ID,
			MediaType:    rec.MediaType,
			Title:        rec.TitleOrName(),
			Name:         rec.Name,
			ThumbnailURL: rec.ThumbnailURL,
			DriveLink:    rec.DriveLink,
			Score:        1.0,
			Hash:         q.Hash,
		})
	}
	return out, nil
}

func (b *pgLocalSearchBackend) searchByText(ctx context.Context, q search.Query) ([]search.Candidate, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = search.DefaultLimit
	}
	if limit > search.MaxLimit {
		limit = search.MaxLimit
	}
	mediaType := ""
	if ids := mediaTypesSingleFromString(q.Filters.MediaType); len(ids) > 0 {
		mediaType = string(ids[0])
	}
	hits, err := b.store.SearchLocal(ctx, pgmedia.LocalMediaSearchRequest{
		Text:     q.Text,
		Source:   sourceOrAll(q.Filters.Source),
		Category: strings.TrimSpace(q.Filters.Category),
		// Taxonomy filters, compiled into the SQL predicate so the catalog leg
		// narrows in-database. Source alone cannot express "stock clips": a
		// YouTube-acquired stock clip is source="youtube" with
		// asset_kind="stock_video".
		AssetKind:           strings.TrimSpace(q.Filters.AssetKind),
		SemanticRole:        strings.TrimSpace(q.Filters.SemanticRole),
		MediaType:           mediaType,
		Limit:               limit,
		ExcludeUnclassified: true,
	})
	if err != nil {
		return nil, err
	}
	out := make([]search.Candidate, 0, len(hits))
	for i := range hits {
		rec := &hits[i]
		sig := search.LocalSignal{
			Title:       rec.Name,
			Tags:        rec.Tags,
			Source:      rec.Source,
			DurationMs:  int(rec.DurationMS),
			MinDuration: q.Filters.DurationMsMin,
		}
		out = append(out, search.Candidate{
			AssetID:      rec.ID,
			Source:       rec.Source,
			SourceRef:    rec.ID,
			Title:        rec.TitleOrName(),
			Name:         rec.Name,
			MediaType:    rec.MediaType,
			ThumbnailURL: rec.ThumbnailURL,
			DriveLink:    rec.DriveLink,
			Score:        search.LocalScore(sig, q),
		})
	}
	return out, nil
}
