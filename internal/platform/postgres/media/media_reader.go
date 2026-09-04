package media

import (
	"context"
	"fmt"
	"strings"

	appsearch "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
)

// MediaSearcher owns BOTH halves of the canonical media read path:
// pgvector retrieval and media_assets hydration. Keeping the two ports on the
// same adapter makes it impossible to compose PostgreSQL retrieval with a
// second database for metadata hydration.
var _ appsearch.MediaReadRepository = (*MediaSearcher)(nil)

// GetMany hydrates already-selected media asset IDs from the PostgreSQL media
// SSOT. The vector query is the primary workspace boundary; when a concrete
// workspace is present we repeat that predicate here as defence in depth.
// System/admin searches may intentionally carry an empty WorkspaceID after
// retrieval, so an empty workspace does not widen the query: the explicit ID
// set remains the only rows eligible for hydration.
func (s *MediaSearcher) GetMany(ctx context.Context, actor appsearch.Actor, assetIDs []string) ([]appsearch.MediaAsset, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("postgres media reader: not wired")
	}
	if len(assetIDs) == 0 {
		return []appsearch.MediaAsset{}, nil
	}

	args := make([]any, 0, len(assetIDs)+len(appsearch.SearchableLifecycleStates)+1)
	idPlaceholders := make([]string, 0, len(assetIDs))
	for _, id := range assetIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		args = append(args, id)
		idPlaceholders = append(idPlaceholders, fmt.Sprintf("$%d", len(args)))
	}
	if len(idPlaceholders) == 0 {
		return []appsearch.MediaAsset{}, nil
	}

	where := []string{
		fmt.Sprintf("id IN (%s)", strings.Join(idPlaceholders, ", ")),
		"deleted_at = ''",
	}
	workspaceID := strings.TrimSpace(actor.WorkspaceID)
	if workspaceID == "default" && !actor.IsAdmin && !actor.IsSystem {
		return nil, fmt.Errorf(`postgres media reader: WorkspaceID is the reserved "default" sentinel`)
	}
	if workspaceID != "" && !actor.IsAdmin && !actor.IsSystem {
		args = append(args, workspaceID)
		where = append(where, fmt.Sprintf("workspace_id = $%d", len(args)))
	}

	lifecyclePlaceholders := make([]string, 0, len(appsearch.SearchableLifecycleStates))
	for _, state := range appsearch.SearchableLifecycleStates {
		args = append(args, state)
		lifecyclePlaceholders = append(lifecyclePlaceholders, fmt.Sprintf("$%d", len(args)))
	}
	where = append(where, fmt.Sprintf("lifecycle_state IN (%s)", strings.Join(lifecyclePlaceholders, ", ")))

	query := `
		SELECT id, name, source, media_type, category, tags, language,
		       duration_ms, width, height, search_text, drive_link, lifecycle_state
		FROM media_assets
		WHERE ` + strings.Join(where, "\n\t\t  AND ")

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres media reader: query: %w", err)
	}
	defer rows.Close()

	byID := make(map[string]appsearch.MediaAsset, len(assetIDs))
	for rows.Next() {
		var (
			a        appsearch.MediaAsset
			tagsJSON string
			duration int64
		)
		if err := rows.Scan(
			&a.ID,
			&a.Name,
			&a.Source,
			&a.MediaType,
			&a.Category,
			&tagsJSON,
			&a.Language,
			&duration,
			&a.Width,
			&a.Height,
			&a.SearchText,
			&a.DriveLink,
			&a.LifecycleState,
		); err != nil {
			return nil, fmt.Errorf("postgres media reader: scan: %w", err)
		}
		a.DurationMs = int(duration)
		a.Tags = decodeTagsJSON(tagsJSON)
		byID[a.ID] = a
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres media reader: iterate: %w", err)
	}

	// PostgreSQL IN queries do not guarantee input order. Preserve the vector
	// ranking order so hydration never perturbs deterministic retrieval.
	out := make([]appsearch.MediaAsset, 0, len(byID))
	for _, id := range assetIDs {
		if a, ok := byID[strings.TrimSpace(id)]; ok {
			out = append(out, a)
		}
	}
	return out, nil
}
