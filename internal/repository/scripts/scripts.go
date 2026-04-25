package scripts

import (
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"
)

type ScriptRepository struct {
	db *sql.DB
}

func NewScriptRepository(db *sql.DB) *ScriptRepository {
	return &ScriptRepository{db: db}
}

func (r *ScriptRepository) SaveScript(script *ScriptRecord, sections []ScriptSectionRecord, stockMatches []ScriptStockMatchRecord) (int64, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.Exec(`
		INSERT INTO scripts (topic, duration, language, template, mode, narrative_text, timeline_json, entities_json, metadata_json, full_document, model_used, ollama_base_url, version, parent_script_id, is_deleted)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, script.Topic, script.Duration, script.Language, script.Template, script.Mode, script.NarrativeText, script.TimelineJSON, script.EntitiesJSON, script.MetadataJSON, script.FullDocument, script.ModelUsed, script.OllamaBaseURL, script.Version, script.ParentScriptID, script.IsDeleted)
	if err != nil {
		return 0, fmt.Errorf("failed to insert script: %w", err)
	}

	scriptID, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed to get last insert id: %w", err)
	}

	for _, section := range sections {
		_, err := tx.Exec(`
			INSERT INTO script_sections (script_id, section_type, section_title, content, sort_order)
			VALUES (?, ?, ?, ?, ?)
		`, scriptID, section.SectionType, section.SectionTitle, section.Content, section.SortOrder)
		if err != nil {
			return 0, fmt.Errorf("failed to insert section: %w", err)
		}
	}

	for _, match := range stockMatches {
		_, err := tx.Exec(`
			INSERT INTO script_stock_matches (script_id, segment_index, stock_path, stock_source, score, matched_terms)
			VALUES (?, ?, ?, ?, ?, ?)
		`, scriptID, match.SegmentIndex, match.StockPath, match.StockSource, match.Score, match.MatchedTerms)
		if err != nil {
			return 0, fmt.Errorf("failed to insert stock match: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return scriptID, nil
}

func (r *ScriptRepository) GetScriptByID(id int64) (*ScriptRecord, []ScriptSectionRecord, []ScriptStockMatchRecord, error) {
	var script ScriptRecord
	err := r.db.QueryRow(`
		SELECT id, topic, duration, language, template, mode, narrative_text, timeline_json, entities_json, metadata_json, full_document, model_used, ollama_base_url, created_at, updated_at, version, parent_script_id, is_deleted
		FROM scripts WHERE id = ? AND is_deleted = 0
	`, id).Scan(&script.ID, &script.Topic, &script.Duration, &script.Language, &script.Template, &script.Mode, &script.NarrativeText, &script.TimelineJSON, &script.EntitiesJSON, &script.MetadataJSON, &script.FullDocument, &script.ModelUsed, &script.OllamaBaseURL, &script.CreatedAt, &script.UpdatedAt, &script.Version, &script.ParentScriptID, &script.IsDeleted)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get script: %w", err)
	}

	sections := []ScriptSectionRecord{}
	rows, err := r.db.Query(`SELECT id, script_id, section_type, section_title, content, sort_order FROM script_sections WHERE script_id = ? ORDER BY sort_order`, id)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get sections: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var s ScriptSectionRecord
		if err := rows.Scan(&s.ID, &s.ScriptID, &s.SectionType, &s.SectionTitle, &s.Content, &s.SortOrder); err != nil {
			return nil, nil, nil, fmt.Errorf("failed to scan section: %w", err)
		}
		sections = append(sections, s)
	}

	matches := []ScriptStockMatchRecord{}
	mRows, err := r.db.Query(`SELECT id, script_id, segment_index, stock_path, stock_source, score, matched_terms FROM script_stock_matches WHERE script_id = ?`, id)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get stock matches: %w", err)
	}
	defer mRows.Close()
	for mRows.Next() {
		var m ScriptStockMatchRecord
		if err := mRows.Scan(&m.ID, &m.ScriptID, &m.SegmentIndex, &m.StockPath, &m.StockSource, &m.Score, &m.MatchedTerms); err != nil {
			return nil, nil, nil, fmt.Errorf("failed to scan stock match: %w", err)
		}
		matches = append(matches, m)
	}

	return &script, sections, matches, nil
}

func (r *ScriptRepository) ListScripts(limit, offset int, language, template string) ([]ScriptRecord, int, error) {
	where := "WHERE is_deleted = 0"
	args := []interface{}{}
	if language != "" {
		where += " AND language = ?"
		args = append(args, language)
	}
	if template != "" {
		where += " AND template = ?"
		args = append(args, template)
	}

	var total int
	countArgs := append([]interface{}{}, args...)
	err := r.db.QueryRow("SELECT COUNT(*) FROM scripts "+where, countArgs...).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to count scripts: %w", err)
	}

	args = append(args, limit, offset)
	rows, err := r.db.Query(`
		SELECT id, topic, duration, language, template, mode, narrative_text, timeline_json, entities_json, metadata_json, full_document, model_used, ollama_base_url, created_at, updated_at, version, parent_script_id, is_deleted
		FROM scripts `+where+` ORDER BY created_at DESC LIMIT ? OFFSET ?
	`, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list scripts: %w", err)
	}
	defer rows.Close()

	var scripts []ScriptRecord
	for rows.Next() {
		var s ScriptRecord
		if err := rows.Scan(&s.ID, &s.Topic, &s.Duration, &s.Language, &s.Template, &s.Mode, &s.NarrativeText, &s.TimelineJSON, &s.EntitiesJSON, &s.MetadataJSON, &s.FullDocument, &s.ModelUsed, &s.OllamaBaseURL, &s.CreatedAt, &s.UpdatedAt, &s.Version, &s.ParentScriptID, &s.IsDeleted); err != nil {
			return nil, 0, fmt.Errorf("failed to scan script: %w", err)
		}
		scripts = append(scripts, s)
	}

	return scripts, total, nil
}

func (r *ScriptRepository) SoftDeleteScript(id int64) error {
	_, err := r.db.Exec("UPDATE scripts SET is_deleted = 1, updated_at = datetime('now') WHERE id = ?", id)
	return err
}

func (r *ScriptRepository) CreateNewVersion(parentID int64, script *ScriptRecord, sections []ScriptSectionRecord, stockMatches []ScriptStockMatchRecord) (int64, error) {
	script.ParentScriptID = &parentID
	script.Version = r.getNextVersion(parentID)
	return r.SaveScript(script, sections, stockMatches)
}

func (r *ScriptRepository) getNextVersion(parentID int64) int {
	var maxVersion int
	err := r.db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM scripts WHERE id = ? OR parent_script_id = ?", parentID, parentID).Scan(&maxVersion)
	if err != nil {
		return 1
	}
	return maxVersion + 1
}