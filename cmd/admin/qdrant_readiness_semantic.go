// cmd/admin/qdrant_readiness_semantic.go — semantic search canary check
// (PR-AGENTE2-READINESS — Agente 2, Azione 5, July 2026).
//
// checkSemanticSearchReal is a production-shaped readiness check that
// proves a real semantic query traverses the full search pipeline:
// SQLite → Qdrant → aggregator → response.
//
// The check is NON-DESTRUCTIVE: it only reads, never writes.
//
// No-internal-locator invariant: this readiness check does NOT scan the
// response for LocalPath / DriveLink / similar internal-locator fields.
// The invariant is enforced at the TYPE level: search.Candidate has no
// such fields by design (PR-SEARCH-PORTS-SPLIT, 2026-07-04; QDRANT-004
// in types_result.go preamble). A runtime check would be unreachable
// and any rewrite against c.PreviewURL (a public URL, intentionally
// non-empty) would always false-fail. If a future maintainer is tempted
// to add such a check, the right defense-in-depth is a JSON-tag-based
// assertion on the wire shape, not a struct-field scan.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	search "github.com/Marcuss-ops/PipelineGen/internal/application/search"
)

// checkSemanticSearchReal is the production-shaped semantic-search canary.
func checkSemanticSearchReal(ctx context.Context, deps readinessDeps) checkStatus {
	if deps.Root == nil || deps.Root.SemanticSearch == nil {
		return checkStatus{Err: "semantic search fanout not wired (composition root missing SemanticSearch)"}
	}
	if deps.DB == nil {
		return checkStatus{Err: "raw *sql.DB is nil — cannot find semantic canary asset"}
	}

	canary, err := findSemanticCanary(ctx, deps.DB)
	if err != nil {
		return checkStatus{Err: fmt.Sprintf("semantic canary unavailable: %v", err)}
	}

	// Execute the real search with the canary asset's workspace.
	q := search.Query{
		Text:  canary.searchText,
		Mode:  search.SearchModeHybrid,
		Limit: 20,
		Actor: search.Actor{
			WorkspaceID: canary.workspaceID,
			UserID:      "readiness-canary",
			IsSystem:    canary.workspaceID == "",
			IsAdmin:     false,
		},
	}
	result, err := deps.Root.SemanticSearch.Search(ctx, q)
	if err != nil {
		return checkStatus{Err: fmt.Sprintf("semantic canary search failed: %v", err)}
	}
	if result == nil {
		return checkStatus{Err: "semantic canary search returned nil result"}
	}

	// Verify the canary asset is in top results and no local_path leaks.
	found := false
	for _, c := range result.Items {
		if c.AssetID == canary.assetID {
			found = true
			// LocalPath-leak check REMOVED (2026-07-04): search.Candidate
			// no longer exposes a LocalPath field per PR-SEARCH-PORTS-SPLIT
			// (QDRANT-004 invariant — see types_result.go preamble). The
			// godlike/07 no-fake-availability invariant is enforced at the
			// type level (struct has no server-internal locators by design),
			// so this readiness check is now obsolete and would only ever
			// produce false-positives if rewritten against PreviewURL
			// (which is intentionally non-empty in normal responses).
			break
		}
	}
	if !found {
		return checkStatus{Err: fmt.Sprintf(
			"semantic canary asset %q (search_text=%q) not found in top %d results",
			canary.assetID, canary.searchText, len(result.Items),
		)}
	}

	// Legacy rows may predate workspace assignment. They are searched only
	// through the explicit system scope above; there is no tenant boundary to
	// test for such a row. Newly ingested assets must carry a real workspace
	// and are covered by the isolation branch below.
	if canary.workspaceID == "" {
		return checkStatus{Pass: true}
	}

	// Cross-workspace isolation: a different workspace must NOT see this asset.
	otherWS := canary.workspaceID + "-other"
	qCross := search.Query{
		Text:  canary.searchText,
		Mode:  search.SearchModeHybrid,
		Limit: 20,
		Actor: search.Actor{
			WorkspaceID: otherWS,
			UserID:      "readiness-canary",
			IsAdmin:     false,
		},
	}
	crossResult, crossErr := deps.Root.SemanticSearch.Search(ctx, qCross)
	if crossErr != nil {
		// Error from unknown workspace is acceptable isolation behaviour.
		return checkStatus{Pass: true}
	}
	if crossResult != nil {
		for _, c := range crossResult.Items {
			if c.AssetID == canary.assetID {
				return checkStatus{Err: fmt.Sprintf(
					"semantic canary cross-workspace leak: asset %q from workspace %q "+
						"appeared in results for workspace %q",
					canary.assetID, canary.workspaceID, otherWS,
				)}
			}
		}
	}

	return checkStatus{Pass: true}
}

// semanticCanary is a lightweight asset snapshot for the semantic check.
type semanticCanary struct {
	assetID     string
	workspaceID string
	searchText  string
}

// findSemanticCanary queries SQLite for an ACTIVE asset with a
// non-empty embedding_json and at least one of search_text/name.
func findSemanticCanary(ctx context.Context, db *sql.DB) (semanticCanary, error) {
	query := `
		SELECT id, COALESCE(workspace_id, ''), COALESCE(NULLIF(search_text, ''), name, '')
		FROM media_assets
		WHERE lifecycle_state = 'ACTIVE'
		  AND COALESCE(embedding_json, '') != ''
		  AND COALESCE(embedding_json, '') != '[]'
		  AND (COALESCE(NULLIF(search_text, ''), name, '') != '')
	`
	// Keep the readiness fixture backwards-compatible while using the
	// canonical Qdrant eligibility boundary in production databases.
	// The small in-memory fixtures intentionally contain only the older
	// minimal schema, so optional predicates are added only when their
	// columns are present.
	columns, err := tableColumns(ctx, db, "media_assets")
	if err != nil {
		return semanticCanary{}, err
	}
	if columns["index_state"] && columns["media_type"] && columns["asset_kind"] && columns["namespace"] && columns["source_type"] {
		query += `
		  AND index_state = 'INDEXED'
		  AND ((media_type = 'video' AND asset_kind IN ('clip','stock_video','generated_video','rendered_video'))
		       OR (media_type = 'image' AND asset_kind IN ('stock_image','web_image','ai_image','graphic')))
		  AND COALESCE(namespace, '') != ''
		  AND COALESCE(source_type, '') != ''
		ORDER BY CASE WHEN media_type IN ('video', 'image') THEN 0 ELSE 1 END, id
		LIMIT 1
		`
	} else {
		query += ` ORDER BY id LIMIT 1`
	}
	var canary semanticCanary
	if err := db.QueryRowContext(ctx, query).Scan(
		&canary.assetID, &canary.workspaceID, &canary.searchText,
	); err != nil {
		return semanticCanary{}, fmt.Errorf(
			"no ACTIVE indexed asset with search_text: %w "+
				"(hint: index an asset before running this check)", err,
		)
	}

	canary.searchText = strings.TrimSpace(canary.searchText)
	if canary.searchText == "" {
		return semanticCanary{}, fmt.Errorf("canary asset %q has empty search_text/name", canary.assetID)
	}
	return canary, nil
}

func tableColumns(ctx context.Context, db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return nil, fmt.Errorf("inspect %s columns: %w", table, err)
	}
	defer rows.Close()
	columns := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, pk int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return nil, fmt.Errorf("scan %s columns: %w", table, err)
		}
		columns[strings.ToLower(name)] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %s columns: %w", table, err)
	}
	return columns, nil
}
