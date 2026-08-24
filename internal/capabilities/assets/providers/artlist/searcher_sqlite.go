package assets

import (
	"context"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// SQLiteSearcher is a compatibility bridge for existing application-level
// tests and callers that provide only the AssetStore port. Production wiring
// uses platform/sqlite.NewArtlistSQLiteSearcher instead.
type SQLiteSearcher struct {
	store AssetStore
}

// NewSQLiteSearcher creates the compatibility bridge. New composition code
// should construct the infrastructure adapter instead.
func NewSQLiteSearcher(store AssetStore) *SQLiteSearcher {
	return &SQLiteSearcher{store: store}
}

// DBSearcher is retained as a source-compatible alias for existing callers.
type DBSearcher = SQLiteSearcher

// NewDBSearcher is retained as a source-compatible constructor alias.
func NewDBSearcher(store AssetStore) *SQLiteSearcher {
	return NewSQLiteSearcher(store)
}

func (s *SQLiteSearcher) Search(ctx context.Context, req SearchRequest) ([]Candidate, error) {
	if s.store == nil {
		return nil, nil
	}
	term := strings.TrimSpace(req.Term)
	if term == "" {
		return nil, nil
	}
	dbClips, err := s.store.SearchClips(ctx, "artlist", term)
	if err != nil {
		return nil, err
	}
	return candidatesFromAssets(dbClips), nil
}

func candidatesFromAssets(clips []*asset.Asset) []Candidate {
	if len(clips) == 0 {
		return nil
	}
	candidates := make([]Candidate, 0, len(clips))
	for _, clip := range clips {
		if clip == nil {
			continue
		}
		candidates = append(candidates, Candidate{
			Provider:     "artlist",
			ExternalID:   clip.ID,
			ID:           clip.ID,
			Title:        clip.Name,
			Description:  clip.GetMetadataString("description"),
			Creator:      clip.GetMetadataString("creator"),
			PageURL:      clip.ClipPageURL,
			PreviewURL:   firstNonEmpty(clip.GetMetadataString("preview_url"), clip.ClipPageURL),
			ThumbnailURL: clip.ThumbnailURL,
			SourceRef:    firstNonEmpty(clip.SourceURL, clip.ClipPageURL),
			SourceName:   "database",
			MediaType:    clip.MediaType,
			Duration:     clip.Duration,
			DurationMs:   clip.Duration.Milliseconds(),
			Keywords:     clip.Tags,
			Categories:   stringSliceFromMetadata(clip.Metadata, "provider_categories"),
			RawMetadata:  cloneMetadata(clip.Metadata),
		})
	}
	return candidates
}
