package entitycatalog

// entity_image_catalog_audit.go is the SQL read behind the catalog audit. The
// classification itself lives in capabilities/images/entitycatalog/audit.go;
// this file only owns the join, because this package is the sole SQL owner of
// the entity image catalog tables.
//
// The read is unbounded on purpose: an audit that silently truncated its input
// would report a clean catalog while rows past the limit kept polluting reuse.

import (
	"context"
	"database/sql"
	"fmt"

	capentity "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
)

// ListCatalogAuditRows reads every candidate with its entity name and its
// materialization state. A candidate without a materialization row is returned
// with empty materialization fields, never dropped: a discovered-but-never-
// materialized row is still visible to the duplicate rules.
func (a *SQLiteEntityImageCatalogAdapter) ListCatalogAuditRows(ctx context.Context) ([]capentity.CatalogAuditRow, error) {
	if a == nil || a.db == nil {
		return nil, fmt.Errorf("entity image catalog: database unavailable")
	}
	rows, err := a.db.QueryContext(ctx, `
		SELECT c.candidate_id,
		       COALESCE(c.canonical_entity_id, ''),
		       COALESCE(e.canonical_name, ''),
		       COALESCE(c.source_url, ''),
		       COALESCE(c.content_sha256, ''),
		       COALESCE(c.status, ''),
		       COALESCE(c.semantic_status, ''),
		       COALESCE(m.status, ''),
		       COALESCE(m.content_sha256, '')
		FROM entity_image_catalog_candidates c
		LEFT JOIN entity_image_catalog_entities e
		       ON e.canonical_entity_id = c.canonical_entity_id
		LEFT JOIN entity_image_catalog_materializations m
		       ON m.candidate_id = c.candidate_id
		ORDER BY c.candidate_id`)
	if err != nil {
		return nil, fmt.Errorf("entity image catalog: list audit rows: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanCatalogAuditRows(rows)
}

func scanCatalogAuditRows(rows *sql.Rows) ([]capentity.CatalogAuditRow, error) {
	out := make([]capentity.CatalogAuditRow, 0)
	for rows.Next() {
		var row capentity.CatalogAuditRow
		if err := rows.Scan(
			&row.CandidateID, &row.EntityID, &row.EntityName, &row.SourceURL,
			&row.ContentSHA256, &row.CandidateStatus, &row.SemanticStatus,
			&row.MaterializationStatus, &row.MaterializationSHA,
		); err != nil {
			return nil, fmt.Errorf("entity image catalog: scan audit row: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("entity image catalog: iterate audit rows: %w", err)
	}
	return out, nil
}
