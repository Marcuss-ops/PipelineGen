package scripts

import (
	"context"
	"fmt"
)

func (r *ScriptRepository) SaveScriptVersion(ctx context.Context, rec *ScriptVersionRecord) (int64, error) {
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO script_versions (script_id, version, final_text, metadata_json)
		VALUES (?, ?, ?, ?)
	`, rec.ScriptID, rec.Version, rec.FinalText, rec.MetadataJSON)
	if err != nil {
		return 0, fmt.Errorf("failed to insert script version: %w", err)
	}
	return result.LastInsertId()
}

func (r *ScriptRepository) GetScriptVersions(ctx context.Context, scriptID int64) ([]ScriptVersionRecord, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, script_id, version, final_text, metadata_json, created_at
		 FROM script_versions WHERE script_id = ? ORDER BY version`, scriptID)
	if err != nil {
		return nil, fmt.Errorf("failed to query script versions: %w", err)
	}
	defer rows.Close()

	var versions []ScriptVersionRecord
	for rows.Next() {
		var v ScriptVersionRecord
		if err := rows.Scan(&v.ID, &v.ScriptID, &v.Version, &v.FinalText, &v.MetadataJSON, &v.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan script version: %w", err)
		}
		versions = append(versions, v)
	}
	return versions, nil
}
