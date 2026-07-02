// cmd/admin/qdrant_readiness_semantic.go — semantic search canary check
// (PR-AGENTE2-READINESS — Agente 2, Azione 5, July 2026).
//
// checkSemanticSearchReal is a production-shaped readiness check that
// proves a real semantic query traverses the full search pipeline:
// SQLite → Qdrant → aggregator → response.
//
// The check is NON-DESTRUCTIVE: it only reads, never writes.
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
			if strings.TrimSpace(c.LocalPath) != "" {
				return checkStatus{Err: fmt.Sprintf(
					"semantic canary integrity: result for asset %q exposes local_path=%q",
					canary.assetID, c.LocalPath,
				)}
			}
			break
		}
	}
	if !found {
		return checkStatus{Err: fmt.Sprintf(
			"semantic canary asset %q (search_text=%q) not found in top %d results",
			canary.assetID, canary.searchText, len(result.Items),
		)}
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
		SELECT id, COALESCE(workspace_id, ''), COALESCE(search_text, name, '')
		FROM media_assets
		WHERE lifecycle_state = 'ACTIVE'
		  AND COALESCE(embedding_json, '') != ''
		  AND COALESCE(embedding_json, '') != '[]'
		  AND (COALESCE(search_text, '') != '' OR COALESCE(name, '') != '')
		ORDER BY id
		LIMIT 1
	`
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
	if canary.workspaceID == "" {
		canary.workspaceID = "default"
	}

	return canary, nil
}
