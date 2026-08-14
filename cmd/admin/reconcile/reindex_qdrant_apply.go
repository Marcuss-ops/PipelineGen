// cmd/admin/reindex_qdrant_apply.go — canonical blue-green Qdrant apply path.
package reconcile

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/indexing"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/search"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/transport"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/verification"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/collections"
)

// newGoldenQueryExecutor builds the GoldenQueryExecutor that embeds the query
// text via the canonical E5 sidecar and searches the candidate collection
// DIRECTLY (never the runtime alias — the alias still points at the previous
// generation during validation). Returned IDs are the Qdrant point IDs, which
// are deterministic (SHA-256 of the canonical asset ID).
func newGoldenQueryExecutor(client *transport.Client, embedder search.TextEmbedder) collections.GoldenQueryExecutor {
	return func(ctx context.Context, collection, query string, topK int) ([]string, error) {
		vec, err := embedder.Embed(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("embed golden query %q: %w", query, err)
		}
		results, err := client.SearchPoints(ctx, collection, schema.SearchRequest{
			QueryVector: vec,
			VectorName:  "text",
			Limit:       topK,
		})
		if err != nil {
			return nil, fmt.Errorf("search golden query %q against %q: %w", query, collection, err)
		}
		ids := make([]string, 0, len(results))
		for _, r := range results {
			ids = append(ids, r.ID)
		}
		return ids, nil
	}
}

// applyQdrant executes the blue-green lifecycle owned by ProjectionManager:
// BUILDING (create + populate), VALIDATING (strict verifier), golden
// certification, READY, and ACTIVE (atomic alias switch). A failed build,
// validation or golden run never mutates the runtime alias; the candidate
// collection is retained for diagnosis/retry.
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
	golden collections.GoldenQueryExecutor,
) error {
	oldTarget, err := collectionMgr.GetActiveCollection(ctx)
	if err != nil {
		log.Warn("could not read active collection before projection build; rollback target will be empty", zap.Error(err))
	}

	deadLetter := search.NewOutboxEventsDeadLetterAdapter(outboxevents.NewRepository(sqliteDB))
	verifier := verification.NewReindexVerifier(client, assetStore, deadLetter, schemaObj, nil, log)
	collectionMgr.SetReindexVerifier(verifier)

	var (
		reindexResult *schema.ReindexResult
		elapsed       time.Duration
	)
	populate := func(ctx context.Context, collection string) error {
		start := time.Now()
		var reindexErr error
		reindexResult, reindexErr = writer.ReindexAll(ctx, collection, deps.Limit)
		elapsed = time.Since(start)
		if reindexErr != nil {
			indexed, failed := 0, 0
			if reindexResult != nil {
				indexed = reindexResult.IndexedAssets
				failed = reindexResult.FailedAssets
			}
			return fmt.Errorf("reindex failed after %d indexed / %d failed: %w", indexed, failed, reindexErr)
		}
		return nil
	}

	if err := collectionMgr.RebuildProjection(ctx, targetCollection, targetCollection, 0, populate); err != nil {
		return fmt.Errorf("build projection %q: %w", targetCollection, err)
	}
	if reindexResult == nil {
		return fmt.Errorf("build projection %q completed without a reindex result", targetCollection)
	}
	log.Info("projection candidate populated",
		zap.String("collection", targetCollection),
		zap.Int("indexed", reindexResult.IndexedAssets),
		zap.Int("failed", reindexResult.FailedAssets))

	report, verifyErr := collectionMgr.ValidateProjection(ctx, targetCollection, 0, reindexResult.IndexedAssets)
	if report == nil {
		return fmt.Errorf("validate projection %q returned no report: %w", targetCollection, verifyErr)
	}
	report.RollbackTarget = oldTarget
	report.OldCollection = targetCollection

	if deps.JSON {
		b, _ := json.Marshal(report)
		fmt.Println(string(b))
	}
	if verifyErr != nil || !report.Ready {
		log.Error("projection validation blocked activation",
			zap.String("target", targetCollection),
			zap.String("rollback_target", report.RollbackTarget),
			zap.Bool("complete_scan", report.CompleteScan),
			zap.Int("expected_points", report.ExpectedPoints),
			zap.Int("actual_points", report.ActualPoints),
			zap.Strings("errors", report.Errors),
			zap.Error(verifyErr))
		return &transport.ErrAliasSwitchNotReady{Report: report}
	}

	// PR-HASH-SEMANTICS (item 14): certify golden query reproducibility before
	// the alias switch. 5 canonical queries × 10 runs must return identical
	// ordered top-10 IDs against the candidate collection; the first drift
	// blocks activation.
	if golden != nil {
		if certErr := collections.CertifyGoldenQueries(ctx, targetCollection, golden); certErr != nil {
			log.Error("golden query certification blocked activation",
				zap.String("target", targetCollection),
				zap.Error(certErr))
			return fmt.Errorf("golden query certification for %q: %w", targetCollection, certErr)
		}
		log.Info("golden query certification passed",
			zap.String("target", targetCollection),
			zap.Int("queries", len(schema.CanonicalGoldenQueries())))
	}

	if err := collectionMgr.ActivateProjection(ctx, targetCollection, 0); err != nil {
		log.Error("projection activation failed; previous alias remains authoritative",
			zap.String("target", targetCollection),
			zap.String("rollback_target", oldTarget),
			zap.Error(err))
		return fmt.Errorf("activate projection %q (rollback to %q): %w", targetCollection, oldTarget, err)
	}
	log.Info("projection ACTIVE; alias switched atomically",
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
