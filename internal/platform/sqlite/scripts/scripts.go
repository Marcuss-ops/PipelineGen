package scripts

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
)

type ScriptRepository struct {
	db *sql.DB
}

func NewScriptRepository(db *sql.DB) *ScriptRepository {
	return &ScriptRepository{db: db}
}

// scriptsSelectColumns is the canonical column projection used by every
// SELECT in this file (GetScriptByID, FindByTopic, ListScripts,
// FindByIdempotencyKey). Centralising the projection + scan target list in
// one place guarantees that all 4 queries stay in lock-step when a new
// column lands in PR 6 / 7 / 8. The Scan target order must match this
// projection exactly — see scanScriptRecord below.
//
// PR 6 (June 2026) — added `idempotency_key` + `specscene` to the
// projection. See migrations/sqlite/100_add_idempotency_key_and_specscene_columns.sql.
const scriptsSelectColumns = `id, topic, title, duration, language, template, mode, tone, target_words, final_word_count, status, narrative_text, timeline_json, entities_json, metadata_json, full_document, model_used, ollama_base_url, idempotency_key, specscene, created_at, updated_at, version, parent_script_id, is_deleted`

// scanScriptRecord performs the canonical scan over a SELECT row matching
// scriptsSelectColumns. The argument type is satisfied by *sql.Row (single
// row from QueryRow) and *sql.Rows (iter-row from Query loop) — both expose
// the `Scan(...any)` method. Centralising the scan guarantees structural
// alignment with the column projection.
func scanScriptRecord(s interface{ Scan(...any) error }, dst *ScriptRecord) error {
	return s.Scan(
		&dst.ID, &dst.Topic, &dst.Title, &dst.Duration, &dst.Language,
		&dst.Template, &dst.Mode, &dst.Tone, &dst.TargetWords, &dst.FinalWordCount,
		&dst.Status, &dst.NarrativeText, &dst.TimelineJSON, &dst.EntitiesJSON,
		&dst.MetadataJSON, &dst.FullDocument, &dst.ModelUsed, &dst.OllamaBaseURL,
		&dst.IdempotencyKey, &dst.SpecScene,
		&dst.CreatedAt, &dst.UpdatedAt, &dst.Version, &dst.ParentScriptID,
		&dst.IsDeleted,
	)
}

func (r *ScriptRepository) SaveScript(ctx context.Context, script *ScriptRecord, sections []scriptSectionRow, stockMatches []scriptStockMatchRow) (int64, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	version := script.Version
	if version == 0 {
		version = 1
	}

	result, err := tx.ExecContext(ctx, `
		INSERT INTO scripts (topic, title, duration, language, template, mode, tone, target_words, final_word_count, status, narrative_text, timeline_json, entities_json, metadata_json, full_document, model_used, ollama_base_url, version, parent_script_id, is_deleted, idempotency_key, specscene)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, script.Topic, script.Title, script.Duration, script.Language, script.Template, script.Mode, script.Tone, script.TargetWords, script.FinalWordCount, script.Status, script.NarrativeText, script.TimelineJSON, script.EntitiesJSON, script.MetadataJSON, script.FullDocument, script.ModelUsed, script.OllamaBaseURL, version, script.ParentScriptID, script.IsDeleted, script.IdempotencyKey, script.SpecScene)
	if err != nil {
		return 0, fmt.Errorf("failed to insert script: %w", err)
	}

	scriptID, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed to get last insert id: %w", err)
	}

	for _, section := range sections {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO script_sections (script_id, section_type, section_title, content, sort_order, word_count, status, voiceover_link)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, scriptID, section.SectionType, section.SectionTitle, section.Content, section.SortOrder, section.WordCount, section.Status, section.VoiceoverLink)
		if err != nil {
			return 0, fmt.Errorf("failed to insert section: %w", err)
		}
	}

	for _, match := range stockMatches {
		_, err := tx.ExecContext(ctx, `
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

func (r *ScriptRepository) GetScriptByID(id int64) (*ScriptRecord, []scriptSectionRow, []scriptStockMatchRow, error) {
	var script ScriptRecord
	err := scanScriptRecord(
		r.db.QueryRow(`
			SELECT `+scriptsSelectColumns+`
			FROM scripts WHERE id = ? AND is_deleted = 0
		`, id),
		&script,
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get script: %w", err)
	}

	sections := []scriptSectionRow{}
	rows, err := r.db.Query(`		SELECT id, script_id, section_type, section_title, content, sort_order, word_count, status, voiceover_link FROM script_sections WHERE script_id = ? ORDER BY sort_order`, id)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get sections: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var s scriptSectionRow
		if err := rows.Scan(&s.ID, &s.ScriptID, &s.SectionType, &s.SectionTitle, &s.Content, &s.SortOrder, &s.WordCount, &s.Status, &s.VoiceoverLink); err != nil {
			return nil, nil, nil, fmt.Errorf("failed to scan section: %w", err)
		}
		sections = append(sections, s)
	}

	matches := []scriptStockMatchRow{}
	mRows, err := r.db.Query(`SELECT id, script_id, segment_index, stock_path, stock_source, score, matched_terms FROM script_stock_matches WHERE script_id = ?`, id)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get stock matches: %w", err)
	}
	defer mRows.Close()
	for mRows.Next() {
		var m scriptStockMatchRow
		if err := mRows.Scan(&m.ID, &m.ScriptID, &m.SegmentIndex, &m.StockPath, &m.StockSource, &m.Score, &m.MatchedTerms); err != nil {
			return nil, nil, nil, fmt.Errorf("failed to scan stock match: %w", err)
		}
		matches = append(matches, m)
	}

	return &script, sections, matches, nil
}

func (r *ScriptRepository) ListScripts(limit, offset int, language, template string) ([]ScriptRecord, int, error) {
	where := "WHERE is_deleted = 0"
	args := []any{}
	if language != "" {
		where += " AND language = ?"
		args = append(args, language)
	}
	if template != "" {
		where += " AND template = ?"
		args = append(args, template)
	}

	var total int
	countArgs := append([]any{}, args...)
	err := r.db.QueryRow("SELECT COUNT(*) FROM scripts "+where, countArgs...).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to count scripts: %w", err)
	}

	args = append(args, limit, offset)
	rows, err := r.db.Query(`
		SELECT `+scriptsSelectColumns+`
		FROM scripts `+where+` ORDER BY created_at DESC LIMIT ? OFFSET ?
	`, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list scripts: %w", err)
	}
	defer rows.Close()

	var scripts []ScriptRecord
	for rows.Next() {
		var s ScriptRecord
		if err := scanScriptRecord(rows, &s); err != nil {
			return nil, 0, fmt.Errorf("failed to scan script: %w", err)
		}
		scripts = append(scripts, s)
	}

	return scripts, total, nil
}

// FindByIdempotencyKey (PR 5/6, June 2026) looks up an existing script
// row whose stored idempotency key matches the supplied hash.
//
// PR 6 (June 2026): the lookup switched from `template = ?` (the
// pre-PR-6 dual-purpose slot) to the dedicated `idempotency_key = ?`
// column introduced in
// migrations/sqlite/100_add_idempotency_key_and_specscene_columns.sql.
// Pre-PR-6 rows had the idem key backfilled by that migration's GLOB
// predicate on `template`, so the lookup still resolves existing
// rows without operator intervention.
//
// Returns the most recently inserted matching row (ORDER BY id DESC
// LIMIT 1) so that newer replays shadow older ones when collisions
// occur. The composite index idx_scripts_idempotency_key
// (idempotency_key, language) backs the WHERE clause.
//
// On no match, returns (nil, nil) — the caller treats this as
// "fresh insert", not an error. SQL errors are surfaced so the
// caller can decide whether to fall through to insert or fail-closed.
func (r *ScriptRepository) FindByIdempotencyKey(ctx context.Context, idemKey, language string) (*ScriptRecord, error) {
	var script ScriptRecord
	err := scanScriptRecord(
		r.db.QueryRowContext(ctx, `
			SELECT `+scriptsSelectColumns+`
			FROM scripts
			WHERE idempotency_key = ? AND language = ? AND is_deleted = 0
			ORDER BY id DESC LIMIT 1
		`, idemKey, language),
		&script,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to find script by idempotency key: %w", err)
	}
	return &script, nil
}

func (r *ScriptRepository) FindByTopic(ctx context.Context, topic, language string) (*ScriptRecord, []scriptSectionRow, []scriptStockMatchRow, error) {
	var script ScriptRecord
	err := scanScriptRecord(
		r.db.QueryRowContext(ctx, `
			SELECT `+scriptsSelectColumns+`
			FROM scripts WHERE topic = ? AND language = ? AND is_deleted = 0 ORDER BY created_at DESC LIMIT 1
		`, topic, language),
		&script,
	)
	if err != nil {
		return nil, nil, nil, err
	}

	sections := []scriptSectionRow{}
	rows, err := r.db.QueryContext(ctx, "		SELECT id, script_id, section_type, section_title, content, sort_order, word_count, status, voiceover_link FROM script_sections WHERE script_id = ? ORDER BY sort_order", script.ID)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var s scriptSectionRow
		if err := rows.Scan(&s.ID, &s.ScriptID, &s.SectionType, &s.SectionTitle, &s.Content, &s.SortOrder, &s.WordCount, &s.Status, &s.VoiceoverLink); err != nil {
			return nil, nil, nil, err
		}
		sections = append(sections, s)
	}

	matches := []scriptStockMatchRow{}
	return &script, sections, matches, nil
}

func (r *ScriptRepository) SoftDeleteScript(id int64) error {
	_, err := r.db.Exec("UPDATE scripts SET is_deleted = 1, updated_at = datetime('now') WHERE id = ?", id)
	return err
}

func (r *ScriptRepository) CreateNewVersion(ctx context.Context, parentID int64, script *ScriptRecord, sections []scriptSectionRow, stockMatches []scriptStockMatchRow) (int64, error) {
	script.ParentScriptID = &parentID
	script.Version = r.getNextVersion(ctx, parentID)
	return r.SaveScript(ctx, script, sections, stockMatches)
}

func (r *ScriptRepository) NextVersionForTopic(ctx context.Context, topic, language, mode string) (int, error) {
	var maxVersion int
	err := r.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version), 0)
		 FROM scripts
		 WHERE topic = ? AND language = ? AND mode = ?`,
		topic, language, mode,
	).Scan(&maxVersion)
	if err != nil {
		return 1, fmt.Errorf("failed to get next version: %w", err)
	}
	return maxVersion + 1, nil
}

func (r *ScriptRepository) getNextVersion(ctx context.Context, parentID int64) int {
	var maxVersion int
	err := r.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(version),0) FROM scripts WHERE id = ? OR parent_script_id = ?", parentID, parentID).Scan(&maxVersion)
	if err != nil {
		return 1
	}
	return maxVersion + 1
}

func (r *ScriptRepository) HardDeleteOldDeletedScripts(ctx context.Context, daysOld int) (int64, error) {
	result, err := r.db.ExecContext(ctx, `
		DELETE FROM scripts
		WHERE is_deleted = 1
		AND updated_at < datetime('now', ?)
	`, fmt.Sprintf("-%d days", daysOld))
	if err != nil {
		return 0, fmt.Errorf("failed to hard delete old scripts: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get rows affected: %w", err)
	}
	return count, nil
}

func (r *ScriptRepository) VacuumDatabase(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, "VACUUM")
	if err != nil {
		return fmt.Errorf("failed to vacuum database: %w", err)
	}
	return nil
}

func (r *ScriptRepository) AnalyzeDatabase(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, "ANALYZE")
	if err != nil {
		return fmt.Errorf("failed to analyze database: %w", err)
	}
	return nil
}

func (r *ScriptRepository) UpdateScriptFinalContent(ctx context.Context, id int64, text string, wordCount int, status string, metadataJSON string, model string, baseURL string, version int) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE scripts
		SET narrative_text = ?, full_document = ?, final_word_count = ?, status = ?,
		    metadata_json = ?, model_used = ?, ollama_base_url = ?, version = ?,
		    updated_at = datetime('now')
		WHERE id = ?
	`, text, text, wordCount, status, metadataJSON, model, baseURL, version, id)
	return err
}

// SaveManifestV2 (PR 1, July 2026, SCRIPT-DOWNSTREAM-CUTOVER wave)
// persists the canonical NEW-mode downstream-fan-out manifest bound
// to the given scriptID. The manifest is the JSON-marshalled
// script.ManifestV2 (pre-marshalled by the caller via
// json.Marshal before the port-adapter layer; the concrete impl
// writes the bytes verbatim to the `manifest_v2 TEXT` column
// introduced by migration 134).
//
// godlike/06 SSOT one-canonical-owner-per-fact: the
// *ScriptRepository concrete is the SOLE writer of the manifest_v2
// column. No other code path mutates the column (the adapter just
// passes bytes; the persistence processor is the only caller).
//
// godlike/07 fail-closed: an empty/zero payload returns
// ErrSaveManifestV2Empty so the caller surfaces a typed sentinel
// (NOT a silent "I wrote nothing" success that would mask a
// caller-side bug).
//
// Idempotency: the call is an UPSERT keyed on scriptID — a
// re-publish of the same manifest overwrites the column
// (rows_affected = 1 on UPDATE; = 0 if scriptID not found). The
// dispatcher's fan-out uses the canonical NEW-mode (NoInlineAssets
// true) per the godlike/07 no-fake-availability contract.
func (r *ScriptRepository) SaveManifestV2(ctx context.Context, scriptID int64, manifest []byte) error {
	// godlike/07 fail-closed: nil/empty payload returns the port's
	// CANONICAL sentinel (godlike/06 SSOT one-owner-per-fact — the
	// port is the SOLE owner of the typed-error contract; the
	// concrete layer enforces the check for defense in depth and
	// uses the SAME port sentinel so errors.Is probes are stable
	// across both layers). Importing the port here is allowed
	// (infrastructure → application is fine; the port package
	// only depends on stdlib + domain, so no cycle).
	if len(manifest) == 0 {
		return scriptports.ErrSaveManifestV2NilManifest
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE scripts
		SET manifest_v2 = ?, updated_at = datetime('now')
		WHERE id = ?
	`, string(manifest), scriptID)
	if err != nil {
		return fmt.Errorf("failed to save manifest_v2 for script %d: %w", scriptID, err)
	}
	return nil
}
