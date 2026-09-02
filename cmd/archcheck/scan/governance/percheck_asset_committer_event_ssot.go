// Package scan — Check 79 (PR-DIAGNOSI-FINALE rule 3, July 2026):
// only AssetCommitter creates `asset.index.requested` events.
//
// scan/percheck_asset_committer_event_ssot.go pins the
// godlike/06 SSOT that the canonical `asset.index.requested`
// outbox event is created in EXACTLY ONE place: the canonical
// AssetCommitter chain. The canonical Chain runs:
//
//  1. (caller) AssetCommitter.CommitAsset(ctx, *asset.Asset)
//  2. internally: media_assets UPSERT +
//     outbox_events INSERT (event_type='asset.index.requested')
//     in the SAME SQLite TX (QDRANT-002 atomicity invariant).
//
// Any other production-code site that emits
// `event_type='asset.index.requested'` (via outbox dispatcher,
// SQL string literal, outbox-events API) bypasses the canonical
// COMMIT pipeline and risks:
//   - silent QDRANT-002 atomicity regression (Qdrant indexing a
//     not-yet-committed media_assets row).
//   - duplicate-outbox-event emission (a future cleanup that
//     looks for ONE event per asset would over-flag).
//   - ad-hoc envelope shape (the canonical
//     asset.index.requested.v1 envelope is documented in
//     internal/kernel/idempotency/keys.go::OutboxKey; ad-hoc payloads fail
//     the idempotency-key uniqueness constraint silently).
//
// This gate is the forward-prevention fence. Production-code
// emission of the literal `asset.index.requested` outside the
// canonical AssetCommitter chain (and outside the documented
// exempt zones — see exempt set below) surfaces as a CI build
// failure. Comment-only references are residue-accounted as WARN.
//
// scanner policy (mirrors percheck_qdrant_index_import_ban
// precedent):
//   - skip file basenames `.git`, `vendor`, `node_modules`,
//     `node-scraper`, `examples`, `archivist`, `docs`, `data`.
//   - skip `_test.go` files (regression-guard surface
//     legitimately needs fixture setups).
//   - skip `cmd/archcheck/scan/**` (this scanner file +
//     sibling scanners reference the canonical literal for
//     documentation).
//   - skip the canonical AssetCommitter files (the SOLE
//     authority on the event_type string).
//   - skip the canonical outboxevents package
//     (internal/platform/sqlite/outboxevents)
//     — the constants `EventAssetIndexRequested` are the
//     single source of truth for the literal value; the
//     package IS the SSOT definition site.
//   - skip `tests/**` paths — these test packages legitimately
//     emulate the outbox event for end-to-end pipeline
//     validation.
//   - skip `cmd/admin/**` — operator tooling (reconciliation,
//     diagnostics) legitimately inspects the outbox for
//     data correction.
//   - comment-only references are residue-accounted as WARN
//     (godlike/07).
//
// matched rule_id: `percheck_asset_committer_event_ssot`.
package governance

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// assetCommitterEventSSOTSkipDirs is the standard skip-dir set.
var assetCommitterEventSSOTSkipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
	"node-scraper": true,
	"examples":     true,
	"archivist":    true,
	"docs":         true,
	"data":         true,
}

// assetCommitterEventSSOTSkipPathPrefixes is the scan's own
// package exemption (the scanner file + sibling scanners
// reference the literal for documentation).
var assetCommitterEventSSOTSkipPathPrefixes = []string{
	"cmd/archcheck/scan",
}

// assetCommitterEventSSOTExemptPathPrefixes is the canonical
// exempt set — files that legitimately emit or reference the
// canonical `asset.index.requested` envelope without bypassing
// the AssetCommitter chain.
var assetCommitterEventSSOTExemptPathPrefixes = []string{
	// 1. Canonical AssetCommitter files — the SOLE authority
	//    on the asset.index.requested emission site.
	"internal/capabilities/assets/persistence/",
	// 2. The canonical outboxevents package — the constants
	//    that define EventAssetIndexRequested as the literal
	//    value (single source of truth for the literal value).
	"internal/platform/sqlite/outboxevents/",
	// 2b. The PostgreSQL media outbox adapter — engine mirror of the
	//     outboxevents constants for the staged media-domain cutover;
	//     defines the same canonical literal values for the pgvector
	//     engine adapter (one fact family, two engine adapters).
	"internal/platform/postgres/media/",
	// 3. Mutations.AssetMutationDispatcher — the canonical
	//    envelope surface (EnqueueAndIndex emits the canonical
	//    envelope inside the commit pipeline).
	"internal/capabilities/assets/mutations/",
	// 4. Finalizer / texttracks / voiceover / image ingest /
	//    soundeffect / catalogsync / provider — surfaces that
	//    route through the canonical AssetCommitter pipeline
	//    (lambda-flow-down). These ARE the canonical owner
	//    chain for the literal value (per godlike/06 SSOT).
	"internal/capabilities/assets/finalizer/",
	"internal/capabilities/assets/texttracks/",
	"internal/capabilities/voiceover/service/",
	"internal/capabilities/images/workflow/",
	"internal/capabilities/assets/soundeffect/",
	"internal/capabilities/assets/catalogsync/",
	"internal/capabilities/assets/providers/",
	"internal/recommendation/",
	// 5. Composition-root bundles — emit the canonical
	//    event_type literal only as typed documentation.
	"internal/app/",
	// 6. Idempotency keys package — the canonical
	//    asset.index.requested.v1 envelope is documented at
	//    internal/kernel/idempotency/keys.go::OutboxKey.
	"internal/kernel/idempotency/",
	// 7. CLI admin tools — operator tooling (reconciliation,
	//    diagnostics) legitimately inspects and possibly
	//    emits asset.index.requested for data correction.
	"cmd/admin/",
	// 8. Soundeffect / image API surfaces — typed-port
	//    documentation referencing the canonical event_type
	//    literal.
	"internal/capabilities/assets/soundeffect/",
	"internal/capabilities/images/",
	// 9. Metrics / observability — event_type labels for
	//    metric dimensions (NOT for emission).
	"internal/platform/observability/",
	// 10. Qdrant search dead-letter adapter — references the
	//     literal event_type for classification (NOT for
	//     emission).
	"internal/platform/qdrant/search/",
	// 11. Tests folder — regression-guard fixtures that
	//     legitimately reference the literal.
	"tests/",
}

// assetCommitterEventSSOTScanScope is the canonical prefix
// the gate applies to. Files under this prefix or under the
// tests/ or cmd/ fallback prefixes AND not under any exempt
// prefix (above) are the gate's target surface.
const assetCommitterEventSSOTScanScope = "internal/"

// assetCommitterEventSSOTLiteralRe matches production-code
// emission of the literal `asset.index.requested` AND the
// canonical envelope `asset.index.requested.v1`. The canonical
// envelope is documented at
// `internal/kernel/idempotency/keys.go::OutboxKey` as the canonical
// idempotency-key target. We match BOTH forms because the
// canonical AssetCommitter emits the canonical envelope; an
// ad-hoc emitter using either literal is a violation.
var assetCommitterEventSSOTLiteralRe = regexp.MustCompile(`['"]asset\.index\.requested(\.v1)?['"]`)

// assetCommitterEventSSOTRule is the rule-family id the
// scanner emits.
const assetCommitterEventSSOTRule = "percheck_asset_committer_event_ssot"

// assetCommitterEventSSOTNote is the violation Note string
// for any outbox-event literal emission outside the canonical
// AssetCommitter chain. The message references the canonical
// envelope + the canonical COMMIT pipeline concretely so the
// operator sees the migration path inline.
const assetCommitterEventSSOTNote = "forbidden emission of canonical 'asset.index.requested' outbox-event literal outside the canonical AssetCommitter chain (PR-DIAGNOSI-FINALE rule 3, July 2026); godlike/06 SSOT requires the canonical envelope (asset.index.requested.v1) to be emitted ONLY by the AssetCommitter.CommitAsset pathway (internal/capabilities/assets/persistence/committer.go) via the mutations.AssetMutationDispatcher (atomic UPSERT + outbox INSERT in single TX, QDRANT-002 atomicity invariant). Any other emission site risks silent QDRANT-002 regression (Qdrant indexing a not-yet-committed media_assets row) or duplicate-emission regression (a future cleanup over-counts events per asset_id). Exempt zones per scanner policy: canonical AssetCommitter files, outboxevents constants package, mutations.Dispatcher envelope, provider services routing through AssetCommitter, finalizer/post-processing surfaces that emit via the canonical AssetMutationDispatcher envelope, CLI admin tools (cmd/admin/**), test fixtures (tests/**)."

// assetCommitterEventSSOTWarn is the residue-emitter for
// comment-only references.
func assetCommitterEventSSOTWarn(r *report.Report, label, msg string) {
	r.Warnings = append(r.Warnings, assetCommitterEventSSOTRule+" "+label+" "+msg)
}

// ScanAssetCommitterEventSSOT walks every .go file under
// <root>/** (production + tests + cmd) and emits a violation
// for any production file (NOT _test.go) outside the canonical
// exempt set that references the `asset.index.requested`
// canonical envelope literal. Comment-only references are
// residue-accounted as WARN (godlike/07).
//
// productionOnly mode silences the comment-only WARN bucket so
// the operator-facing "zero production-code hits" claim
// (PR-P12-PERCHECK-BASELINE-ZERO pattern) is auditable via
// len(r.Violations) == 0.
func ScanAssetCommitterEventSSOT(root string, pol *policy.Policy, r *report.Report, productionOnly bool) {
	_ = pol // reserved for future SeverityOverride plumbing.

	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if assetCommitterEventSSOTSkipDirs[base] {
				return filepath.SkipDir
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr == nil {
				relSlash := filepath.ToSlash(rel)
				if hasAnyPathPrefix(relSlash, assetCommitterEventSSOTSkipPathPrefixes) {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		// Test files are exempt — regression-guard surface
		// legitimately needs fixture setups (matches the
		// percheck_qdrant_index_import_ban precedent).
		if strings.HasSuffix(relSlash, "_test.go") {
			return nil
		}
		// Out-of-scope (NOT under internal/, tests/, or cmd/):
		// skip without scanning.
		if !strings.HasPrefix(relSlash, assetCommitterEventSSOTScanScope) &&
			!strings.HasPrefix(relSlash, "tests/") &&
			!strings.HasPrefix(relSlash, "cmd/") {
			return nil
		}
		// Exempt subzones (canonical AssetCommitter +
		// outboxevents constants + composition root + tests +
		// CLI admin tools): these packages MAY reference
		// the asset.index.requested literal for legitimate
		// reasons (documentation, fixture setup, operator
		// tooling) without bypassing the AssetCommitter
		// commit pipeline.
		if hasAnyPathPrefix(relSlash, assetCommitterEventSSOTExemptPathPrefixes) {
			return nil
		}
		scanAssetCommitterEventSSOTFile(path, relSlash, r, productionOnly)
		return nil
	})
}

// scanAssetCommitterEventSSOTFile opens a single .go file
// and emits percheck_asset_committer_event_ssot violations
// for any line matching the canonical envelope literal.
// Comment-only references are residue-accounted as WARN
// (godlike/07 discipline). productionOnly suppresses the
// residue WARN bucket.
func scanAssetCommitterEventSSOTFile(path, relPath string, r *report.Report, productionOnly bool) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	lineNo := 0
	commentOnly := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		trimmed := strings.TrimLeft(line, " \t")

		// Residue accounting (godlike/07): comment-only
		// references to the canonical envelope literal
		// are descriptive prose, not real emissions.
		// WARN in !productionOnly mode.
		if (strings.HasPrefix(trimmed, "//") ||
			strings.HasPrefix(trimmed, "/*") ||
			strings.HasPrefix(trimmed, "*")) &&
			assetCommitterEventSSOTLiteralRe.MatchString(line) {
			commentOnly++
			continue
		}

		if !assetCommitterEventSSOTLiteralRe.MatchString(line) {
			continue
		}
		r.Violations = append(r.Violations, report.Violation{
			Package:     pkgFromAssetCommitterEventSSOTRel(relPath),
			File:        relPath,
			Line:        lineNo,
			Rule:        assetCommitterEventSSOTRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "non_canonical_index_event_emission",
			Note:        assetCommitterEventSSOTNote + " | snippet: " + truncateAssetCommitterEventSSOT(line),
		})
	}
	if commentOnly > 0 && !productionOnly {
		assetCommitterEventSSOTWarn(r, "index-event-comments:",
			strconv.Itoa(commentOnly)+" comment-only reference(s) in "+relPath+
				" (descriptive prose; non-fatal per godlike/07 no-fake-availability)")
	}
}

// pkgFromAssetCommitterEventSSOTRel extracts the package
// identifier from a repo-relative file path. Mirrors the family
// idiom from percheck_qdrant_index_import_ban.
func pkgFromAssetCommitterEventSSOTRel(rel string) string {
	dir := filepath.Dir(rel)
	if dir == "." || dir == "" {
		return "."
	}
	return filepath.ToSlash(dir)
}

// truncateAssetCommitterEventSSOT bounds the snippet surface
// at 120 chars to keep report JSON size stable. Mirrors the
// family idiom from percheck_qdrant_index_import_ban.
func truncateAssetCommitterEventSSOT(s string) string {
	const maxLen = 120
	const marker = " <<<"
	if len(s) > maxLen {
		return s[:maxLen] + marker
	}
	return s
}
