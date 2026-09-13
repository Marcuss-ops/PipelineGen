package artlist

// searcher_local_testhelper_test.go — TEST-ONLY replacement for the retired
// production bridge `searcher_sqlite.go`.
//
// MEDIA-SSOT P1-5 (September 2026): the production SQLite searcher bridge
// (SQLiteSearcher / NewSQLiteSearcher / NewDBSearcher) was DELETED, together
// with its only production call site — the `NewSQLiteSearcher(s.assetStore)`
// compatibility fallback in SearchService.buildSearcherChain, which served
// local hits from a retired SQLite media mirror.
//
// The gate scenario tests in this package build a service around an in-memory
// SQLite AssetStore and need a local searcher over that store. They exercise
// the SQLite fixture directly, so the helper stays available to THEM while
// being unreachable from production code (a `_test.go` file is not compiled
// into the package binary). The import-cycle rule is unchanged: this file
// lives in the artlist package and consumes only the AssetStore port.

import (
	"context"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// SQLiteSearcher is the test-only compatibility bridge over an AssetStore.
type SQLiteSearcher struct {
	store AssetStore
}

// NewSQLiteSearcher creates the test-only compatibility bridge.
func NewSQLiteSearcher(store AssetStore) *SQLiteSearcher {
	return &SQLiteSearcher{store: store}
}

// DBSearcher is retained as a source-compatible test alias.
type DBSearcher = SQLiteSearcher

// NewDBSearcher is retained as a source-compatible test constructor alias.
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
