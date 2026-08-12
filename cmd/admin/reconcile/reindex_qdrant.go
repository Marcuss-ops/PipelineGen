// cmd/admin/reindex_qdrant.go — QDRANT-003 + QDRANT-004 closure + PR 13 (June 2026)
//
// One-shot reindex of media_assets into Qdrant using the canonical
// IndexWriter.ReindexAll pipeline (AssetStore → PayloadMapper → IndexWriter).
// This command replaces the legacy reindex (reindex.go, removed in QDRANT-003)
// which used raw SQL + VectorAsset directly without schema validation.
//
// 3-file split layout (LONG-FILES-DECOMPOSITION-V2-2026-07-06 P3 BASSA, July 2026):
//
//   - reindex_qdrant.go          (this file, slim) — package doc + reindexQdrantDeps + parseReindexQdrantArgs + timestampedTargetCollection + RunReindexQdrant (thin dispatch)
//   - reindex_qdrant_dryrun.go   (sibling)         — dryRunQdrant helper (the side-effect-free enumeration path)
//   - reindex_qdrant_apply.go    (sibling)         — applyQdrant helper (the 4-phase apply path)
//
// QDRANT-003 PR fix (June 2026): wired CollectionManager for schema creation,
// post-reindex verification, and atomic alias swap. The old code wrote into
// the target collection but never ensured the schema existed, never verified
// the result, and never switched the alias.
//
// QDRANT-004 closure: the alias switch is GATED on a SwitchReport.Ready=true
// verification. On failure it returns *transport.ErrAliasSwitchNotReady
// and never touches the alias.
//
// PR 13 (June 2026) closure — Blue-green reindex:
//
//	Apply mode NEVER reuses schema.PhysicalName as the target
//	collection. Each `--apply` invocation creates a brand-new
//	timestamped collection, indexes into it, runs the strict PR 12
//	verifier, and only switches the runtime alias on Ready=true.
//	The previous collection is RETAINED — never deleted — so the
//	operator can `retry --target-collection=<old>` to rollback
//	manually. Operator escape hatch: `--target-collection=<NAME>`
//	writes into the explicit target (no timestamp override).
//
// Usage:
//
//	go run ./cmd/admin reindex-qdrant                           # dry-run (counts only)
//	go run ./cmd/admin reindex-qdrant --apply                    # apply, target = media_assets_v3_<UTC> (PR 13 blue-green)
//	go run ./cmd/admin reindex-qdrant --apply --target-collection=media_assets_recovery_v9  # explicit recovery target
//	go run ./cmd/admin reindex-qdrant --apply --limit=500        # cap rows
//	go run ./cmd/admin reindex-qdrant --json                     # machine-readable dry-run / apply
package reconcile

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"go.uber.org/zap"

	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/indexing"
	qdrantschema "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/transport"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/collections"
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
//	--limit=N            cap number of assets
//	--target-collection=X  override target collection name
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
			deps.TargetCollection = strings.TrimPrefix(a, "--target-collection=")
		default:
			if strings.HasPrefix(a, "-") {
				return deps, fmt.Errorf("unknown flag: %s", a)
			}
		}
	}
	if deps.Apply && deps.DryRun {
		return deps, fmt.Errorf("--apply and --dry-run are mutually exclusive")
	}
	return deps, nil
}

// timestampedTargetCollection (PR 13, June 2026 + follow-up, June 2026) —
// builds the canonical blue-green target name from the schema's
// PhysicalName base + a UTC nanosecond timestamp suffix. The schema's
// PhysicalName is the "logical" name; the timestamped variant is the
// immutable physical collection the apply flow writes into.
//
// Format: <base>_<UTC-YYYYMMDD-HHMMSS-nnnnnnnnn>
//
// Deterministically derived from `now time.Time` so tests can
// assert against a frozen clock. Nanosecond resolution via
// time.Now()'s monotonic clock source on Linux/macOS gives
// sub-microsecond uniqueness for sequential calls; this
// resolves the human-driven blue-green collision case (the
// user spec).
//
// Returns a string that — by construction — does NOT equal
// schema.PhysicalName (the suffix is non-empty). PR 13's
// `new != active` invariant is structurally guaranteed.
func timestampedTargetCollection(base string, now time.Time) string {
	if base == "" {
		base = "media_assets_v3"
	}
	utc := now.UTC()
	return fmt.Sprintf("%s_%s_%09d", base, utc.Format("20060102_150405"), utc.Nanosecond())
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

	sqliteDB, err := storage.OpenSQLiteDB(cfg.Storage.PrimaryDBFullPath(), log)
	if err != nil {
		return fmt.Errorf("open media DB: %w", err)
	}
	defer sqliteDB.Close()

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

	// Determine the target collection.
	targetCollection := deps.TargetCollection
	if targetCollection == "" && !deps.Apply {
		targetCollection = schemaObj.PhysicalName
	}
	// PR 13: Apply mode auto-targets a fresh timestamped collection
	// unless the operator explicitly chose one. Dry-run mode still
	// uses the canonical physical name (no new collection is created
	// — dry-run is a side-effect-free enumeration).
	if deps.Apply && targetCollection == "" {
		targetCollection = timestampedTargetCollection(schemaObj.PhysicalName, time.Now())
	}

	// Sanity warning for the operator on same-collection overwrite.
	// Apply-only path (matches pre-PR behavior; the warning is about
	// the apply path's same-collection overwrite hazard, not dry-run).
	if deps.Apply && deps.TargetCollection != "" && deps.TargetCollection == schemaObj.PhysicalName {
		log.Warn("PR 13: --target-collection matches schema.PhysicalName — same-collection overwrite. Use the auto-timestamped path unless you are recovering from a failed blue-green run.",
			zap.String("target_collection", deps.TargetCollection))
	}

	// Dispatch to the appropriate phase handler.
	if !deps.Apply {
		return dryRunQdrant(ctx, log, mapper, targetCollection, deps)
	}
	return applyQdrant(ctx, log, schemaObj, writer, collectionMgr, client, assetStore, sqliteDB.DB, deps, targetCollection)
}
