// cmd/admin/reindex_qdrant_apply.go — the apply path extracted from the
// canonical runReindexQdrant orchestrator (LONG-FILES-DECOMPOSITION-V2-2026-07-06
// P3 BASSA, July 2026). Extract-method refactor: the apply branch body is
// moved verbatim from runReindexQdrant into this file's applyQdrant function.
// The 3-file split layout:
//
//   - reindex_qdrant.go          (slim orchestrator) — package doc + reindexQdrantDeps + parseReindexQdrantArgs + timestampedTargetCollection + runReindexQdrant (thin dispatch)
//   - reindex_qdrant_dryrun.go   (sibling)            — dryRunQdrant helper
//   - reindex_qdrant_apply.go    (this file)         — applyQdrant helper
package reconcile

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/collections"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/indexing"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/search"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/transport"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/verification"
)

// applyQdrant executes the 4-phase apply path: create target collection,
// reindex all assets, verify via the strict PR 12 verifier, and switch
// the runtime alias atomically. PR 13 (June 2026) closure — Blue-green
// reindex: Apply mode NEVER reuses schema.PhysicalName as the target
// collection. Each invocation creates a brand-new timestamped collection.
//
// QDRANT-003 PR fix (June 2026): wired CollectionManager for schema creation,
// post-reindex verification, and atomic alias swap. The old code wrote into
// the target collection but never ensured the schema existed, never verified
// the result, and never switched the alias.
//
// QDRANT-004 closure: the alias switch is GATED on a SwitchReport.Ready=true
// verification. The previous code unconditionally swapped at the end of
// phase 4 — a regression that could promote a half-written or schema-broken
// collection into service. On failure it returns *transport.ErrAliasSwitchNotReady
// and never touches the alias.
//
// PR 12 (June 2026): STICT equality (was `>=`); per-channel scan runs on EVERY
// scrolled page; ANY scroll error or max-pages cap hit returns non-nil err
// + CompleteScan=false + Ready=false; pt.ID MUST equal
// AssetIDToQdrantPointID(payload["asset_id"]) literally (not just uuid-parseable).
func applyQdrant(
	ctx context.Context,
	log *zap.Logger,
	schemaObj *schema.IndexSchema,
	writer *indexing.IndexWriter,
	collectionMgr *collections.CollectionManager,
	client *transport.Client,
	assetStore *indexing.SQLiteAssetStore,
	sqliteDB *sql.DB,
	deps reindexQdrantDeps,
	targetCollection string,
) error {
	// Phase 1: Create the target collection with matching schema.
	// Blocco 4a (July 2026): use CreateCollection(ctx, targetCollection)
	// instead of EnsureSchema(ctx). Pre-fix, EnsureSchema hardcoded
	// schema.CanonicalName() internally and ignored the timestamped
	// targetCollection computed above — so the new physical collection
	// was never created, and Phase 2 wrote points into a non-existent
	// collection. CreateCollection explicitly takes the target name
	// and creates the physical collection + payload indexes without
	// aliasing — exactly what the blue-green reindex needs.
	if err := collectionMgr.CreateCollection(ctx, targetCollection); err != nil {
		return fmt.Errorf("create target collection %q: %w", targetCollection, err)
	}
	log.Info("target collection created", zap.String("collection", targetCollection))

	// Phase 2: Reindex all assets into the target collection.
	start := time.Now()
	reindexResult, err := writer.ReindexAll(ctx, targetCollection, deps.Limit)
	elapsed := time.Since(start)
	if err != nil {
		// QDRANT-003 fix: guard nil reindexResult before dereference.
		indexed, failed := 0, 0
		if reindexResult != nil {
			indexed = reindexResult.IndexedAssets
			failed = reindexResult.FailedAssets
		}
		log.Error("reindex failed",
			zap.Int("indexed", indexed),
			zap.Int("failed", failed),
			zap.Error(err))
		return fmt.Errorf("reindex failed after %d indexed / %d failed: %w", indexed, failed, err)
	}

	// PR 13: capture the currently-active alias target BEFORE running
	// the verifier. We need it on BOTH paths (Ready=true and
	// Ready=false) so the operator's report carries the rollback
	// target. CaptureException is logged but not fatal — a missing
	// runtime alias at this point means the verifier will surface
	// other failures anyway.
	oldTarget, err := collectionMgr.GetActiveCollection(ctx)
	if err != nil {
		log.Warn("PR 13: could not read active collection before verify (report rollback target will be empty)",
			zap.Error(err))
	}
	log.Info("PR 13: pre-verify active target captured",
		zap.String("active_target", oldTarget),
		zap.String("apply_target", targetCollection))

	// Phase 3: Post-reindex verification (QDRANT-003 + PR 12 gates).
	// The verifier runs the full suite: strict point count parity,
	// missing/orphan ID detection, payload minimum, FULL per-channel
	// embedding version, dead-letter check, and PR 12's
	// non-canonical-pt.ID gate. Ready==true only when all gates pass.
	deadLetter := search.NewOutboxEventsDeadLetterAdapter(outboxevents.NewRepository(sqliteDB))
	verifier := verification.NewReindexVerifier(client, assetStore, deadLetter, schemaObj, nil, log)
	report, verifyErr := verifier.VerifyReindex(ctx, targetCollection, reindexResult.IndexedAssets)

	// PR 13: populate rollback metadata on the report regardless
	// of gateway result. The operator reading the JSON
	// (ErrAliasSwitchNotReady or success print) MUST see
	// `rollback_target` so the recovery path is deterministic.
	report.RollbackTarget = oldTarget
	report.OldCollection = targetCollection

	if verifyErr != nil {
		// PR 12 hardening: a non-nil verifyErr implies scrollAborted or
		// another fatal infrastructure failure (page error, cap hit).
		// It is defence-in-depth to surface the err AND keep the
		// alias-switch gate fired.
		log.Error("PR 12: verification infrastructure failure (no alias mutation will follow)",
			zap.String("target", targetCollection),
			zap.Error(verifyErr))
	}
	if deps.JSON {
		b, _ := json.Marshal(report)
		fmt.Println(string(b))
	}
	if !report.Ready {
		// PR 13: Ready=false path. Alias NOT switched (we never
		// call SwitchAlias on this branch), new timestamped
		// collection RETAINED (Qdrant has no auto-delete so
		// keeping is the default), report carries rollback info.
		log.Error("alias switch BLOCKED by SwitchReport.Ready=false (no alias mutation performed; new collection RETAINED for retry)",
			zap.String("target", targetCollection),
			zap.String("rollback_target", report.RollbackTarget),
			zap.Int("expected_points", report.ExpectedPoints),
			zap.Int("actual_points", report.ActualPoints),
			zap.Bool("complete_scan", report.CompleteScan),
			zap.Int("scrolled", report.TotalScrolled),
			zap.Strings("errors", report.Errors))
		return &transport.ErrAliasSwitchNotReady{Report: report}
	}
	if verifyErr != nil {
		// Belt-and-braces: Ready could be true while an unrecoverable
		// infrastructure failure (scrollAborted/cap-binding) is reported
		// via non-nil err. The cmd path refuses the switch in that
		// pathological case as well.
		log.Error("PR 12: verifyErr is non-nil while Ready=true (defence-in-depth block)",
			zap.String("target", targetCollection),
			zap.Error(verifyErr))
		return &transport.ErrAliasSwitchNotReady{Report: report}
	}
	log.Info("switch gate PASSED (Ready=true)",
		zap.String("target", targetCollection),
		zap.String("rollback_target", report.RollbackTarget),
		zap.Int("expected_points", report.ExpectedPoints),
		zap.Int("actual_points", report.ActualPoints),
		zap.Int("missing", report.MissingCount),
		zap.Int("orphan", report.OrphanCount),
		zap.Int("dead_letter_open", report.DeadLetterOpen),
		zap.Bool("golden_queries_ok", report.GoldenQueriesOK),
		zap.Bool("filters_ok", report.FiltersOK))

	// PR 13: SwitchAlias(oldTarget, targetCollection). The old
	// collection is RETAINED (Qdrant has no auto-delete; we never
	// call DELETE on it) so the operator can `retry
	// --target-collection=<oldTarget>` to rollback manually.
	if err := collectionMgr.SwitchAlias(ctx, oldTarget, targetCollection); err != nil {
		// PR 13: SwitchAlias failed AFTER the verifier passed.
		// The new collection is RETAINED (never deleted). The
		// operator must investigate; the most common failure
		// mode is a concurrent alias mutation in another admin
		// process or a Qdrant write conflict on the alias table.
		log.Error("PR 13 alias switch failed after verify — neither collection deleted; manual intervention required",
			zap.String("from", oldTarget),
			zap.String("to", targetCollection),
			zap.String("rollback_target", report.RollbackTarget),
			zap.Error(err))
		return fmt.Errorf("PR 13 switch alias from %q to %q (rollback to %q via --target-collection): %w",
			oldTarget, targetCollection, oldTarget, err)
	}
	// PR 13: alias swapped. After this point, the runtime alias
	// points at the new timestamped collection; the previous one
	// (oldTarget) is retained. `report.RollbackTarget` was set
	// BEFORE the switch; reading it post-switch tells the operator
	// "this is the collection to swap back to" if turnup needs to
	// be reverted later.
	log.Info("PR 13 alias switched (blue-green complete; previous collection retained)",
		zap.String("alias", schemaObj.RuntimeAlias),
		zap.String("old", oldTarget),
		zap.String("new", targetCollection),
		zap.String("rollback_target", oldTarget))

	if deps.JSON {
		b, _ := json.Marshal(reindexResult)
		fmt.Println(string(b))
		return nil
	}

	log.Info("reindex complete",
		zap.Int("total", reindexResult.TotalAssets),
		zap.Int("indexed", reindexResult.IndexedAssets),
		zap.Int("failed", reindexResult.FailedAssets),
		zap.String("collection", reindexResult.TargetCollection),
		zap.Duration("elapsed", elapsed))

	fmt.Printf("Reindex complete: %d indexed, %d failed (of %d total) into %q in %s\n",
		reindexResult.IndexedAssets,
		reindexResult.FailedAssets,
		reindexResult.TotalAssets,
		reindexResult.TargetCollection,
		elapsed.Round(time.Millisecond))
	return nil
}
