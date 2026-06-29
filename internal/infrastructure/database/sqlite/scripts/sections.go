package scripts

import (
	"context"
	"database/sql"
	"fmt"
)

func (r *ScriptRepository) GetSectionByID(ctx context.Context, id int64) (*scriptSectionRow, error) {
	var s scriptSectionRow
	err := r.db.QueryRowContext(ctx,
		`SELECT id, script_id, section_type, section_title, content, sort_order, word_count, status, voiceover_link
		 FROM script_sections WHERE id = ?`, id,
	).Scan(&s.ID, &s.ScriptID, &s.SectionType, &s.SectionTitle, &s.Content, &s.SortOrder, &s.WordCount, &s.Status, &s.VoiceoverLink)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *ScriptRepository) UpdateSectionContent(ctx context.Context, id int64, content string) error {
	_, err := r.db.ExecContext(ctx, "UPDATE script_sections SET content = ? WHERE id = ?", content, id)
	return err
}

func (r *ScriptRepository) GetAdjacentSections(ctx context.Context, scriptID int64, currentSortOrder int) (prev *scriptSectionRow, next *scriptSectionRow, err error) {
	var p scriptSectionRow
	err = r.db.QueryRowContext(ctx,
		`SELECT id, script_id, section_type, section_title, content, sort_order, word_count, status, voiceover_link
		 FROM script_sections
		 WHERE script_id = ? AND sort_order < ?
		 ORDER BY sort_order DESC LIMIT 1`,
		scriptID, currentSortOrder,
	).Scan(&p.ID, &p.ScriptID, &p.SectionType, &p.SectionTitle, &p.Content, &p.SortOrder, &p.WordCount, &p.Status, &p.VoiceoverLink)
	if err == nil {
		prev = &p
	} else if err != sql.ErrNoRows {
		return nil, nil, err
	}

	var n scriptSectionRow
	err = r.db.QueryRowContext(ctx,
		`SELECT id, script_id, section_type, section_title, content, sort_order, word_count, status, voiceover_link
		 FROM script_sections
		 WHERE script_id = ? AND sort_order > ?
		 ORDER BY sort_order ASC LIMIT 1`,
		scriptID, currentSortOrder,
	).Scan(&n.ID, &n.ScriptID, &n.SectionType, &n.SectionTitle, &n.Content, &n.SortOrder, &n.WordCount, &n.Status, &n.VoiceoverLink)
	if err == nil {
		next = &n
	} else if err != sql.ErrNoRows {
		return nil, nil, err
	}

	return prev, next, nil
}

func (r *ScriptRepository) SaveOutlineSections(ctx context.Context, scriptID int64, sections []scriptOutlineSectionRow) error {
	if len(sections) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction for outline sections: %w", err)
	}
	defer tx.Rollback()

	for _, sec := range sections {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO script_outline_sections (script_id, section_index, title, purpose, target_words, key_points_json, emotional_role)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`, scriptID, sec.SectionIndex, sec.Title, sec.Purpose, sec.TargetWords, sec.KeyPointsJSON, sec.EmotionalRole)
		if err != nil {
			return fmt.Errorf("failed to insert outline section: %w", err)
		}
	}

	return tx.Commit()
}

func (r *ScriptRepository) GetOutlineSections(ctx context.Context, scriptID int64) ([]scriptOutlineSectionRow, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, script_id, section_index, title, purpose, target_words, key_points_json, emotional_role, created_at
		 FROM script_outline_sections WHERE script_id = ? ORDER BY section_index`, scriptID)
	if err != nil {
		return nil, fmt.Errorf("failed to query outline sections: %w", err)
	}
	defer rows.Close()

	var sections []scriptOutlineSectionRow
	for rows.Next() {
		var s scriptOutlineSectionRow
		if err := rows.Scan(&s.ID, &s.ScriptID, &s.SectionIndex, &s.Title, &s.Purpose, &s.TargetWords, &s.KeyPointsJSON, &s.EmotionalRole, &s.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan outline section: %w", err)
		}
		sections = append(sections, s)
	}
	return sections, nil
}
