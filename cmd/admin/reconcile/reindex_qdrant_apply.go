// cmd/admin/reindex_qdrant_apply.go — canonical in-place production rebuild path.
package reconcile

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/collections"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/indexing"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	platformschema "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/search"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/verification"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// newGoldenQueryExecutor builds the GoldenQueryExecutor that embeds the query
// text via the canonical E5 sidecar and searches the production collection.
// Returned IDs are the Qdrant point IDs, deterministic from the canonical
// asset ID.
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

// applyQdrant rebuilds the single production collection in place:
// remove the runtime alias, recreate media_assets, populate it from SQLite,
// verify parity and golden queries, then restore the alias. During the
// rebuild the alias is absent, so runtime reads fail closed.
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
	// The in-place production rebuild still participates in the durable
	// projection state machine. A unique build id makes retries safe while
	// keeping the physical runtime collection fixed at media_assets.
	projectionID := fmt.Sprintf("production-rebuild-%d", time.Now().UTC().UnixNano())
	if err := collectionMgr.BeginProjection(ctx, projectionID, targetCollection, 0); err != nil {
		return fmt.Errorf("begin production projection rebuild: %w", err)
	}
	failBuild := func(cause error) error {
		if failErr := collectionMgr.FailProjection(ctx, projectionID); failErr != nil {
			return fmt.Errorf("%w; persist FAILED state: %v", cause, failErr)
		}
		return cause
	}
	if err := collectionMgr.PrepareProductionCollection(ctx); err != nil {
		return failBuild(fmt.Errorf("prepare production collection: %w", err))
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

	if err := populate(ctx, targetCollection); err != nil {
		return failBuild(fmt.Errorf("rebuild production collection %q: %w", targetCollection, err))
	}
	if reindexResult == nil {
		return failBuild(fmt.Errorf("build projection %q completed without a reindex result", targetCollection))
	}
	log.Info("projection candidate populated",
		zap.String("collection", targetCollection),
		zap.Int("indexed", reindexResult.IndexedAssets),
		zap.Int("failed", reindexResult.FailedAssets))

	// The expected projection cardinality is reloaded from SQLite by the
	// verifier. Passing the SQLite inventory count here makes the source
	// boundary explicit; IndexedAssets is only an operational write result
	// and must not become runtime truth after partial mapping failures.
	report, verifyErr := collectionMgr.ValidateProjection(ctx, projectionID, 0, reindexResult.SQLiteIndexableAssets)
	if report == nil {
		return fmt.Errorf("validate projection %q returned no report: %w", targetCollection, verifyErr)
	}
	report.RollbackTarget = ""
	report.OldCollection = targetCollection

	if deps.JSON {
		b, _ := json.Marshal(report)
		fmt.Println(string(b))
	}
	if verifyErr != nil || !report.Ready {
		log.Error("production projection validation failed; runtime alias remains absent",
			zap.String("target", targetCollection),
			zap.Bool("complete_scan", report.CompleteScan),
			zap.Int("sqlite_indexable_assets", report.SQLiteIndexableAssets),
			zap.Int("expected_points", report.ExpectedPoints),
			zap.Int("actual_points", report.ActualPoints),
			zap.Strings("errors", report.Errors),
			zap.Error(verifyErr))
		return failBuild(&transport.ErrAliasSwitchNotReady{Report: report})
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
			return failBuild(fmt.Errorf("golden query certification for %q: %w", targetCollection, certErr))
		}
		log.Info("golden query certification passed",
			zap.String("target", targetCollection),
			zap.Int("queries", len(platformschema.CanonicalGoldenQueries())))
	}

	if err := collectionMgr.ActivateProjection(ctx, projectionID, 0); err != nil {
		log.Error("production projection validated but runtime alias restoration failed",
			zap.String("target", targetCollection),
			zap.Error(err))
		return failBuild(fmt.Errorf("activate production collection %q: %w", targetCollection, err))
	}
	log.Info("production projection rebuilt and runtime alias restored",
		zap.String("alias", schemaObj.RuntimeAlias),
		zap.String("collection", targetCollection))

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
