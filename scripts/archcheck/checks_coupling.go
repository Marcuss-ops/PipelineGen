// Package main — archcheck package-dependency coupling rules.
//
// checks_coupling.go owns the 1 package-dependency coupling check that
// monitors "raw database/sql surface" regression vs the locked
// canonical baseline.
//
//   - checkDatabaseSQLGate: ratchet gate for `database/sql` import
//     count in `internal/api`, `internal/application`, and
//     `internal/domain`. The intent is the SSOT godlike/06 invariant
//     "API = thin transport; application = typed repository via port;
//     domain = no DB imports" — `database/sql` reaching into these
//     layers means a repository concrete bubbled up out of its layer.
//     The gate emits violations ONLY for paths in `actual` that are NOT
//     in `databaseSQLLegacyBaseline` (the pre-gate surface that still
//     exists and is being shrunk incrementally).
//
// Helper (godlike/06 SSOT one-canonical-owner-per-fact: lives here,
// not in checks.go, because it's the SOLE owner of the legacy
// pre-gate db/sql surface):
//
//   - databaseSQLLegacyBaseline: the canonical pre-gate db/sql surface
//     snapshot. This baseline is the goto-allowlist for Wave 14 PR-5
//     (the ratchet gate rationale: ratchets MONOTONE-decrease across
//     the minor cycle, so the baseline is the "current state" snapshot
//     operators see at gate promotion time).
package main

import (
	"fmt"
	"os/exec"

	bl "github.com/Marcuss-ops/PipelineGen/scripts/archcheck/baseline"
)

// checkDatabaseSQLGate is the Wave 14 ratchet gate for `database/sql`
// import surface in api/application. Monitors REGGRESSIONS:
//
//   - "actual"     = current rg -ln `"database/sql"` hit count across
//     internal/api + internal/application
//   - "baseline"   = legacy baseline length (adjusted downward when
//     existing baseline entries were removed from
//     production — a positive signal, not a violation).
//   - "regressions"= `actual - baseline` (new import sites added since
//     baseline freeze). The ONLY violation source: every
//     added path emits "new database/sql import in
//     api/application: <path>".
//
// Removed entries (in baseline but not in actual) are NOT regressions
// — they are PROGRESS (-shrinkage is the gate's direction). The
// function adjusts stats["baseline"] = baseline - removed so the
// report correctly shows "active legacy surface shrinks" without
// emitting a violation entry.
//
// Pre-PR2 this rule did not exist (the Wave 14 ratchet was a single
// global counter). PR-5 introduced the per-path regression gate.
//
// NOTE: internal/domain was removed in commit b11ed83f4 (converge
// cleanup architecture, August 2026). The gate now only scans
// internal/api and internal/application.
func checkDatabaseSQLGate() (map[string]int, []string) {
	stats := map[string]int{
		"actual":      0,
		"baseline":    len(databaseSQLLegacyBaseline),
		"regressions": 0,
	}
	out, err := exec.Command("rg", "-ln", `"database/sql"`,
		"internal/api",
		"internal/application",
		"--type", "go",
	).Output()
	if err != nil && !(execErrIsNoMatch(err)) {
		stats["regressions"] = -1
		return stats, []string{fmt.Sprintf("checkDatabaseSQLGate: rg failed: %v", err)}
	}

	actual := bl.NormalizePaths(splitNonEmpty(string(out)))
	baseSet := bl.NormalizePaths(databaseSQLLegacyBaseline)
	added := bl.SubtractSet(actual, baseSet)
	removed := bl.SubtractSet(baseSet, actual)
	stats["actual"] = len(actual)
	stats["regressions"] = len(added)

	var violations []string
	for _, path := range added {
		violations = append(violations, "new database/sql import in api/application: "+path)
	}
	if len(removed) > 0 {
		stats["baseline"] = len(baseSet) - len(removed)
	}
	return stats, violations
}

// databaseSQLLegacyBaseline captures the pre-gate db/sql surface that still
// exists in api/application/domain and is being shrunk incrementally.
//
// Canonical SSOT (godlike/06 one-owner-per-fact): this slice lives in
// checks_coupling.go (NOT in a separate `baseline.go` file) because
// checkDatabaseSQLGate is its SOLE consumer. Wave 14 PR-5 explicitly
// co-located the baseline next to its consumer so a future agent
// adding new entries cannot accidentally edit a stale static snapshot
// from a different file.
//
// Ordering: alphabetical (Go sort.String idiom) for deterministic
// diff output across CI runs (random ordering would produce spurious
// CI churn on baseline refresh).
var databaseSQLLegacyBaseline = []string{
	"internal/api/middleware/middleware_auth_test.go",
	"internal/application/assets/artifacts/clips_adapter.go",
	"internal/application/assets/artifacts/finalizer_test.go",
	"internal/application/assets/artifacts/repository.go",
	"internal/application/assets/ingest/adapter_clip.go",
	"internal/application/assets/maintenance/deep_cleanup.go",
	"internal/application/assets/maintenance/run_cleanup.go",
	"internal/application/assets/maintenance/service.go",
	"internal/application/assets/monitor/channel_monitor.go",
	"internal/application/assets/providers/artlist/assetrepo_integration_test.go",
	"internal/application/assets/providers/artlist/search_cache.go",
	"internal/application/assets/providers/artlist/service.go",
	"internal/application/assets/providers/artlist/service_test.go",
	"internal/application/books/service.go",
	"internal/application/images/google_generate.go",
	"internal/application/jobs/outbox/delivery.go",
	"internal/application/jobs/outbox/registry.go",
	"internal/application/jobs/service_test.go",
	"internal/application/scripts/batch_persistence_test.go",
	"internal/application/scripts/gemmamemory/adapters.go",
	"internal/application/scripts/gemmamemory/stub_test.go",
	"internal/application/voiceover/groups_resolver_test.go",
	"internal/application/voiceover/service.go",
	"internal/application/youtube/assetrepo_integration_test.go",
}
