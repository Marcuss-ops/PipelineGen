// cmd/admin/reindex_qdrant_dryrun.go — the dry-run path extracted from
// the canonical runReindexQdrant orchestrator (LONG-FILES-DECOMPOSITION-V2-2026-07-06
// P3 BASSA, July 2026). The dry-run branch body is moved
// verbatim from runReindexQdrant. The 3-file split layout:
//
//   - reindex_qdrant.go          (slim orchestrator) — package doc + reindexQdrantDeps + parseReindexQdrantArgs + timestampedTargetCollection + runReindexQdrant (thin dispatch)
//   - reindex_qdrant_dryrun.go   (this file)         — dryRunQdrant helper
//   - reindex_qdrant_apply.go    (sibling)            — applyQdrant helper
package reconcile

import (
	"context"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/indexing"
)

// dryRunQdrant executes the side-effect-free enumeration path: list
// all asset IDs via mapper.ListAllAssetIDs, print count, and emit
// either a JSON result or a human-readable summary.
//
// QDRANT-003 (June 2026): dry-run is the canonical "verify before write"
// surface — no Qdrant writes occur, no collection is created, no
// alias is touched. The function is the "noop" twin of applyQdrant
// (sibling file) and shares the build-Qdrant-stack preamble with
// the orchestrator (slim reindex_qdrant.go).
func dryRunQdrant(
	ctx context.Context,
	log *zap.Logger,
	mapper *indexing.PayloadMapper,
	targetCollection string,
	deps reindexQdrantDeps,
) error {
	assetIDs, err := mapper.ListAllAssetIDs(ctx)
	if err != nil {
		return fmt.Errorf("list assets for dry-run: %w", err)
	}
	if deps.Limit > 0 && len(assetIDs) > deps.Limit {
		assetIDs = assetIDs[:deps.Limit]
	}

	result := map[string]any{
		"mode":              "dry-run",
		"target_collection": targetCollection,
		"total_assets":      len(assetIDs),
		"limit":             deps.Limit,
	}

	if deps.JSON {
		b, _ := json.Marshal(result)
		fmt.Println(string(b))
		return nil
	}

	log.Info("DRY-RUN complete (no Qdrant writes)",
		zap.Int("total_assets", len(assetIDs)),
		zap.String("target_collection", targetCollection))
	fmt.Printf("Dry-run: %d assets would be reindexed into %q\n", len(assetIDs), targetCollection)
	fmt.Println("Re-run with --apply to execute.")
	return nil
}
