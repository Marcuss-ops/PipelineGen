package media

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	appsearch "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
)

// Compile-time cutover pin: the PostgreSQL MediaSearcher is the ONE media
// read adapter. It owns both pgvector retrieval and media_assets hydration.
var _ appsearch.MediaReadRepository = (*MediaSearcher)(nil)

// GetMany implements the canonical MediaReadRepository on the same concrete
// adapter that implements VectorStorePort. This is intentionally not a second
// repository: BuildSearchBackends requires one object to satisfy both ports,
// making pgvector -> PostgreSQL media_assets the only semantic media read path.
func (s *MediaSearcher) GetMany(
	ctx context.Context,
	actor appsearch.Actor,
	assetIDs []string,
) ([]appsearch.MediaAsset, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("postgres media searcher: hydration not wired")
	}

	ids := dedupeHydrationIDs(assetIDs)
	if len(ids) == 0 {
		return []appsearch.MediaAsset{}, nil
	}

	out := make([]appsearch.MediaAsset, 0, len(ids))
	for _, id := range ids {
		row, err := s.fetchAssetForWorkspace(ctx, id, strings.TrimSpace(actor.WorkspaceID))
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return nil, fmt.Errorf("postgres media searcher: hydrate asset %q: %w", id, err)
		}
		if !isSearchableMediaLifecycle(row.LifecycleState) {
			continue
		}
		out = append(out, appsearch.MediaAsset{
			ID:             row.ID,
			Name:           row.Name,
			Source:         row.Source,
			MediaType:      row.MediaType,
			Category:       row.Category,
			Tags:           decodeTagsJSON(row.Tags),
			Language:       row.Language,
			DurationMs:     int(row.DurationMs),
			SearchText:     row.SearchText,
			LifecycleState: row.LifecycleState,
		})
	}
	return out, nil
}

// fetchAssetForWorkspace repeats the workspace predicate when the verified
// search actor carries one. Empty workspace is reserved for admin/system
// searches: the vector leg has already enforced IsSystem there, while normal
// user searches always carry the authenticated workspace.
func (s *MediaSearcher) fetchAssetForWorkspace(ctx context.Context, assetID, workspaceID string) (*assetRow, error) {
	query := `
		SELECT id, name, source, media_type, category, language,
		       tags, search_text, duration_ms, lifecycle_state,
		       youtube_video_id, youtube_url, start_time, end_time, style
		FROM media_assets
		WHERE id = $1
	`
	args := []any{assetID}
	if workspaceID != "" {
		query += " AND workspace_id = $2"
		args = append(args, workspaceID)
	}

	row := s.db.QueryRowContext(ctx, query, args...)
	var a assetRow
	if err := row.Scan(&a.ID, &a.Name, &a.Source, &a.MediaType, &a.Category, &a.Language,
		&a.Tags, &a.SearchText, &a.DurationMs, &a.LifecycleState,
		&a.YouTubeVideoID, &a.YouTubeURL, &a.StartTime, &a.EndTime, &a.Style); err != nil {
		return nil, err
	}
	return &a, nil
}

func dedupeHydrationIDs(assetIDs []string) []string {
	seen := make(map[string]struct{}, len(assetIDs))
	out := make([]string, 0, len(assetIDs))
	for _, raw := range assetIDs {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func isSearchableMediaLifecycle(state string) bool {
	for _, allowed := range appsearch.SearchableLifecycleStates {
		if state == allowed {
			return true
		}
	}
	return false
}
