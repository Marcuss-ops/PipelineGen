// Package jobs — P0 #2 audit closure runtime composition test.
//
// registry_wiring_completeness_test.go is the canonical P0 #2 audit-pin
// (audit 2026-07-02/2026-07-03 retire dormant TypeYouTubeChannelSync).
//
// SCOPE (per user Option A selection): the test is the **narrow P0 #2
// retirement regression pin**, NOT a comprehensive orphan-detector. It
// asserts that the canonical `r.Register(JobPolicy{...})` callsite in
// registry.go no longer references `TypeYouTubeChannelSync`. The
// broader "registered-types-without-handlers count = 0" invariant is
// owned at the canonical composition-time surface by
// `internal/app/critical_handler_validator.go` (the REQUIRED 6-entry
// list of canonical wired-handler types) — that file is the godlike/06
// SSOT owner of the global wiring-completeness fact. Future
// `PR-HARMONIUM-RUNBOOK` work is the canonical path to expand this
// narrow pin into a typed-port-runtime orphan-detector.
//
// PATTERN PRECEDENT: tests in this package follow the canonical
// `expectedRegisteredTypes` literal-list pattern
// (registry_completeness_test.go) for source-graph completude pinning,
// rather than extracting from the regex walk below. The regex walk
// here is intentionally separate — it serves as a `TypeYouTubeChannelSync`
// -specific regression pin, where the canonical literal-list pattern
// already drops the entry.
//
// REGEX EDGE-CASE LIMITATIONS (godlike/06 audit-pin):
//   - The pattern `r\.Register\s*\(\s*JobPolicy\s*\{` requires
//     `JobPolicy{` immediately following the r.Register call. A future
//     typed variant `JobPolicy[T]{...}` would not match (regex would
//     require zero whitespace between `JobPolicy` and `{` after
//     whitespace consumption). Phase 0 trade-off: not a current
//     PipelineGen pattern; the regex is documented to surface forward
//     if a future typed variant lands.
//
// Refs:
//   - architecture/issues.yaml#PR-RETIRE-DORMANT-TYPEYOUTUBECHANNELSYNC
//     (this closure's canonical surface, status: done post-verification)
//   - internal/application/jobs/registry_completeness_test.go
//     (the canonical expectedRegisteredTypes list pattern)
//   - internal/app/critical_handler_validator.go
//     (the composition-time twin that owns the broader global
//     wiring-completeness invariant)
package policy

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// registerCallsiteTypeRegex mirrors the canonical pattern from
// cmd/archcheck scaffolding conventions: matches
// `r.Register(JobPolicy{ ... Type: <X>, ...)` across single-line + simple
// multi-line forms. Captures <X> as either string-literal (group 1)
// or Go-identifier (group 2). The pattern between JobPolicy{ and Type:
// uses `[^{}]*?` to skip non-bracket content; future contributors
// adding nested structs between JobPolicy{ and Type: would silently
// skip a callsite — godlike/07 audit-pin residue, documented in the
// package doc above.
var registerCallsiteTypeRegex = regexp.MustCompile(
	`r\.Register\s*\(\s*JobPolicy\s*\{[^{}]*?Type:\s*(?:"([^"]+)"|([A-Za-z_][A-Za-z0-9_]*))`,
)

// TestP0_2_Closure_TypeYouTubeChannelSyncRetired pins the canonical
// audit-pin for P0 #2 closure: the dormant typed-handle has been
// physically removed from the canonical job-types set in registry.go.
//
// Acceptance gate: post-commit, this test MUST pass cleanly. Canonical
// verifiable state on origin/main:
//
//   - rg --type go -n 'TypeYouTubeChannelSync' internal/ returns zero
//     PRODUCTION-CODE hits. (The comment-only audit-pin residue in
//     internal/application/assets/monitor/enqueue.go is INTENTIONAL
//     and stays per the post-2026-07-02 cleanup-pattern preservation
//     discipline; it cross-references this entry, NOT a live code
//     symbol.)
//
//   - The archcheck Check 54 detector (formerly static
//     cmd/archcheck/scan/scan_jobhandlers.go) is NOT in the canonical
//     sequence — the static approach was rejected per the user's
//     Option A selection on 2026-07-03 (godlike/07 §"No fake
//     availability" posture + canonical PipelineGen handler-dispatch
//     is slot-based via jobs.Service.RegisterHandler at composition
//     boot, not topic-name-pattern via func Handle<X>Job
//     declarations — a static regex walk would either miss dynamic
//     dispatch surface or invent fake provenance).
//
//   - The canonical composition-time REQUIRED-validator
//     (internal/app/critical_handler_validator.go) continues to gate
//     the broader wiring-completeness invariant — this test is the
//     P0 #2-specific retirement regression pin, NOT a substitute for
//     the composition-time validator.
func TestP0_2_Closure_TypeYouTubeChannelSyncRetired(t *testing.T) {
	// NOTE (godlike/07 no-fake-availability): this test is the canonical
	// regression pin for the P0 #2 audit closure. It MUST run in BOTH
	// `-short` and non-`-short` modes (CI runs `-short` by default) — no
	// `if testing.Short()` guard. The test does only file-read + regex
	// walks (~ms total) so it is safe to run on every CI build.

	// registry.go lives in the same package as this test — relative
	// path is just the filename. The test is hermetic to the package
	// boundary (no internal/app/ import — the pre-existing build
	// residue in that package's build_bundles_domain.go +
	// worker_registry_e2e_test.go does not block this test's
	// execution).
	regPath := filepath.Join("registry.go")
	raw, err := os.ReadFile(regPath)
	if err != nil {
		t.Fatalf("read registry.go: %v (test must run from the same directory as registry.go via `go test ./internal/application/jobs/...`)", err)
	}

	matches := registerCallsiteTypeRegex.FindAllSubmatch(raw, -1)
	hits := 0
	for _, m := range matches {
		var name string
		if len(m) >= 2 && len(m[1]) > 0 {
			name = string(m[1])
		} else if len(m) >= 3 && len(m[2]) > 0 {
			name = string(m[2])
		} else {
			continue
		}
		if name != "TypeYouTubeChannelSync" {
			continue
		}
		hits++
		t.Errorf(
			"TypeYouTubeChannelSync still appears in registry.go r.Register callsite (P0 #2 audit closure incomplete) — context: matched substring %q",
			string(m[0]),
		)
	}

	if hits > 0 {
		t.Logf("P0 #2 audit closure FAIL: %d occurrence(s) of TypeYouTubeChannelSync in registry.go r.Register callsites — expected 0 per P0 #2 retired-typed-handle contract", hits)
	} else {
		t.Logf("P0 #2 audit closure PASS: TypeYouTubeChannelSync retired from registry.go r.Register callsites (retirement acceptance gate met)")
	}
}
