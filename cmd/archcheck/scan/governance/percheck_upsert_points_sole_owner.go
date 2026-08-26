// Package scan — Check 80 (PR-DIAGNOSI-FINALE rule 4, July 2026):
// only the Outbox IndexingHandler calls qdrant.UpsertPoints.
//
// scan/percheck_upsert_points_sole_owner.go pins the
// godlike/06 SSOT that the sole production caller of
// `transport.Client.UpsertPoints(` is
// `internal/platform/qdrant/indexing/` (the IndexingHandler
// outbox consumer). The canonical apply-path is:
//
//	(caller) asset.index.requested asset.index.delete_requested outbox
//	                 |
//	                 v
//	IndexingHandler (outbox worker)
//	  -> qdrant.IndexClip / qdrant.DeletePointsByFilter
//	     -> (internally) client.UpsertPoints(
//
// Any other production-code site that calls
// `client.UpsertPoints(` directly bypasses the outbox pipeline
// and risks:
//   - silent outbox-bypass (a Qdrant projection may reflect a
//     point that has no durable outbox record — the operator
//     cannot re-derive it on rebuild).
//   - silent at-least-once vs at-most-once ambiguity (outbox
//     guarantees at-least-once delivery with idempotency-key
//     dedup; direct callers don't).
//   - ad-hoc envelope shape (the canonical envelope travels
//     via the typed outbox payload; ad-hoc payloads fail
//     Qdrant schema validation silently on first deploy).
//
// This gate is the forward-prevention fence. Production-code
// emission of `\.UpsertPoints\(` outside the canonical
// `internal/platform/qdrant/indexing/` caller surface
// surfaces as a CI build failure. Comment-only references are
// residue-accounted as WARN.
//
// The transport-package function definition itself
// (`internal/platform/qdrant/transport/client_points.go`;
// the literal `func (c *Client) UpsertPoints(`) is the
// declaration line, NOT a call site — the regex `\.UpsertPoints\(
// requires a dot-receiver before the function name, so the
// declaration line is naturally exempt.
//
// scanner policy (mirrors percheck_qdrant_index_import_ban
// precedent):
//   - skip file basenames `.git`, `vendor`, `node_modules`,
//     `node-scraper`, `examples`, `archivist`, `docs`, `data`.
//   - skip `_test.go` files (regression-guard surface
//     legitimately needs fixture UpsertPoints calls; residue
//     documented in docs/migrations/archcheck-strict-baseline.json).
//   - skip `cmd/archcheck/scan/**` (this scanner file +
//     sibling scanners reference the canonical pattern for
//     documentation).
//   - skip the canonical IndexingHandler caller surface
//     `internal/platform/qdrant/indexing/`.
//   - comment-only references are residue-accounted as WARN
//     (godlike/07).
//
// matched rule_id: `percheck_upsert_points_sole_owner`.
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

// upsertPointsSoleOwnerSkipDirs is the standard skip-dir set.
var upsertPointsSoleOwnerSkipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
	"node-scraper": true,
	"examples":     true,
	"archivist":    true,
	"docs":         true,
	"data":         true,
}

// upsertPointsSoleOwnerSkipPathPrefixes is the scan's own
// package exemption.
var upsertPointsSoleOwnerSkipPathPrefixes = []string{
	"cmd/archcheck/scan",
}

// upsertPointsSoleOwnerCanonicalCallers is the EXEMPT set:
// the canonical surfaces where direct `\.UpsertPoints\(` calls
// are legitimate. The IndexingHandler routes asset index events
// through `internal/platform/qdrant/indexing/`; the
// qdrantmm package (mediamemory concept/frame indexers) is the
// SOLE additional owner for concept/frame vector writes. The
// generic ProjectionWriter adapter (TransportProjectionWriter)
// moved to `internal/platform/qdrant/indexing/` and remains the
// canonical translation layer for those calls.
var upsertPointsSoleOwnerCanonicalCallers = []string{
	"internal/platform/qdrant/indexing/",
	"internal/platform/qdrant/qdrantmm/",
	"internal/platform/qdrant/indexing/",
}

// upsertPointsSoleOwnerScanScope is the prefix the gate
// applies to. Files under this prefix AND not under any
// exempt prefix (above) are the gate's target surface.
const upsertPointsSoleOwnerScanScope = "internal/"

// upsertPointsSoleOwnerCallRe is the canonical line-shape
// detector. The regex requires a dot receiver before
// `UpsertPoints(`, so the function declaration line
// `func (c *Client) UpsertPoints(ctx context.Context, ...)`
// (which has `*Client` in parens, not a dotted receiver) is
// naturally exempt — only call sites match.
var upsertPointsSoleOwnerCallRe = regexp.MustCompile(`\.\s*UpsertPoints\s*\(`)

// qdrantDeletePointsCallRe is the destructive twin of the UpsertPoints
// detector (PR-HASH-SEMANTICS item 16, August 2026): `.DeletePoints(`
// must be owned by the same canonical projection-writer surface as
// UpsertPoints. The same dot-receiver requirement makes the transport
// declaration line (`func (c *Client) DeletePoints(...)`) naturally exempt.
var qdrantDeletePointsCallRe = regexp.MustCompile(`\.\s*DeletePoints\s*\(`)

// upsertPointsSoleOwnerRule is the rule-family id the
// scanner emits.
const upsertPointsSoleOwnerRule = "percheck_upsert_points_sole_owner"

// upsertPointsSoleOwnerNote is the violation Note for any
// non-canonical production call site of `.UpsertPoints(`.
// The message references the canonical IndexingHandler +
// outbox pipeline + the docs/migrations/archcheck-strict-baseline.json
// residue list so the operator sees the migration path inline.
const upsertPointsSoleOwnerNote = "forbidden non-canonical call site of client.UpsertPoints( outside the IndexingHandler outbox consumer (PR-DIAGNOSI-FINALE rule 4, July 2026); godlike/06 SSOT requires the sole production caller of qdrant UpsertPoints to be internal/platform/qdrant/indexing/ (the IndexingHandler outbox consumer). The canonical outbox-driven path is asset.index.requested → IndexingHandler → clipindexer.IndexClip → (internally) client.UpsertPoints(. Any direct caller from non-canonical paths bypasses the outbox pipeline and risks silent at-least-once regression (outbox guarantees at-least-once delivery with idempotency-key dedup; direct callers don't). Test-fixture residue callers are documented in docs/migrations/archcheck-strict-baseline.json (godlike/07 NO-FAKE-AVAILABILITY migration window)."

// deletePointsSoleOwnerNote is the violation Note for any non-canonical
// production call site of `.DeletePoints(`. It is the destructive twin of
// upsertPointsSoleOwnerNote: a direct DeletePoints bypasses the projection
// writer's retention/alias contract and can silently orphan points.
const deletePointsSoleOwnerNote = "forbidden non-canonical call site of client.DeletePoints( outside the canonical projection writer surface (PR-HASH-SEMANTICS item 16, August 2026); godlike/06 SSOT requires the sole production caller of qdrant DeletePoints to be internal/platform/qdrant/indexing/ (the IndexingHandler outbox consumer) or internal/platform/qdrant/qdrantmm/. A direct DeletePoints from a non-canonical path bypasses the projection writer's alias/retention contract and risks silent point loss. Test-fixture residue callers are documented in docs/migrations/archcheck-strict-baseline.json."

// upsertPointsSoleOwnerWarn is the residue-emitter for
// comment-only references.
func upsertPointsSoleOwnerWarn(r *report.Report, label, msg string) {
	r.Warnings = append(r.Warnings, upsertPointsSoleOwnerRule+" "+label+" "+msg)
}

// ScanUpsertPointsSoleOwner walks every .go file under
// <root>/internal/** and emits a violation for any production
// file (NOT _test.go) outside the canonical IndexingHandler
// caller surface that contains a call site matching
// `\.UpsertPoints\(`. The canonical IndexingHandler caller
// surface + test files are exempt. Comment-only references
// are residue-accounted as WARN (godlike/07).
//
// productionOnly mode silences the comment-only WARN bucket so
// the operator-facing "zero production-code hits" claim
// (PR-P12-PERCHECK-BASELINE-ZERO pattern) is auditable via
// len(r.Violations) == 0.
func ScanUpsertPointsSoleOwner(root string, pol *policy.Policy, r *report.Report, productionOnly bool) {
	_ = pol // reserved for future SeverityOverride plumbing.

	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if upsertPointsSoleOwnerSkipDirs[base] {
				return filepath.SkipDir
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr == nil {
				relSlash := filepath.ToSlash(rel)
				if hasAnyPathPrefix(relSlash, upsertPointsSoleOwnerSkipPathPrefixes) {
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
		// legitimately needs fixture UpsertPoints calls
		// (residue documented in docs/migrations/archcheck-strict-baseline.json).
		if strings.HasSuffix(relSlash, "_test.go") {
			return nil
		}
		// Out-of-scope (NOT under internal/): skip without
		// scanning. The walk is broad; the gate is narrow.
		if !strings.HasPrefix(relSlash, upsertPointsSoleOwnerScanScope) {
			return nil
		}
		// Canonical caller surface: exempt.
		if hasAnyPathPrefix(relSlash, upsertPointsSoleOwnerCanonicalCallers) {
			return nil
		}
		scanUpsertPointsSoleOwnerFile(path, relSlash, r, productionOnly)
		return nil
	})
}

// scanUpsertPointsSoleOwnerFile opens a single .go file and
// emits percheck_upsert_points_sole_owner violations for any
// line matching the canonical call-site regex. Comment-only
// references are residue-accounted as WARN. productionOnly
// suppresses the WARN bucket.
func scanUpsertPointsSoleOwnerFile(path, relPath string, r *report.Report, productionOnly bool) {
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

		matched, note := "", ""
		switch {
		case upsertPointsSoleOwnerCallRe.MatchString(line):
			matched = "non_canonical_upsert_points_caller"
			note = upsertPointsSoleOwnerNote
		case qdrantDeletePointsCallRe.MatchString(line):
			matched = "non_canonical_delete_points_caller"
			note = deletePointsSoleOwnerNote
		}
		if matched == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "//") ||
			strings.HasPrefix(trimmed, "/*") ||
			strings.HasPrefix(trimmed, "*") {
			commentOnly++
			continue
		}
		r.Violations = append(r.Violations, report.Violation{
			Package:     pkgFromUpsertPointsSoleOwnerRel(relPath),
			File:        relPath,
			Line:        lineNo,
			Rule:        upsertPointsSoleOwnerRule,
			Severity:    string(report.SeverityError),
			MatchedRule: matched,
			Note:        note + " | snippet: " + truncateUpsertPointsSoleOwner(line),
		})
	}
	if commentOnly > 0 && !productionOnly {
		upsertPointsSoleOwnerWarn(r, "upsert-points-comments:",
			strconv.Itoa(commentOnly)+" comment-only reference(s) in "+relPath+
				" (descriptive prose; non-fatal per godlike/07 no-fake-availability)")
	}
}

// pkgFromUpsertPointsSoleOwnerRel extracts the package
// identifier from a repo-relative file path. Mirrors the
// family idiom from percheck_qdrant_index_import_ban.
func pkgFromUpsertPointsSoleOwnerRel(rel string) string {
	dir := filepath.Dir(rel)
	if dir == "." || dir == "" {
		return "."
	}
	return filepath.ToSlash(dir)
}

// truncateUpsertPointsSoleOwner bounds the snippet surface at
// 120 chars to keep report JSON size stable. Mirrors the family
// idiom from percheck_qdrant_index_import_ban.
func truncateUpsertPointsSoleOwner(s string) string {
	const maxLen = 120
	const marker = " <<<"
	if len(s) > maxLen {
		return s[:maxLen] + marker
	}
	return s
}
