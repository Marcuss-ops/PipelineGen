// Package qdrant — QDRANT-003 + PR 12 (June 2026) reindex verification.
//
// VerifyReindex is the post-reindex validation gate. Replaces the
// previous buildSwitchReport placeholder (QDRANT-003) AND the lax
// sample-based gates (PR 12).
//
// PR 12 hardening — strict verifier (the user spec:
// "Verifier severo"):
//
//  1. ActualPoints == ExpectedPoints (strict equality, NOT >= — the
//     previous >= accepted over-counting, which masked failed
//     partial reindexes).
//  2. Full per-channel scan on EVERY scrolled page (was: first 2
//     pages = 1000-point sample only). Every channel-declared key
//     MUST be present on every point.
//  3. EVERY scroll page error is fatal — the previous
//     `break; partial data is better than nothing` is gone. The
//     verifier returns a partial report + non-nil err so the
//     caller (reindex_qdrant.go) refuses the alias switch.
//  4. maxScrolls page cap is BLOCKING (was a logged warning). A
//     collection larger than the cap cannot complete its scan
//     and the operator MUST raise the cap on the production
//     deployment — silently truncating was the original hazard.
//  5. pt.ID MUST equal schema.AssetIDToQdrantPointID(payload["asset_id"])
//     EXACTLY. The previous uuid.Parse(pt.ID) accepted ANY
//     UUID-form string, which silently lost the reverse-mapping
//     when the canonical asset_id was different from the
//     authored point ID (a write path this codebase no longer
//     has but legacy collections may carry).
//
// Cross-PR invariants the verifier keeps paying attention to:
//
//   - QDRANT-005 closure: the global embedding_version rescue
//     path was DELETED. Points missing per-channel keys ALWAYS
//     bump the per-channel counter; no legacy fallback path.
//   - PR 10 fail-closed vocabulary: CompleteScan bool, Report.Errors,
//     terminal-vs-noop terminology. schema.SwitchReport now exposes
//     CompleteScan + TotalScrolled so the operator can audit the
//     verifier's footprint the same way they audit the
//     reconciler's.
//
// Ready is true ONLY when all gates pass: counts match exactly,
// CompleteScan=true, zero missing, zero orphan, zero payload
// issues, zero per-channel mismatches, zero non-canonical point
// IDs, zero dead letters, golden-query and filter smokes pass,
// and no errors occurred during verification.
//
// P1 QDRANT-VERIFIER-SPLIT (July 2026): VerifyReindex is a thin
// orchestrator that delegates to 3 phase files:
//
//	verifier_counts.go  — verifyCounts (point parity + SQLite loading)
//	verifier_sample.go  — verifySample (scroll + canonical + versions + missing/orphan)
//	verifier_metadata.go — verifyMetadata (dead letters + smokes + Ready)
//
// Each phase has independent test coverage; the orchestrator runs them
// in strict sequence.
package verification

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/indexing"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
)

// ReindexVerifier holds the dependencies for post-reindex validation.
type ReindexVerifier struct {
	client        *transport.Client
	assetStore    indexing.AssetStore
	deadLetter    schema.DeadLetterChecker // nil = skip dead-letter check
	schema        *schema.IndexSchema      // canonically the schema under reindex; nil = skip per-channel version check
	log           *zap.Logger
	goldenQueries schema.GoldenQueryRunner // nil = skip golden-query gate (QDRANT-005)
}

// NewReindexVerifier creates a verifier. deadLetter may be nil (legacy
// admin CLIs). schema MAY be nil only for tests that exercise gates
// unrelated to per-channel embedding versioning; production wire paths
// (cmd/admin/reindex_qdrant.go, BuildOutboxBundle) MUST supply non-nil
// schema so the per-channel version check fires.
func NewReindexVerifier(client *transport.Client, assetStore indexing.AssetStore, deadLetter schema.DeadLetterChecker, schema *schema.IndexSchema, goldenQueries schema.GoldenQueryRunner, log *zap.Logger) *ReindexVerifier {
	return &ReindexVerifier{
		client:        client,
		assetStore:    assetStore,
		deadLetter:    deadLetter,
		schema:        schema,
		log:           log,
		goldenQueries: goldenQueries,
	}
}

// VerifyReindex runs the full validation suite against the target collection
// and returns a populated schema.SwitchReport with Ready set accordingly.
//
// expectedPoints is the count reported by ReindexAll (IndexedAssets).
//
// P1 QDRANT-VERIFIER-SPLIT (July 2026): the orchestrator delegates to
// 3 phase methods: verifyCounts → verifySample → verifyMetadata.
func (v *ReindexVerifier) VerifyReindex(ctx context.Context, targetCollection string, expectedPoints int) (*schema.SwitchReport, error) {
	report := &schema.SwitchReport{
		TargetCollection:          targetCollection,
		ExpectedPoints:            expectedPoints,
		CompleteScan:              false,
		GoldenQueriesOK:           false,
		FiltersOK:                 false,
		VersionMismatchPerChannel: make(map[string]int),
		NonCanonicalPointIDs:      nil,
	}

	// ── Phase 1: Counts + SQLite ID loading ─────────────────────
	sqliteSet, err := v.verifyCounts(ctx, targetCollection, expectedPoints, report)
	if err != nil {
		return report, err
	}

	// ── Phase 2: Scroll + canonical + missing/orphan ─────────────
	scrollAborted := v.verifySample(ctx, targetCollection, sqliteSet, report)

	// ── Phase 3: Metadata (dead letters + smokes + Ready) ────────
	v.verifyMetadata(ctx, targetCollection, report)

	if scrollAborted {
		return report, fmt.Errorf("PR 12: scroll aborted mid-scan (see report.Errors — alias switch BLOCKED)")
	}
	return report, nil
}
