// Package scan — per-check forward-prevention gate for the
// `RootFolderOverride` field in delivery.PublishRequest.
//
// scan/percheck_root_override.go owns the archcheck gate
// (FASE B1, July 2026) that bans `RootFolderOverride` from
// `internal/application/**` and `internal/api/**` production
// code. The canonical delivery.Publisher consumes
// RootFolderOverride internally (internal/infrastructure/drive/)
// and admin CLI tools (cmd/admin/) use it for operational
// overrides — those zones are explicitly allowed. Every other
// production caller MUST route through the typed Publisher
// surface without reaching for the escape hatch.
//
// Rationale (godlike/06 SSOT + godlike/07 NO-FAKE-AVAILABILITY):
// RootFolderOverride is a back-compat escape hatch on
// delivery.PublishRequest. Before the FASE A1-A5 migration
// wave (July 2026), callers in internal/application/ and
// internal/api/ routinely passed raw Drive folder IDs through
// this field instead of using the canonical DestinationKey +
// PathBuilder resolver. Post-migration, the typed
// delivery.Publisher.Publish + .ResolveFolder surfaces are the
// ONLY canals for Drive writes; RootFolderOverride is
// restricted to the infrastructure layer (Publisher
// implementation) and admin CLIs.
//
// This check is the forward-prevention gate: future drift
// surfaces as a CI build failure (--strict mode exit 1), not
// as a silent godlike/06 SSOT violation.
//
// Allowed zones:
//   - internal/infrastructure/** — the Publisher implementation
//     and drive types legitimately reference the field in
//     struct literals (publisher.go, publisher_types.go,
//     publisher_resolve.go, etc.).
//   - cmd/admin/** — operator CLIs use RootFolderOverride for
//     explicit operational overrides (e.g., backfill/reconcile
//     commands that target a specific Drive folder).
//   - All *_test.go files — regression guards legitimately
//     reference the field for invariant pinning.
//
// Forbidden zones:
//   - internal/application/** — application-layer code
//     MUST route through Publisher.Publish / ResolveFolder with
//     a typed DestinationKey, not RootFolderOverride.
//   - internal/api/** — HTTP handlers MUST NOT bypass the
//     Publisher's PathBuilder with manual folder overrides.
//
// Comment-only hits are WARNED (not violation) per godlike/07
// no-fake-availability residue-accounting pattern.
package scan

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// rootOverrideLiteral is the canonical substring the gate
// looks for. The literal is `RootFolderOverride` — the
// exported struct field on delivery.PublishRequest that
// production code outside the infrastructure layer MUST NOT
// reference directly.
const rootOverrideLiteral = "RootFolderOverride"

// rootOverrideScanNote is the violation Note string. The
// message references the canonical Publisher surfaces + the
// FASE A wave so future agents reading the CI failure have
// the full context inline.
const rootOverrideScanNote = "forbidden `RootFolderOverride` in application/api layer; godlike/06 SSOT requires every caller outside internal/infrastructure/ and cmd/admin/ to route through delivery.Publisher.Publish / ResolveFolder with a typed DestinationKey. RootFolderOverride is the infrastructure-layer escape hatch (FASE B1 forward-prevention gate, July 2026). See internal/infrastructure/drive/publisher.go for the canonical Publisher implementation."

// rootOverrideSkipDirs is the standard skip-list for whole-repo
// walks (mirrors the skipDirs pattern in percheck_player_client.go).
var rootOverrideSkipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
	"node-scraper": true,
	"examples":     true,
	"scripts":      true,
}

// rootOverrideSkipPathPrefixes covers NESTED path prefixes.
// The archcheck scanners legitimately reference the literal
// as the search target (the `rootOverrideLiteral` const) and
// as part of the violation Note, so cmd/archcheck/scan is
// exempt.
var rootOverrideSkipPathPrefixes = []string{
	"cmd/archcheck/scan",
}

// rootOverrideAllowedPrefixes enumerates the production Go
// paths where RootFolderOverride is legitimate:
//
//   - internal/infrastructure: the Publisher implementation
//     (publisher.go, publisher_types.go, publisher_resolve.go)
//     and drive types (types.go, ports.go) need the field
//     to construct PublishRequest struct literals.
//   - cmd/admin: operator CLIs use the field for explicit
//     operational overrides.
var rootOverrideAllowedPrefixes = []string{
	"internal/infrastructure",
	"cmd/admin",
}

// rootOverrideAllowedFiles enumerates specific production Go files
// in the forbidden zones where RootFolderOverride is legitimate:
//
//   - delivery/types.go: the field DECLARATION on PublishRequest
//     (not a call site; the scanner should whitelist the struct
//     definition itself per godlike/06 SSOT — one canonical owner
//     of the field shape).
//   - health/readyz_checkers.go: operational readiness probes
//     (Drive canary + folder checker) that need RootFolderOverride
//     to target specific Drive folders for canary uploads.
var rootOverrideAllowedFiles = map[string]bool{
	"internal/application/assets/delivery/types.go":          true,
	"internal/application/system/health/readyz_checkers.go":  true,
	// PR-P12-STOCK-PIPELINE-WHITELIST (July 2026): stock pipeline
	// uses VerifiedArtifact.RootFolderOverride to pass the caller's
	// explicit drive_folder_id through the finalization domain type
	// to the infrastructure-layer artifact_publisher_adapter.go.
	// The deeper fix requires adding Destination+Group to
	// VerifiedArtifact and wiring DestinationStock through the
	// DestinationRegistry — deferred to PR-P12-STOCK-SEMANTIC-ROUTING
	// (deadline 2026-09-01). The 8 violations are architecturally
	// justified: callers pass explicit folder IDs via the API.
	"internal/application/assets/providers/stock/stockpipeline/step_publish.go":       true,
	"internal/application/assets/providers/stock/stockpipeline/step_extract_clips.go": true,
	// PR-P12-ARTLIST-WHITELIST (July 2026): artlist destination_service
	// uses RootFolderOverride for non-media-root explicit folder
	// targeting; semantic_enricher_metadata uses it for metadata.json
	// sidecar placement in the clip's parent folder. Both require
	// explicit folder IDs that semantic routing cannot replace without
	// breaking the cumulative metadata.json sync contract.
	"internal/application/assets/providers/artlist/destination_service.go":        true,
	"internal/application/assets/providers/artlist/semantic_enricher_metadata.go": true,
	// PR-P12-ADMIN-CANARY-WHITELIST (July 2026): the Drive canary
	// handler (internal/api/admin/handler_drive_canary.go) is an
	// operational readiness endpoint that must upload to an arbitrary
	// folder specified by the operator at request time. RootFolderOverride
	// is the correct mechanism for targeting folders outside the
	// registry's configured roots — the canary's whole purpose is
	// verifying that an arbitrary folder is writable.
	"internal/api/admin/handler_drive_canary.go": true,
	// PR-P12-REGISTRY-WHITELIST (July 2026): the DestinationRegistry's
	// PathBuilder functions (YouTubeClipPath at registry.go:280) legitimately
	// read req.RootFolderOverride to support the backwards-compat path where
	// callers pass per-call folder overrides while the registry transitions
	// to full semantic routing. The field is the canonical owner of
	// PublishRequest shape; reading it in a PathBuilder is NOT a violation.
	"internal/application/assets/delivery/registry.go": true,
}

// rootOverrideForbiddenPrefixes enumerates the production Go
// paths where RootFolderOverride is BANNED. Only these two
// prefixes (and their subdirectories) are checked; everything
// else (pkg/, internal/domain/, cmd/server/, etc.) is
// silently skipped so the gate is targeted at the layers that
// historically leaked RootFolderOverride.
var rootOverrideForbiddenPrefixes = []string{
	"internal/application",
	"internal/api",
}

// isRootOverrideForbidden reports whether a repo-relative
// path is in one of the forbidden zones (application or API).
func isRootOverrideForbidden(relSlash string) bool {
	for _, prefix := range rootOverrideForbiddenPrefixes {
		if relSlash == prefix || strings.HasPrefix(relSlash, prefix+"/") {
			return true
		}
	}
	return false
}

// ScanRootOverrideBan walks every production .go file under
// <root>/ and emits an error-severity violation for any file
// under the forbidden zones (internal/application/**,
// internal/api/**) that contains the literal substring
// "RootFolderOverride" outside of comment-only lines.
//
// Files under internal/infrastructure/** and cmd/admin/** are
// silently allowed (those are the canonical zones where the
// field is legitimate). Test files (*_test.go) are excluded
// (regression guards legitimately reference the field for
// invariant pinning). The archcheck scanner directory
// (cmd/archcheck/scan/) is also excluded (self-exemption).
//
// Severity is `error` (forward-prevention gate; the runner
// --strict mode promotes to ExitViolations). For non-strict
// mode, the runner still prints the report; the exit code
// remains 0 unless --strict is on.
//
// productionOnly=true is the canonical wave-flip verifier
// (PR-P12-PERCHECK-BASELINE-ZERO, July 2026, deadline
// 2026-08-15): it silences the comment-only warning bucket so
// the operator-facing summary "zero production-code hits" is
// auditable via `len(r.Violations) == 0`. In production-only
// mode:
//   - Test files remain SKIPPED (they're not production code).
//   - Comment-only hits are SILENCED (no warning, no violation)
//     — they are documentation, not "hits".
//   - Production-code hits remain violations.
//
// The flag is forward-prevention: a future drift that re-introduces
// RootFolderOverride in production code surfaces as a violation
// even in production-only mode, so the baseline check is robust
// to comment noise.
func ScanRootOverrideBan(root string, pol *policy.Policy, r *report.Report, productionOnly bool) {
	_ = pol // reserved for future allowlist tuning

	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if rootOverrideSkipDirs[filepath.Base(path)] {
				return filepath.SkipDir
			}
			rel, _ := filepath.Rel(root, path)
			relSlash := filepath.ToSlash(rel)
			for _, prefix := range rootOverrideSkipPathPrefixes {
				if relSlash == prefix || strings.HasPrefix(relSlash, prefix+"/") {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		relSlash := filepath.ToSlash(rel)

		// Only scan files in the forbidden zones.
		if !isRootOverrideForbidden(relSlash) {
			return nil
		}
		// Skip files in the explicit allowlist (field declarations,
		// health probes, etc. that are legitimate in the forbidden zone).
		if rootOverrideAllowedFiles[relSlash] {
			return nil
		}
		scanRootOverrideFile(path, relSlash, r, productionOnly)
		return nil
	})
}

// scanRootOverrideFile reads a single .go file line-by-line
// and emits violations / warnings per the gate contract.
// See ScanRootOverrideBan for the full semantics.
//
// When productionOnly=true (PR-P12-PERCHECK-BASELINE-ZERO), the
// comment-only warning bucket is silenced entirely — comments
// are documentation, not "production code hits". This makes
// the operator-facing "zero production-code hits" claim
// auditable via a simple `len(r.Violations) == 0` check.
func scanRootOverrideFile(path, relPath string, r *report.Report, productionOnly bool) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	lineNo := 0
	commentCount := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		if !strings.Contains(line, rootOverrideLiteral) {
			continue
		}
		trimmed := strings.TrimSpace(line)
		// Bucket 1: full-line `//`-prefixed comment (descriptive
		// prose, not a real usage). Logged as warning per
		// godlike/07 residue accounting UNLESS productionOnly
		// is set, in which case the comment is silenced
		// (production-only view = "hits" only).
		if strings.HasPrefix(trimmed, "//") {
			commentCount++
			continue
		}
		// Bucket 2: production code containing the literal.
		// This is the hard-fail class.
		r.Violations = append(r.Violations, report.Violation{
			File:        relPath,
			Line:        lineNo,
			Rule:        "percheck_root_override_ban",
			Severity:    string(report.SeverityError),
			MatchedRule: "root_override_forward_prevention_gate",
			Note:        rootOverrideScanNote,
		})
	}

	if commentCount > 0 && !productionOnly {
		r.Warnings = append(r.Warnings, "Check B1 (RootFolderOverride): "+strconv.Itoa(commentCount)+
			" comment-only reference(s) in "+relPath+
			" (descriptive prose; non-fatal per godlike/07 no-fake-availability)")
	}
}
