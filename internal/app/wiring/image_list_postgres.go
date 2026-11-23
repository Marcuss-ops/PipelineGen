package wiring

import (
	"context"
	"database/sql"
	"strings"

	imagesrouting "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images"
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

// postgresImageListRepository is the canonical media-SSOT source behind
// ExistingImages.
//
// VidRush materializes every acquired image into PostgreSQL media_assets and
// records its subject identity there. Reading the reuse lookup from the
// operational SQLite mirror made every lookup a guaranteed miss: the SQLite
// media_assets table has no rows for those images, so a warm run re-downloaded
// and re-uploaded bytes that were already published. The SQLite repository
// stays in the chain only for legacy rows written before the media cutover.
//
// The SQL itself belongs to internal/platform/postgres/media (the single media
// read authority); this adapter only maps its rows onto the routing contract.
type postgresImageListRepository struct {
	reader *pgmedia.ImageReader
}

var _ imagesrouting.ImageListRepository = (*postgresImageListRepository)(nil)
var _ imagesrouting.LocalImageAssetHydrator = (*postgresImageListRepository)(nil)

func newPostgresImageListRepository(db *sql.DB) *postgresImageListRepository {
	if db == nil {
		return nil
	}
	return &postgresImageListRepository{reader: pgmedia.NewImageReader(db)}
}

func (r *postgresImageListRepository) ListImages(ctx context.Context, filter imagesrouting.ImageFilter) ([]imagesrouting.ImageSearchResult, error) {
	if r == nil || r.reader == nil {
		return nil, nil
	}
	subject := strings.TrimSpace(filter.SubjectID)
	if subject == "" {
		return nil, nil
	}
	rows, err := r.reader.ListImagesBySubject(ctx, subject, filter.Limit)
	if err != nil {
		return nil, err
	}
	return mapSubjectImageRows(rows, filter), nil
}

func (r *postgresImageListRepository) ListImagesByAssetIDs(ctx context.Context, assetIDs []string) ([]imagesrouting.ImageSearchResult, error) {
	if r == nil || r.reader == nil || len(assetIDs) == 0 {
		return nil, nil
	}
	rows, err := r.reader.ListImagesByAssetIDs(ctx, assetIDs)
	if err != nil {
		return nil, err
	}
	return mapSubjectImageRows(rows, imagesrouting.ImageFilter{}), nil
}

func mapSubjectImageRows(rows []pgmedia.SubjectImageRow, filter imagesrouting.ImageFilter) []imagesrouting.ImageSearchResult {
	out := make([]imagesrouting.ImageSearchResult, 0, len(rows))
	for _, row := range rows {
		if !imageRowMatchesFilter(row.Origin, row.Provider, filter) {
			continue
		}
		out = append(out, imagesrouting.ImageSearchResult{
			AssetID: row.AssetID, Origin: row.Origin, Provider: row.Provider,
			Name: row.Name, PreviewURL: row.Location.PreviewURL, DriveLink: row.Location.DriveLink,
			ContentSHA256: row.ContentSHA256, SourcePageURL: row.SourcePageURL,
			Width: row.Width, Height: row.Height, Score: 1,
		})
	}
	return out
}

func imageRowMatchesFilter(origin, provider string, filter imagesrouting.ImageFilter) bool {
	// An empty origin is a legacy row written before the provenance columns
	// were populated; the column default and the only writer of image rows
	// mean "retrieved". Treating it as "not retrieved" hid every such row from
	// the reuse lookup.
	if strings.TrimSpace(origin) == "" {
		origin = string(detail.ImageOriginRetrieved)
	}
	if len(filter.Origins) > 0 {
		matched := false
		for _, want := range filter.Origins {
			if strings.EqualFold(string(want), origin) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	if len(filter.Providers) > 0 {
		for _, want := range filter.Providers {
			if strings.EqualFold(string(want), provider) {
				return true
			}
		}
		return false
	}
	return true
}

// compositeImageListRepository queries its members in order and merges the
// rows, deduplicating on asset identity. PostgreSQL is first in the chain, so
// the durable SSOT always wins over a legacy mirror row.
type compositeImageListRepository struct {
	repos []imagesrouting.ImageListRepository
}

var _ imagesrouting.ImageListRepository = (*compositeImageListRepository)(nil)
var _ imagesrouting.LocalImageAssetHydrator = (*compositeImageListRepository)(nil)

func (r *compositeImageListRepository) ListImages(ctx context.Context, filter imagesrouting.ImageFilter) ([]imagesrouting.ImageSearchResult, error) {
	if r == nil {
		return nil, nil
	}
	var out []imagesrouting.ImageSearchResult
	seen := make(map[string]struct{})
	for _, repo := range r.repos {
		if repo == nil {
			continue
		}
		rows, err := repo.ListImages(ctx, filter)
		if err != nil {
			if len(out) > 0 {
				// A later repository failing must not discard the rows the
				// canonical one already returned.
				break
			}
			return nil, err
		}
		out = appendUniqueImageResults(out, seen, rows, filter.Limit)
		if filter.Limit > 0 && len(out) >= filter.Limit {
			break
		}
	}
	return out, nil
}

func (r *compositeImageListRepository) ListImagesByAssetIDs(ctx context.Context, assetIDs []string) ([]imagesrouting.ImageSearchResult, error) {
	if r == nil {
		return nil, nil
	}
	var out []imagesrouting.ImageSearchResult
	seen := make(map[string]struct{})
	for _, repo := range r.repos {
		hydrator, ok := repo.(imagesrouting.LocalImageAssetHydrator)
		if !ok || hydrator == nil {
			continue
		}
		rows, err := hydrator.ListImagesByAssetIDs(ctx, assetIDs)
		if err != nil {
			if len(out) > 0 {
				break
			}
			return nil, err
		}
		out = appendUniqueImageResults(out, seen, rows, 0)
	}
	return out, nil
}

func appendUniqueImageResults(out []imagesrouting.ImageSearchResult, seen map[string]struct{}, rows []imagesrouting.ImageSearchResult, limit int) []imagesrouting.ImageSearchResult {
	for _, row := range rows {
		key := strings.TrimSpace(row.AssetID)
		if key == "" {
			key = strings.TrimSpace(row.DriveLink)
		}
		if key == "" {
			continue
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, row)
		if limit > 0 && len(out) >= limit {
			return out
		}
	}
	return out
}
