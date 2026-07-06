//go:build ignore

// Package fixture — self-check fixture for Check 13 (TODO 16, TODO 2).
//
// This file (check_13_listassets_placeholder.go) demonstrates the
// ListAssetsForReconcile build-time placeholder. The placeholder
// returns an error so any reconcile --apply call produces 0 findings,
// hiding real drift. The fix is to implement the SQL scan; this
// fixture documents the placeholder text the lint is designed to
// catch.
package fixture

import (
	"context"
	"fmt"
)

// Forbidden: SQLiteAssetStore.ListAssetsForReconcile currently returns
// this placeholder string. The canonical implementation must run the
// real SQL scan and return the rows for the reconciler to classify.
func placeholderListAssets(ctx context.Context, states []string) (any, error) {
	return nil, fmt.Errorf("ListAssetsForReconcile: wired as build-time placeholder only (QDRANT-005); reconcile jobs must implement the SQL scan before being enabled — requested lifecycleStates=%v", states)
}
