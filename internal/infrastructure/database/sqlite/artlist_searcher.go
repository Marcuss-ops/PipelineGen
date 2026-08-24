// Package sqlite contains concrete SQLite adapters for application ports.
package sqlite

import (
	"context"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// artlistSearchStore is the narrow repository surface needed by the adapter.
// The concrete SQLite repository satisfies this interface through its
// SearchClips method; SQL ownership remains inside the assets infrastructure
// package.
type artlistSearchStore interface {
	SearchClips(ctx context.Context, source, term string) ([]*asset.Asset, error)
}

// ArtlistSQLiteSearcher is the canonical infrastructure adapter from the
// SQLite Artlist catalog to the application Searcher port.
type ArtlistSQLiteSearcher struct {
	store artlistSearchStore
}

// NewArtlistSQLiteSearcher constructs the canonical local Artlist search
// adapter. The repository is composed by the application root; this adapter
// owns no provider policy and does not construct a database connection.
func NewArtlistSQLiteSearcher(store artlistSearchStore) artlist.Searcher {
	return &ArtlistSQLiteSearcher{store: store}
}

var _ artlist.Searcher = (*ArtlistSQLiteSearcher)(nil)

func (s *ArtlistSQLiteSearcher) Search(ctx context.Context, req artlist.SearchRequest) ([]artlist.Candidate, error) {
	if s == nil || s.store == nil {
		return nil, nil
	}
	term := strings.TrimSpace(req.Term)
	if term == "" {
		return nil, nil
	}
	clips, err := s.store.SearchClips(ctx, "artlist", term)
	if err != nil {
		return nil, err
	}
	return artlistCandidatesFromAssets(clips), nil
}

func artlistCandidatesFromAssets(clips []*asset.Asset) []artlist.Candidate {
	if len(clips) == 0 {
		return nil
	}
	candidates := make([]artlist.Candidate, 0, len(clips))
	for _, clip := range clips {
		if clip == nil {
			continue
		}
		candidates = append(candidates, artlist.Candidate{
			Provider:     "artlist",
			ExternalID:   clip.ID,
			ID:           clip.ID,
			Title:        clip.Name,
			Description:  clip.GetMetadataString("description"),
			Creator:      clip.GetMetadataString("creator"),
			PageURL:      clip.ClipPageURL,
			PreviewURL:   firstNonEmptyString(clip.GetMetadataString("preview_url"), clip.ClipPageURL),
			ThumbnailURL: clip.ThumbnailURL,
			SourceRef:    firstNonEmptyString(clip.SourceURL, clip.ClipPageURL),
			SourceName:   "database",
			MediaType:    clip.MediaType,
			Duration:     clip.Duration,
			DurationMs:   clip.Duration.Milliseconds(),
			Keywords:     clip.Tags,
			Categories:   artlistCategories(clip),
			RawMetadata:  cloneAssetMetadata(clip),
		})
	}
	return candidates
}

func artlistCategories(clip *asset.Asset) []string {
	return stringSliceFromMetadata(clip.Metadata, "provider_categories")
}

func stringSliceFromMetadata(metadata asset.Metadata, key string) []string {
	value, ok := metadata[key]
	if !ok {
		return nil
	}
	switch values := value.(type) {
	case []string:
		return append([]string(nil), values...)
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			if text, ok := value.(string); ok {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func cloneAssetMetadata(clip *asset.Asset) map[string]any {
	if clip.Metadata == nil {
		return nil
	}
	out := make(map[string]any, len(clip.Metadata))
	for key, value := range clip.Metadata {
		out[key] = value
	}
	return out
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
