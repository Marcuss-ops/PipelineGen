// cmd/admin/reindex_qdrant.go — QDRANT-007 TODO 11 blue-green reindex
// (June 2026). One-shot reindex of media_assets into a fresh timestamped
// Qdrant collection with verifier-gated atomic alias swap.
//
// The blue-green pattern (QDRANT-007, June 2026) replaces the
// PhysicalName-targeted write of QDRANT-003 / QDRANT-004. Under the
// prior pattern, every admin reindex run wrote into the same
// stable collection name; the live alias pointed at that
// collection. The pre-blue-green failure mode was reading a
// half-written collection. The blue-green pattern guarantees:
//
//  1. Each reindex run creates a fresh timestamped collection
//     (`media_assets_<version>_<UTC timestamp>` — see
//     qdrant.GenerateTimestampName). The active (aliased)
//     collection is NEVER written into.
//  2. The active collection is preserved until the new collection
//     passes post-reindex verification (SwitchReport.Ready).
//  3. SwitchAlias is atomic (delete+create in one UpdateAliases
//     call) and happens AFTER verification only — the prior
//     unconditional swap is removed.
//  4. The pre-switch alias target is preserved as the rollback
//     handle so operators can return to the known-good collection
//     IF verification succeeds (the spec deliberately polls the
//     rollback on success too — "rollback_target valorizzato
//     anche su success").
//
// Usage:
//
//	go run ./cmd/admin reindex-qdrant                            # dry-run (counts only)
//	go run ./cmd/admin reindex-qdrant --apply                     # blue-green reindex + verifier-gated alias swap
//	go run ./cmd/admin reindex-qdrant --apply --limit=500         # cap rows
//	go run ./cmd/admin reindex-qdrant --apply --json              # machine-readable JSON: BlueGreenReport
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
)

// reindexQdrantDeps holds the parsed flags for runReindexQdrant.
type reindexQdrantDeps struct {
	Apply  bool
	JSON   bool
	DryRun bool
	Limit  int
	// TargetCollection is left here for backward-compat with pre-QDRANT-007
	// admin scripts; in --apply mode it is rejected outright (the blue-green
	// pattern requires a fresh timestamped name every run, never an
	// --target-collection override). In --dry-run mode it remains useful
	// for "preview what would happen if I reindexed into <X>".
	TargetCollection string
}

// parseReindexQdrantArgs parses CLI args into reindexQdrantDeps.
// Flags:
//
//	--apply              actually run the blue-green flow (default: dry-run)
//	--dry-run            explicit dry-run (default when --apply is omitted)
//	--json               emit BlueGreenReport JSON on success / failure
//	--limit=N            cap number of assets
//	--target-collection=X  only honoured in dry-run mode; rejected in apply
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
			n, err := parsePositiveFlag(a, "--limit")
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

// runReindexQdrant is the entry point registered in cmd/admin/main.go.
//
// QDRANT-007 TODO 11 pipeline (blue-green, June 2026):
//
//	Phase 0 — Snapshot the active alias target BEFORE any side-effect.
//	          This is `rollback_target` for the rest of the run; it is
//	          surfaced in the JSON report even on the success path so
//	          operators have a known-good handle to return to.
//
//	Phase 1 — Generate a unique timestamped name from `schema.Version`
//	          (NOT `schema.PhysicalName`) and ensure uniqueness via
//	          one-retry-or-ErrTimestampCollision.
//	          The new collection is the ONLY write target.
//
//	Phase 2 — CreateCollection on the new name (qdrant.CollectionManager).
//	          The active (aliased) collection is not touched.
//
//	Phase 3 — ReindexAll into the new collection (qdrant.IndexWriter).
//
//	Phase 4 — VerifyReindex on the new collection (qdrant.ReindexVerifier
//	          — SwitchReport gates Ready). If !Ready ⇒ AliasSwapped=false,
//	          JSON report emitted (when --json), error returned
//	          (ErrAliasSwitchNotReady carrying the SwitchReport) so the
//	          caller can surface the verification failure.
//
//	Phase 5 — SwitchAlias atomically (CollectionManager) only when
//	          Ready=true. AliasSwapped=true in the JSON report.
//
//	Phase 6 — Emit BlueGreenReport JSON (when --json) or human-readable
//	          summary (default). Exit code: 0 when AliasSwapped=true,
//	          non-zero when AliasSwapped=false (verifier blocked swap).
func runReindexQdrant(args []string) error {
	cfg, log, cleanup, err := appLogger()
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

	ctx := cmdContext()

	log.Info("reindex-qdrant starting",
		zap.Bool("apply", deps.Apply),
		zap.Bool("dry_run", deps.DryRun || !deps.Apply),
		zap.Int("limit", deps.Limit),
		zap.String("qdrant_url", cfg.Qdrant.BaseURL),
	)

	sqliteDB, err := storage.OpenSQLiteDB(cfg.Storage.PrimaryDBFullPath(), log)
	if err != nil {
		return fmt.Errorf("open media DB: %w", err)
	}
	defer sqliteDB.Close()

	// Build canonical Qdrant stack.
	// QDRANT-007 (TODO 11): Config now carries APIKey + BaseURL + Timeout
	// (the APIKey wire-on-every-request was wired in QDRANT-003 close-out
	// at internal/infrastructure/qdrant/client.go::doRequest). Constructing
	// the Client via NewClient(&qdrant.Config{BaseURL, APIKey, Timeout})
	// is the canonical wiring shape.
	schema := qdrant.DefaultV3Schema()
	assetStore := qdrant.NewSQLiteAssetStore(sqliteDB.DB)
	mapper := qdrant.NewPayloadMapper(assetStore, log)
	client := qdrant.NewClient(&qdrant.Config{
		BaseURL: cfg.Qdrant.BaseURL,
		Timeout: cfg.Qdrant.Timeout,
		APIKey:  cfg.Qdrant.APIKey,
	}, log)
	writer := qdrant.NewIndexWriter(client, schema, mapper, log)
	collectionMgr := qdrant.NewCollectionManager(client, schema, log)

	// Dry-run
	if !deps.Apply {
		assetIDs, err := mapper.ListAllAssetIDs(ctx)
		if err != nil {
			return fmt.Errorf("list assets for dry-run: %w", err)
		}
		if deps.Limit > 0 && len(assetIDs) > deps.Limit {
			assetIDs = assetIDs[:deps.Limit]
		}

		// In dry-run the report's old_collection / new_collection are
		// preview-only: old_collection is the live alias target and
		// new_collection is what GenerateTimestampName would emit
		// without committing it. alias_swapped is always false in
		// dry-run (no write happened). verifier_passed is false by
		// convention even when the dry-run "would" pass — the result
		// is just a preview.
		oldCollection, _ := collectionMgr.GetActiveCollection(ctx)
		previewName := qdrant.GenerateTimestampName(schema.Version)
		report := qdrant.BlueGreenReport{
			OldCollection:  oldCollection,
			NewCollection:  previewName,
			AliasSwapped:   false,
			RollbackTarget: oldCollection,
			VerifierPassed: false,
		}

		if deps.JSON {
			b, _ := json.Marshal(report)
			fmt.Println(string(b))
			return nil
		}

		log.Info("DRY-RUN complete (no Qdrant writes)",
			zap.Int("total_assets", len(assetIDs)),
			zap.String("active_collection", oldCollection),
			zap.String("preview_new_collection", previewName),
		)
		fmt.Printf("Dry-run: %d assets would be reindexed.\n", len(assetIDs))
		fmt.Printf("  active_collection:    %q\n", oldCollection)
		fmt.Printf("  preview_new_collection: %q\n", previewName)
		fmt.Println("Re-run with --apply to execute.")
		return nil
	}

	// Apply path
	// QDRANT-007 (TODO 11): --target-collection is REJECTED in apply
	// mode (QDRANT-007 invariant: a stable override would re-introduce
	// the pre-blue-green failure mode of writing into the active
	// collection). Remediation recipe is included inline so incident
	// responders do not have to round-trip to docs.
	if deps.TargetCollection != "" {
		return errors.New(
			"--target-collection is not allowed in --apply mode. " +
				"QDRANT-007 TODO 11 blue-green allocates a fresh timestamped " +
				"collection per run, so a fixed-override write would land in the " +
				"active collection. To preview the would-be name: run --dry-run " +
				"--json first (new_collection field); then re-run --apply --json " +
				"without --target-collection to execute.",
		)
	}

	// Phase 0 — snapshot the active alias as the rollback handle.
	// Spec test #5: rollback_target must be populated even on success;
	// capturing pre-side-effect means a partial-failure also leaves a
	// usable handle for the operator's forensics.
	//
	// PARALLEL-RUN CAVEAT: another operator swapping the alias between
	// Phase 0 and Phase 5 makes OldCollection stale; SwitchAlias then
	// fails noisily (Qdrant enforces delete_alias from current target).
	// Canonical QDRANT-007 invariant: one reindex per alias at a time.
	oldCollection, err := collectionMgr.GetActiveCollection(ctx)
	if err != nil {
		// A missing alias (first-ever reindex) is NOT a failure — the
		// server has nothing to roll back to. Log and continue with
		// rollback_target="" so the report reflects truth.
		log.Warn("could not resolve active alias at start of run; "+
			"rollback_target will be empty",
			zap.String("alias", schema.RuntimeAlias),
			zap.Error(err))
		oldCollection = ""
	}
	report := qdrant.BlueGreenReport{
		OldCollection:  oldCollection,
		RollbackTarget: oldCollection,
	}
	log.Info("phase 0: rollback target captured",
		zap.String("alias", schema.RuntimeAlias),
		zap.String("old_collection", oldCollection))

	// Phase 1 — Generate + ensure-unique name (one-retry-or-err).
	proposedName := qdrant.GenerateTimestampName(schema.Version)
	newCollection, err := collectionMgr.EnsureUniqueName(ctx, proposedName)
	if err != nil {
		if errors.Is(err, qdrant.ErrTimestampCollision) {
			// Spec test #6: collision path → regenerate OR fail
			// explicitly. We failed (regenerated name was also taken).
			// Surface as a JSON-safe partial report; the operator sees
			// `new_collection` populated with the colliding regen name
			// so they can clean it up.
			report.NewCollection = newCollection
			report.AliasSwapped = false
			report.VerifierPassed = false
			emitBlueGreenReport(deps.JSON, &report)
			return fmt.Errorf(
				"phase 1: timestamp collision on both proposed %q and regenerated %q: %w",
				proposedName, newCollection, err)
		}
		return fmt.Errorf("phase 1: ensure unique name on %q: %w", proposedName, err)
	}
	report.NewCollection = newCollection
	log.Info("phase 1: new collection name assigned",
		zap.String("proposed", proposedName),
		zap.String("final", newCollection))

	// Phase 2 — Create the timestamped collection (no writes to active).
	if err := collectionMgr.CreateCollection(ctx, newCollection); err != nil {
		return fmt.Errorf("phase 2: create reindex collection %q: %w", newCollection, err)
	}
	log.Info("phase 2: reindex collection created", zap.String("collection", newCollection))

	// Phase 3 — Reindex all assets into the new collection.
	start := time.Now()
	reindexResult, err := writer.ReindexAll(ctx, newCollection, deps.Limit)
	elapsed := time.Since(start)
	if err != nil {
		// Guard nil reindexResult before dereference (ReindexAll returns
		// (nil, error) when ListAllAssetIDs fails).
		indexed, failed := 0, 0
		if reindexResult != nil {
			indexed = reindexResult.IndexedAssets
			failed = reindexResult.FailedAssets
		}
		// Phase 3 did not complete. The active collection is untouched.
		// The new collection was created but is half-populated at best.
		// Surface partial state in the JSON; operator can drop
		// `new_collection` manually if they choose to abandon the run.
		log.Error("phase 3: reindex failed; active collection untouched",
			zap.String("new_collection", newCollection),
			zap.Int("indexed", indexed),
			zap.Int("failed", failed),
			zap.Error(err))
		emitBlueGreenReport(deps.JSON, &report)
		return fmt.Errorf("phase 3: reindex failed after %d indexed / %d failed: %w",
			indexed, failed, err)
	}
	log.Info("phase 3: reindex complete", zap.Int("indexed", reindexResult.IndexedAssets))

	// Phase 4 — Post-reindex verification on the new collection.
	deadLetter := qdrant.NewOutboxEventsDeadLetterAdapter(outboxevents.NewRepository(sqliteDB.DB))
	goldenRunner := qdrant.NewDefaultGoldenQueryRunner(client, schema, log)
	verifier := qdrant.NewReindexVerifier(client, assetStore, deadLetter, schema, goldenRunner, log)
	switchReport, verifyErr := verifier.VerifyReindex(ctx, newCollection, reindexResult.IndexedAssets)
	if verifyErr != nil {
		// Verification infrastructure failure (count / scroll / SQLite
		// failed entirely). The report is still populated with whatever
		// was gathered; Ready stays false.
		log.Error("phase 4: verification infrastructure failure",
			zap.String("new_collection", newCollection), zap.Error(verifyErr))
	}
	if switchReport != nil {
		report.VerifierPassed = switchReport.Ready
	}

	// Spec test #3 + #4: alias NEVER changes if verifier says no.
	// Spec test #2: active collection NOT modified during reindex —
	// we never wrote into it. The verifier's count check is on the
	// NEW collection; the active collection's point count is
	// unchanged from before the run.
	//
	// report.AliasSwapped stays at its zero-value (false) here; the
	// only positive-state site is Phase 5, where the Ready gate
	// passes and SwitchAlias succeeds.
	if !report.VerifierPassed {
		log.Error("phase 4: alias switch BLOCKED by SwitchReport.Ready=false "+
			"(alias NOT swapped, old collection still active)",
			zap.String("new_collection", report.NewCollection),
			zap.String("old_collection", report.OldCollection),
			zap.Bool("verifier_passed", report.VerifierPassed))
		emitBlueGreenReport(deps.JSON, &report)
		if switchReport != nil {
			return &qdrant.ErrAliasSwitchNotReady{Report: switchReport}
		}
		return errors.New("phase 4: verifier did not return a Ready=true report")
	}
	log.Info("phase 4: switch gate PASSED (Ready=true)",
		zap.String("new_collection", newCollection),
		zap.Int("points", switchReport.ActualPoints),
		zap.Int("missing", switchReport.MissingCount),
		zap.Int("orphan", switchReport.OrphanCount))

	// Phase 5 — Atomic alias switch. Allowed only because Ready=true.
	//
	// SwitchAlias empty-oldTarget semantics: when `report.OldCollection
	// == ""` (first-ever reindex on a fresh alias), Qdrant's atomic
	// switch emits `delete_alias: ""` (Qdrant treats this as a no-op)
	// then `create_alias: newCollection`. So no special-casing is
	// needed for the very-first blue-green run.
	//
	// Stale-oldTarget possibility: a parallel swap between Phase 0 and
	// Phase 5 would make OldCollection stale; SwitchAlias then fails.
	// The returned error message below flags the root-cause hypothesis
	// (stale-oldTarget or concurrent-swap) so the operator does not
	// need to read the QDRANT-007 invariant comment to understand.
	if err := collectionMgr.SwitchAlias(ctx, report.OldCollection, newCollection); err != nil {
		// The old collection is STILL active (SwitchAlias failed); the
		// new collection is created + populated + verified but not
		// promoted. Operators can retry SwitchAlias or drop the new
		// collection. report.AliasSwapped stays at its zero-value
		// (false) — the only positive-state site is below, after
		// SwitchAlias returns nil.
		log.Error("phase 5: alias switch failed; old collection STILL active",
			zap.String("old", report.OldCollection),
			zap.String("new", newCollection),
			zap.Error(err))
		emitBlueGreenReport(deps.JSON, &report)
		return fmt.Errorf(
			"phase 5: switch alias from %q to %q (likely stale-oldTarget or "+
				"concurrent-swap; QDRANT-007 forbids concurrent reindexes for "+
				"the same alias): %w", report.OldCollection, newCollection, err)
	}
	report.AliasSwapped = true
	log.Info("phase 5: alias switched (new collection promoted)",
		zap.String("alias", schema.RuntimeAlias),
		zap.String("old", report.OldCollection),
		zap.String("new", newCollection),
		zap.Duration("elapsed", elapsed))

	// Phase 6 — Emit BlueGreenReport.
	emitBlueGreenReport(deps.JSON, &report)

	if !deps.JSON {
		fmt.Printf("Blue-green reindex complete.\n")
		fmt.Printf("  old_collection:   %q\n", report.OldCollection)
		fmt.Printf("  new_collection:   %q\n", report.NewCollection)
		fmt.Printf("  alias_swapped:    %v\n", report.AliasSwapped)
		fmt.Printf("  rollback_target:  %q\n", report.RollbackTarget)
		fmt.Printf("  verifier_passed:  %v\n", report.VerifierPassed)
		fmt.Printf("  points:           %d\n", reindexResult.IndexedAssets)
		fmt.Printf("  elapsed:          %s\n", elapsed.Round(time.Millisecond))
	}
	return nil
}

// emitBlueGreenReport writes the report to stdout iff `--json` was passed.
// In human-readable mode it stays silent — main()'s call site provides
// the operator-facing summary directly via fmt.Printf. Centralising the
// JSON emission here means the report-shape contract is enforced by a
// single function: any future addition to BlueGreenReport's JSON tags
// automatically flows through every emit point.
func emitBlueGreenReport(asJSON bool, report *qdrant.BlueGreenReport) {
	if !asJSON || report == nil {
		return
	}
	b, _ := json.Marshal(report)
	fmt.Println(string(b))
}
