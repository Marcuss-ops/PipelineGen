package scripts

import (
	"context"
	"fmt"
)

func (r *ScriptRepository) SaveResearchSources(ctx context.Context, scriptID int64, sources []ScriptResearchSource) error {
	if len(sources) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction for research sources: %w", err)
	}
	defer tx.Rollback()

	for _, src := range sources {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO script_research_sources (script_id, query, url, title, snippet, source_type, used_in_sections, relevance_score)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(script_id, url, query) WHERE url != '' AND url IS NOT NULL DO UPDATE SET
				used_in_sections = excluded.used_in_sections,
				relevance_score   = MAX(script_research_sources.relevance_score, excluded.relevance_score)
		`, scriptID, src.Query, src.URL, src.Title, src.Snippet, src.SourceType, src.UsedInSections, src.RelevanceScore)
		if err != nil {
			return fmt.Errorf("failed to insert research source: %w", err)
		}
	}

	return tx.Commit()
}

func (r *ScriptRepository) GetResearchSources(ctx context.Context, scriptID int64) ([]ScriptResearchSource, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, script_id, query, url, title, snippet, source_type, used_in_sections, relevance_score, created_at
		 FROM script_research_sources WHERE script_id = ? ORDER BY id`, scriptID)
	if err != nil {
		return nil, fmt.Errorf("failed to query research sources: %w", err)
	}
	defer rows.Close()

	var sources []ScriptResearchSource
	for rows.Next() {
		var s ScriptResearchSource
		if err := rows.Scan(&s.ID, &s.ScriptID, &s.Query, &s.URL, &s.Title, &s.Snippet, &s.SourceType, &s.UsedInSections, &s.RelevanceScore, &s.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan research source: %w", err)
		}
		sources = append(sources, s)
	}
	return sources, nil
}
