// cmd/admin/reindex_qdrant.go — canonical SQLite → Qdrant rebuild command.
//
// One-shot reindex of SQLite-indexable media assets into the single
// production collection `media_assets` using the canonical
// IndexWriter.ReindexAll pipeline (AssetStore → PayloadMapper → IndexWriter).
// Runtime routing is production-only: this command has no blue-green,
// versioned-target, recovery, synthetic, or collection override mode.
//
// Usage:
//
//	go run ./cmd/admin reindex-qdrant                         # dry-run (counts only)
//	go run ./cmd/admin reindex-qdrant --apply                 # rebuild media_assets in place
//	go run ./cmd/admin reindex-qdrant --dry-run --limit=500    # preview a capped subset
//	go run ./cmd/admin reindex-qdrant --json                  # machine-readable output
package reconcile

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/embeddings"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/collections"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/indexing"
	platformschema "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	qdrantschema "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/search"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
	regsql "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/mediaregistry"
)

// reindexQdrantDeps holds the parsed flags for RunReindexQdrant.
type reindexQdrantDeps struct {
	Apply            bool
	JSON             bool
	DryRun           bool
	Limit            int
	TargetCollection string
}

// parseReindexQdrantArgs parses CLI args into reindexQdrantDeps.
// Flags:
//
//	--apply              actually write to Qdrant (default: dry-run)
//	--dry-run            explicit dry-run (default, omit when --apply)
//	--json               machine-readable output
//	--limit=N            cap number of assets (dry-run only)	//	--target-collection=X  rejected: runtime target is always media_assets

func parseReindexQdrantArgs(args []string) (reindexQdrantDeps, error) {
	deps := reindexQdrantDeps{}
	for _, a := range args {
		a = strings.TrimSpace(a)
		switch {
		case a == "--apply":
			deps.Apply = true
		case a == "--dry-run":
			deps.DryRun = true
		case a == "--json":
			deps.JSON = true
		case strings.HasPrefix(a, "--limit="):
			n, err := cli.ParsePositiveFlag(a, "--limit")
			if err != nil {
				return deps, err
			}
			deps.Limit = n
		case strings.HasPrefix(a, "--target-collection="):
			return deps, fmt.Errorf("--target-collection is not supported; runtime rebuild target is always %q", platformschema.ProductionCollection)
		default:
			if strings.HasPrefix(a, "-") {
				return deps, fmt.Errorf("unknown flag: %s", a)
			}
		}
	}
	if deps.Apply && deps.DryRun {
		return deps, fmt.Errorf("--apply and --dry-run are mutually exclusive")
	}
	if deps.Apply && deps.Limit > 0 {
		return deps, fmt.Errorf("--limit is supported only for dry-run; apply always rebuilds every SQLite-indexable asset")
	}
	if deps.TargetCollection != "" {
		return deps, fmt.Errorf("target collection overrides are not supported; runtime target is always %q", platformschema.ProductionCollection)
	}
	return deps, nil
}

// RunReindexQdrant is the entry point registered in cmd/admin/main.go.
// It is the thin orchestrator that:
//  1. Loads config + opens the media DB
//  2. Builds the canonical Qdrant stack (schema + assetStore + mapper + client + writer + collectionMgr)
//  3. Dispatches to dryRunQdrant (sibling) or applyQdrant (sibling) based on deps.Apply
//
// The two phase handlers (dryRunQdrant, applyQdrant) live in sibling files
// per the 3-file split layout (LONG-FILES-DECOMPOSITION-V2-2026-07-06 P3 BASSA).
func RunReindexQdrant(args []string) error {
	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	deps, err := parseReindexQdrantArgs(args)
	if err != nil {
		return err
	}

	if !cfg.Qdrant.Enabled {
		return errors.New(
			"qdrant is disabled in config (qdrant.enabled=false); " +
				"reindex-qdrant requires qdrant.enabled=true",
		)
	}

	ctx := cli.CmdContext()

	log.Info("reindex-qdrant starting",
		zap.Bool("apply", deps.Apply),
		zap.Bool("dry_run", deps.DryRun || !deps.Apply),
		zap.Int("limit", deps.Limit),
		zap.String("target_collection", deps.TargetCollection),
		zap.String("qdrant_url", cfg.Qdrant.BaseURL),
	)

	dbSet, err := cli.OpenDatabaseSet(cfg, log)
	if err != nil {
		return fmt.Errorf("open database set: %w", err)
	}
	defer dbSet.Close()
	sqliteDB := dbSet.Primary

	// Build canonical Qdrant stack.
	schemaObj := qdrantschema.DefaultV3Schema()
	assetStore := indexing.NewSQLiteAssetStore(sqliteDB.DB)
	mapper := indexing.NewPayloadMapper(assetStore, log)
	client := transport.NewClient(&qdrantschema.Config{
		BaseURL: cfg.Qdrant.BaseURL,
		APIKey:  cfg.Qdrant.APIKey,
		Timeout: cfg.Qdrant.Timeout,
	}, log)
	writer := indexing.NewIndexWriter(client, schemaObj, mapper, log)
	collectionMgr := collections.NewCollectionManager(client, schemaObj, log)
	registryLedger, err := regsql.NewLedger(sqliteDB.DB)
	if err != nil {
		return fmt.Errorf("create media registry ledger: %w", err)
	}
	if err := collectionMgr.SetRegistryLedger(ctx, registryLedger); err != nil {
		return fmt.Errorf("hydrate media registry projection ledger: %w", err)
	}

	// Both dry-run and apply report the same immutable production target.
	// There is intentionally no target override or generated collection.
	targetCollection := platformschema.ProductionCollection

	// Wire the golden query executor against the canonical E5 sidecar so the
	// apply flow can certify deterministic top-K before the alias switch.
	golden := newGoldenQueryExecutor(
		client,
		search.NewTextEmbedderAdapter(embeddings.NewHTTPTextEmbedder(cfg.ClipIndexer.ServerURL)),
	)

	// Dispatch to the appropriate phase handler.
	if !deps.Apply {
		return dryRunQdrant(ctx, log, mapper, targetCollection, deps)
	}
	return applyQdrant(ctx, log, schemaObj, writer, collectionMgr, client, assetStore, sqliteDB.DB, deps, targetCollection, golden)
}
