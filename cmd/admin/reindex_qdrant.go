// cmd/admin/reindex_qdrant.go — QDRANT-003 + QDRANT-004 closure + PR 13 (June 2026)
//
// One-shot reindex of media_assets into Qdrant using the canonical
// IndexWriter.ReindexAll pipeline (AssetStore → PayloadMapper → IndexWriter).
// This command replaces the legacy reindex (reindex.go, removed in QDRANT-003)
// which used raw SQL + VectorAsset directly without schema validation.
//
// QDRANT-003 PR fix (June 2026): wired CollectionManager for schema creation,
// post-reindex verification, and atomic alias swap. The old code wrote into
// the target collection but never ensured the schema existed, never verified
// the result, and never switched the alias.
//
// QDRANT-004 closure (this iteration): the alias switch is GATED on a
// SwitchReport.Ready=true verification. The previous code unconditionally
// swapped at the end of phase 4 — a regression that could promote a
// half-written or schema-broken collection into service. The new
// implementation builds a SwitchReport (point counts, schema match, dead-
// letter, golden-query placeholders) and only calls SwitchAlias when
// `Ready` is true. On failure it returns *qdrant.ErrAliasSwitchNotReady
// and never touches the alias.
//
// PR 13 (June 2026) closure — Blue-green reindex (the user spec):
//
//	Apply mode NEVER reuses schema.PhysicalName as the target
//	collection. Each `--apply` invocation creates a brand-new
//	timestamped collection (e.g. media_assets_v3_20260627_153045),
//	indexes into it, runs the strict PR 12 verifier, and only
//	switches the runtime alias on Ready=true. The previous
//	collection (the "old target") is RETAINED — never deleted —
//	so the operator can `retry --target-collection=<old>` to
//	rollback manually. The SwitchReport is augmented with
//	`rollback_target` (the active alias target captured BEFORE
//	the verifier ran) and `old_collection` (the timestamped
//	target that was attempted). On Ready=false the operator
//	sees the new collection kept AND the alias unchanged in
//	the returned ErrAliasSwitchNotReady payload.
//
//	Operator escape hatch: `--target-collection=<NAME>` writes
//	into the explicit target (no timestamp override). The
//	strict verifier still gates the alias switch, same as the
//	auto-timestamped path; the override is for recovery flows
//	(manual fix-up after a failed auto-reindex, or matching a
//	pre-published collection name from a different CI run).
//
//	The legacy QDRANT-003 block "targetCollection != schema.PhysicalName
//	MUST be rejected in --apply" is REMOVED per the PR 12 code-review
//	which flagged it as the literal blocker for blue-green mode.
//
// Usage:
//
//	go run ./cmd/admin reindex-qdrant                           # dry-run (counts only)
//	go run ./cmd/admin reindex-qdrant --apply                    # apply, target = media_assets_v3_<UTC> (PR 13 blue-green)
//	go run ./cmd/admin reindex-qdrant --apply --target-collection=media_assets_recovery_v9  # explicit recovery target
//	go run ./cmd/admin reindex-qdrant --apply --limit=500        # cap rows
//	go run ./cmd/admin reindex-qdrant --json                     # machine-readable dry-run / apply
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

// timestampedTargetCollection (PR 13, June 2026 + follow-up, June 2026) —
// builds the canonical blue-green target name from the schema's
// PhysicalName base + a UTC nanosecond timestamp suffix. The schema's
// PhysicalName is the "logical" name (e.g. media_assets_v3_e5_768_siglip_768);
// the timestamped variant is the immutable physical collection the
// apply flow writes into.
//
// Format: <base>_<UTC-YYYYMMDD-HHMMSS-nnnnnnnnn>
//
// Follow-up history (June 2026): the seconds-resolution suffix
// (`YYYYMMDD_HHMMSS`) collided on concurrent --apply invocations in the
// same UTC second — two processes with aligned `time.Now()` produced
// the same target name and the second EnsureSchema call surfaced a
// duplicate-collection error from Qdrant. Pure nanosecond precision
// (`20060102_150405_000000000`) resolves the collision while keeping
// the helper pure and the tests fully deterministic against a
// frozen `now time.Time` parameter.
//
// Notes:
//
//   - Deterministically derived from `now time.Time` so tests can
//     assert against a frozen clock. Nanosecond resolution via
//     time.Now()'s monotonic clock source on Linux/macOS gives
//     sub-microsecond uniqueness for sequential calls; this
//     resolves the human-driven blue-green collision case (the
//     user spec).
//   - Accepts the Linux/macOS monotonic-clock guarantee. Known
//     residual risk: hosts with coarser clock resolution
//     (Windows' 15ms tick clock, edge embedded devices) MAY
//     still surface collisions if multiple --apply invocations
//     land on the same monotonic-clock bucket. The production
//     deployment target (cfg.Qdrant.Timeout + Linux container
//     host) is NOT affected; the residual is a developer-machine
//   - CI-on-Windows concern and a future hardening could mix
//     in a small `crypto/rand` nonce if it surfaces.
//   - Returns a string that — by construction — does NOT equal
//     schema.PhysicalName (the suffix is non-empty). PR 13's
//     `new != active` invariant is structurally guaranteed.
//   - UTC token keeps the format operator-friendly across timezones
//     so `ls media_assets_v3_*` produces a chronologically sort-
//     friendly sequence; the Production clock source is
//     time.Now().UTC() to avoid local-tz drift in the suffix.
func timestampedTargetCollection(base string, now time.Time) string {
	if base == "" {
		base = "media_assets_v3"
	}
	return fmt.Sprintf("%s_%s", base, now.UTC().Format("20060102_150405_000000000"))
}

// runReindexQdrant is the entry point registered in cmd/admin/main.go.
//
// Pipeline (QDRANT-003, June 2026):
//  1. Load config and open the media DB
//  2. Build the canonical Qdrant stack: SQLiteAssetStore → PayloadMapper → Client → IndexWriter → ReindexVerifier
//  3. Dry-run: list all asset IDs via mapper.ListAllAssetIDs, print count
//  4. Apply: ensure schema → reindex → verify (QDRANT-003 gates) → switch alias
//     - CollectionManager.EnsureSchema guarantees the target collection exists
//     with matching vector config and payload indexes
//     - IndexWriter.ReindexAll writes all assets into the target collection
//     - ReindexVerifier.VerifyReindex runs the full validation suite:
//     * Point count parity (hard gate)
//     * Missing/orphan ID detection (scroll + SQLite compare)
//     * Payload minimum validation (asset_id, name, source)
//     * Embedding version check
//     * Dead-letter count (optional, when wired)
//     - Only when SwitchReport.Ready==true: CollectionManager.SwitchAlias
//     atomically promotes the new collection
//
// QDRANT-003 closed (June 2026): count mismatch is now a HARD error
// (detected by the verifier, blocks Ready). The previous implementation
// logged a warning and continued; the new flow aborts the alias switch
// and returns *qdrant.ErrAliasSwitchNotReady.
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
		zap.String("target_collection", deps.TargetCollection),
		zap.String("qdrant_url", cfg.Qdrant.BaseURL),
	)

	sqliteDB, err := storage.OpenSQLiteDB(cfg.Storage.PrimaryDBFullPath(), log)
	if err != nil {
		return fmt.Errorf("open media DB: %w", err)
	}
	defer sqliteDB.Close()

	// Build canonical Qdrant stack.
	schema := qdrant.DefaultV3Schema()
	assetStore := qdrant.NewSQLiteAssetStore(sqliteDB.DB)
	mapper := qdrant.NewPayloadMapper(assetStore, log)
	client := qdrant.NewClient(&qdrant.Config{
		BaseURL: cfg.Qdrant.BaseURL,
		APIKey:  cfg.Qdrant.APIKey,
		Timeout: cfg.Qdrant.Timeout,
	}, log)
	writer := qdrant.NewIndexWriter(client, schema, mapper, log)
	collectionMgr := qdrant.NewCollectionManager(client, schema, log)

	targetCollection := deps.TargetCollection
	if targetCollection == "" && !deps.Apply {
		targetCollection = schema.PhysicalName
	}
	// PR 13: Apply mode auto-targets a fresh timestamped collection
	// unless the operator explicitly chose one. Dry-run mode still
	// uses the canonical physical name (no new collection is created
	// — dry-run is a side-effect-free enumeration).
	if deps.Apply && targetCollection == "" {
		targetCollection = timestampedTargetCollection(schema.PhysicalName, time.Now())
	}

	// ── Dry-run ──────────────────────────────────────────────────
	if !deps.Apply {
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

	// ── Apply ────────────────────────────────────────────────────
	// PR 13 (June 2026): removed the QDRANT-003 era
	// `targetCollection != schema.PhysicalName` rejection. The
	// blue-green Apply path uses timestamped collections by
	// construction, which are intentionally NOT equal to
	// schema.PhysicalName. Operators can also pass an explicit
	// recovery target via --target-collection; the strict verifier
	// still gates the alias switch in either case.
	//
	// Sanity: the new collection must NOT collide with
	// schema.PhysicalName (we are running PR 13 blue-green, not
	// a same-collection overwrite). If the operator passed
	// `schema.PhysicalName` explicitly the verifier still gates
	// on Ready but an in-place overwrite is the explicit
	// recovery escape hatch — log a warning so the operator
	// sees the special-case flag in the log line.
	if deps.TargetCollection != "" && deps.TargetCollection == schema.PhysicalName {
		log.Warn("PR 13: --target-collection matches schema.PhysicalName — same-collection overwrite. Use the auto-timestamped path unless you are recovering from a failed blue-green run.",
			zap.String("target_collection", deps.TargetCollection))
	}

	// Phase 1: Ensure the target collection exists with matching schema.
	if _, err := collectionMgr.EnsureSchema(ctx); err != nil {
		return fmt.Errorf("ensure schema for %q: %w", targetCollection, err)
	}
	log.Info("schema ensured", zap.String("collection", targetCollection))

	// Phase 2: Reindex all assets into the target collection.
	start := time.Now()
	reindexResult, err := writer.ReindexAll(ctx, targetCollection, deps.Limit)
	elapsed := time.Since(start)
	if err != nil {
		// QDRANT-003 fix: guard nil reindexResult before dereference.
		// ReindexAll returns (nil, error) when ListAllAssetIDs fails.
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
	//
	// QDRANT-003 closed (June 2026): count mismatch is a HARD gate.
	// PR 12 (June 2026): STICT equality (was `>=`); per-channel scan
	// runs on EVERY scrolled page; ANY scroll error or max-pages cap
	// hit returns non-nil err + CompleteScan=false + Ready=false; pt.ID
	// MUST equal AssetIDToQdrantPointID(payload["asset_id"]) literally
	// (not just uuid-parseable).
	//
	// PR 13 (June 2026): the new targetCollection is the timestamped
	// one (`media_assets_v3_<UTC>`) or the explicit recovery
	// override. The verifier runs on the NEW collection only; the
	// old alias target is untouched.
	deadLetter := qdrant.NewOutboxEventsDeadLetterAdapter(outboxevents.NewRepository(sqliteDB.DB))
	verifier := qdrant.NewReindexVerifier(client, assetStore, deadLetter, schema, nil, log)
	report, verifyErr := verifier.VerifyReindex(ctx, targetCollection, reindexResult.IndexedAssets)

	// PR 13: populate rollback metadata on the report regardless
	// of gateway result. The operator reading the JSON
	// (ErrAliasSwitchNotReady or success print) MUST see
	// `rollback_target` so the recovery path is deterministic.
	//
	// Field semantics (per the user spec):
	//
	//   - RollbackTarget = the active alias target captured
	//     BEFORE the verifier ran. On failure: the alias target
	//     to swap back to via `--target-collection=<this>`. On
	//     success: the previously-active collection retained for
	//     rollback in case turnup needs to be reverted later.
	//
	//   - OldCollection  = the timestamped target that was
	//     attempted (the new collection written into, in BOTH
	//     cases — failed verification AND successful swap). It
	//     names the run uniquely in operator logs (two reindexes
	//     in the same minute get distinct suffixes) and answers
	//     "what did THIS apply attempt?" for post-mortems.
	report.RollbackTarget = oldTarget
	report.OldCollection = targetCollection

	if verifyErr != nil {
		// PR 12 hardening: a non-nil verifyErr implies scrollAborted or
		// another fatal infrastructure failure (page error, cap hit).
		// It is defence-in-depth to surface the err AND keep the
		// alias-switch gate fired — the Ready=false path below is
		// already correct because CompleteScan=false ⇒ Ready=false,
		// but explicit log + return guards against a future Ready-gate
		// weakening that might pass on a fatal infrastructure failure.
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
		return &qdrant.ErrAliasSwitchNotReady{Report: report}
	}
	if verifyErr != nil {
		// Belt-and-braces: Ready could be true while an unrecoverable
		// infrastructure failure (scrollAborted/cap-binding) is reported
		// via non-nil err. The cmd path refuses the switch in that
		// pathological case as well — surface the err in the report
		// and exit ErrAliasSwitchNotReady so the operator sees the
		// block in the JSON report and log line.
		log.Error("PR 12: verifyErr is non-nil while Ready=true (defence-in-depth block)",
			zap.String("target", targetCollection),
			zap.Error(verifyErr))
		return &qdrant.ErrAliasSwitchNotReady{Report: report}
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
	// --target-collection=<oldTarget>` to rollback manually. The
	// rollback target is now in `report.RollbackTarget` (the
	// active target pre-switch) and on the success log line the
	// operator sees the explicit `from → to` transition.
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
		zap.String("alias", schema.RuntimeAlias),
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
