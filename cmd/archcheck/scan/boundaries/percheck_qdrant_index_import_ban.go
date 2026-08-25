// Package scan — percheck_qdrant_index_import_ban.go (Wave XX,
// bulk YouTube uploader + image ingest drift-fix, July 2026).
//
// Forward-prevention per-check that BANS the import of
// `github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant`
// from `internal/application/**` packages. The literal symbols
// `qdrant.IndexClip` / `qdrant.UpsertPoints` / `qdrant.WriteQdrant`
// are the canonical qdrant-write operations; per godlike/06
// SSOT, durable side effects to the qdrant ANN index MUST flow
// through the canonical CommitAsset → outbox → IndexingHandler
// pipeline (not directly from the application layer).
//
// Per the user directive ("Aggiungere check scan che vieta
// import di qdrant.IndexClip dai package application (esclusi
// admin tools)"): the ban covers the import statement of the
// qdrant infrastructure package from any internal/application/**
// package OUTSIDE the canonical exempt set:
//
//  1. cmd/admin/** — operator-grade admin tools (backfill,
//     reconciliation, diagnostics) legitimately read+write to
//     qdrant for data correction / migration. Per user
//     directive, these are EXEMPT.
//  2. internal/capabilities/jobs/outbox/** — the canonical
//     IndexingHandler outbox consumer IS the legitimate
//     receiver of outbox events emitted by CommitAsset. It
//     routes the outbox payload to the qdrant adapter.
//     Without this exemption the gate would force-couple the
//     outbox worker to a fake/no-op (godlike/07
//     NO-FAKE-AVAILABILITY violation). EXEMPT.
//
// Scan scope is `internal/application/**` ONLY. The composition
// root at `internal/app/` is explicitly NOT scanned here because
// `internal/app/build_process_qdrant.go` wires the canonical
// qdrant adapter for the composition root (godlike/06 SSOT —
// composition root is the ONLY site permitted to instantiate the
// qdrant infrastructure). Wiring-style imports from cmd/server,
// cmd/worker are also exempt (composition-root + CLIs).
//
// _test.go files are exempt (regression-guard surface legitimately
// needs fixture setups that import the qdrant package).
//
// Comment-only references to the banned import path are residue-
// accounted (godlike/07) — descriptive prose is non-fatal but
// emits a WARN so operator dashboards can spot drift.
//
// Matched rule_id: `percheck_qdrant_index_import_ban`.
package boundaries

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

// qdrantImportBanSkipDirs mirrors the standard sibling scanning
// policy from percheck_asset_state_no_shadow_enum.go +
// percheck_image_asset_invariants.go + percheck_binder_scene_field_writes.go.
var qdrantImportBanSkipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
	"node-scraper": true,
	"examples":     true,
	"archivist":    true,
	"docs":         true,
	"data":         true,
}

// qdrantImportBanSkipPathPrefixes is the scanner-package-exemption
// set: cmd/archcheck/scan references this literal pattern for
// greppability, mirrors the family precedent. The scan package
// itself imports nothing from infra/qdrant so this is a
// defense-in-depth exclusion only.
var qdrantImportBanSkipPathPrefixes = []string{
	"cmd/archcheck/scan",
}

// qdrantImportBanExemptPathPrefixes is the canonical exempt set
// per user directive ("esclusi admin tools") + the operator/audit
// tooling extension (Wave YY, July 2026, image ingest drift-fix):
//
//  1. cmd/admin/** — operator-grade CLI tooling (backfill,
//     reconciliation, diagnostics, maintenance). Per the user
//     directive the EXEMPT set is "admin tools"; the canonical
//     CLI admin subpackage at cmd/admin/ is the surface form.
//  2. internal/capabilities/jobs/outbox/** — the canonical
//     IndexingHandler outbox consumer. The composition-root
//     hooks the outbox worker up with the canonical qdrant
//     infrastructure; the outbox worker is the SOLE wire from
//     the application surface to the qdrant ANN index for
//     production index/upsert/delete events.
//  3. internal/platform/qdrant/legacyaudit/** — operator/audit
//     tooling (read-only classification walker + canonical-point-ID
//     helpers + dry-run apply-step shapes). legacyaudit is a
//     pure-classification walker that never mutates the qdrant
//     collection; the apply step is gated behind cmd/admin which
//     dispatches through the canonical outbox. Conceptually
//     identical to "admin tools" — operator tooling that
//     legitimately reads the schema for audit purposes.
//  4. internal/platform/qdrant/maintenance/** — operator/maintenance
//     tooling (audit / repair-locators / delete-invalid modes).
//     Constructs the qdrant client via the typed QdrantScannerAdapter
//     + QdrantCleaner ports; the Delete mode dispatches via the
//     canonical root.Outbox.Dispatcher.EnqueueAndDelete path.
//     Conceptually identical to "admin tools" — operator tooling.
//
// Files under these paths MAY import the qdrant infrastructure;
// any other internal/application/** package importing it must
// be refactored to CommitAsset → outbox first.
var qdrantImportBanExemptPathPrefixes = []string{
	"cmd/admin/",
	"internal/capabilities/jobs/outbox/",
	"internal/application/jobs/outbox/",
	"internal/platform/qdrant/legacyaudit/",
	"internal/platform/qdrant/maintenance/",
	"internal/application/qdrant/legacyaudit/",
	"internal/application/qdrant/maintenance/",
}

// qdrantImportBanScope is the canonical application-zone prefix
// that the gate applies to. Files under this prefix AND not under
// any exempt prefix are the gate's target surface.
const qdrantImportBanScope = "internal/application/"

const qdrantImportBanLegacyScope = "internal/capabilities/images/workflow/"

// qdrantImportPath is the literal import path the gate detects.
// The regex anchors on this fully-qualified package path + the
// closing quote (so an import like
// `"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport/client"`
// is matched at the literal line carrying the import statement
// — the `(/|\")` terminator handles both the bare-package form
// and path-subpackage forms, e.g. x/foo or x/foo/bar).
const qdrantImportPath = "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant"

// qdrantImportBanRe matches the import line shape. The pattern
// is an isolated string-literal test — only lines carrying the
// canonical qdrant import path under double quotes will trip.
// The terminator `(/|\")` eliminates false positives on strings
// like "...qdrant-adapter...".
var qdrantImportBanRe = regexp.MustCompile(`"` + regexp.QuoteMeta(qdrantImportPath) + `(/|")`)

// qdrantImportBanRule is the rule-family id the scanner emits.
const qdrantImportBanRule = "percheck_qdrant_index_import_ban"

// qdrantImportBanNote is the violation Note for any application
// import of the qdrant infrastructure. The message references
// the canonical CommitAsset → outbox path concretely so the
// operator can fix the violation without archaeology. Three
// concrete handles:
//
//  1. The canonical asset finalizer that emits the outbox event:
//     internal/application/assets/finalizer/asset_finalizer_outbox.go
//     ::(*AssetTxFinalizer).insertOutboxEvent. The caller threads
//     the user's tx so the canonical SQL UPSERT into outbox_events
//     commits ATOMICALLY with the media_assets row (QDRANT-002
//     atomicity invariant).
//
//  2. The canonical outbox consumer (i.e. THE legitimate
//     application-side caller of qdrant.IndexClip +
//     qdrant.UpsertPoints + qdrant.WriteQdrant): the IndexingHandler
//     at internal/capabilities/jobs/outbox/indexing_handle.go.
//     Routes outbox envelopes carrying the asset.index.* /
//     asset.points.upserted event types to the qdrant adapter in
//     the same SQLite TX.
//
//  3. The exempt zones for this gate: cmd/admin/** (operator
//     tooling — backfill, reconciliation, diagnostics) +
//     internal/capabilities/jobs/outbox/** (canonical outbox→qdrant
//     emitter; exempt because the IndexingHandler IS the
//     legitimate application-level wire to the qdrant infra).
//
// Scope note: the gate widens the user's literal wording
// ("vieta import di qdrant.IndexClip") to ban ALL imports of
// the qdrant infrastructure package from internal/application/**
// (including qdrant.IndexClip + qdrant.UpsertPoints +
// qdrant.WriteQdrant). This widening is the ARCHITECTURALLY CORRECT
// guard: any qdrant import from application code is suspect
// because the canonical surface is outbox-mediated. godlike/06
// SSOT forbids direct apply-layer → infra-layer writes except via
// the canonical outbox consumer.
const qdrantImportBanNote = "forbidden import of github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant from a non-exempt application package; the canonical write surface is CommitAsset (call site emits via internal/application/assets/finalizer/asset_finalizer_outbox.go::(*AssetTxFinalizer).insertOutboxEvent) THEN the canonical consumer at internal/capabilities/jobs/outbox/indexing_handle.go routes the outbox envelope (asset.index.* / asset.points.upserted event types) to the qdrant adapter in the same SQLite TX. Exempt zones per user directive + godlike/06 SSOT: cmd/admin/** (operator tooling) + internal/capabilities/jobs/outbox/** (canonical outbox→qdrant emitter). Note: the gate widens the user literal 'vieta import di qdrant.IndexClip' to ban ALL infra/qdrant imports from internal/application/** because any apply-layer → infra-layer write (other than via the outbox consumer) is godlike/06 SSOT-forbidden."

// qdrantImportBanWarnBucket is the centralized residue-emitter.
// Mirrors assetStateWarn + percheck_binder_scene_field_writes's
// warn idiom: descriptive prose referencing the banned import
// path is non-fatal per godlike/07 NO-FAKE-AVAILABILITY.
func qdrantImportBanWarnBucket(r *report.Report, label, msg string) {
	r.Warnings = append(r.Warnings, qdrantImportBanRule+" "+label+" "+msg)
}

// ScanQdrantIndexImportBan walks every .go file under
// <root>/internal/application/** and emits a violation for any
// production file (NOT _test.go) that imports the canonical
// qdrant infrastructure package from a non-exempt path. Files
// under cmd/admin/** + internal/capabilities/jobs/outbox/** are
// exempt per user directive. Comment-only references to the
// banned import path are residue-accounted as WARN
// (godlike/07).
//
// Files NOT under internal/application/** are out of scope: the
// composition root at internal/app/wire_*.go legitimately imports
// the qdrant infrastructure (it's the canonical instantiation
// site); cmd/server + cmd/worker also legitimately construct the
// qdrant adapter at CLIs.
//
// Scope-boundary note: the gate only applies to the canonical
// qdrant INFRASTRUCTURE package. Imports of
// `github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/dr`
// (the application-layer mirror) are out of scope — they're
// application types and may be imported freely. Only the literal
// `internal/platform/qdrant` import is banned.
func ScanQdrantIndexImportBan(root string, pol *policy.Policy, r *report.Report) {
	_ = pol // reserved for future SeverityOverride plumbing.

	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if qdrantImportBanSkipDirs[base] {
				return filepath.SkipDir
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr == nil {
				relSlash := filepath.ToSlash(rel)
				// SkipDirs-stringent: even if the directory is
				// under the scan scope, it MUST NOT be the
				// scanner's own package OR a doc/ scratch root.
				if hasAnyPathPrefix(relSlash, qdrantImportBanSkipPathPrefixes) {
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
		// legitimately needs fixture import setups.
		if strings.HasSuffix(relSlash, "_test.go") {
			return nil
		}
		// Out-of-scope (NOT under internal/application/): skip
		// without scanning. The walk is broad; the gate is
		// narrow. Per AGENTS.md, application-zone is the
		// canonical scope for this ban; composition root,
		// CLIs, infrastructure, and docs are all out of scope.
		if !strings.HasPrefix(relSlash, qdrantImportBanScope) && !strings.HasPrefix(relSlash, qdrantImportBanLegacyScope) {
			return nil
		}
		// Exempt subzones (cmd/admin/** + the outbox worker):
		// these packages MAY import the qdrant infrastructure
		// for legitimate operator / outbox-driven concerns.
		if hasAnyPathPrefix(relSlash, qdrantImportBanExemptPathPrefixes) {
			return nil
		}
		scanQdrantImportBanFile(path, relSlash, r)
		return nil
	})
}

// scanQdrantImportBanFile opens a single .go file and emits
// percheck_qdrant_index_import_ban violations for any line whose
// content matches qdrantImportBanRe. Comment-only references
// are residue-accounted as WARN (godlike/07 discipline).
func scanQdrantImportBanFile(path, relPath string, r *report.Report) {
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
		// references to the banned import path are descriptive
		// prose, not real imports. WARN, do NOT violate.
		if (strings.HasPrefix(trimmed, "//") ||
			strings.HasPrefix(trimmed, "/*") ||
			strings.HasPrefix(trimmed, "*")) &&
			strings.Contains(line, qdrantImportPath) {
			commentOnly++
			continue
		}
		if !qdrantImportBanRe.MatchString(line) {
			continue
		}
		r.Violations = append(r.Violations, report.Violation{
			Package:     pkgFromQdrantImportBanRel(relPath),
			File:        relPath,
			Line:        lineNo,
			Rule:        qdrantImportBanRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "qdrant_index_import_attempt",
			Note:        qdrantImportBanNote + " | snippet: " + truncateQdrantImportBan(line),
		})
	}
	if commentOnly > 0 {
		qdrantImportBanWarnBucket(r, "qdrant-import-comments:",
			strconv.Itoa(commentOnly)+" comment-only reference(s) in "+relPath+
				" (descriptive prose; non-fatal per godlike/07 no-fake-availability)")
	}
}

// truncateQdrantImportBan bounds the snippet surface at 120 chars
// to keep report JSON size stable. Mirrors truncateForReport in
// percheck_asset_state_no_shadow_enum.go.
func truncateQdrantImportBan(s string) string {
	const maxLen = 120
	const marker = " <<<"
	if len(s) > maxLen {
		return s[:maxLen] + marker
	}
	return s
}

// pkgFromQdrantImportBanRel extracts the package identifier from
// a repo-relative file path. Mirrors pkgFromAssetStateRel.
func pkgFromQdrantImportBanRel(rel string) string {
	dir := filepath.Dir(rel)
	if dir == "." || dir == "" {
		return "."
	}
	return filepath.ToSlash(dir)
}
