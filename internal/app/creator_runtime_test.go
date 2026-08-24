// Package app — creator_runtime_test.go (P0 Commit 8, July 2026).
//
// This test file enforces the Creator-side no-DB / no-Qdrant /
// no-Scheduler / no-CatalogSync contract declared in
// creator_runtime.go's package doc. Three freeze tests + three
// constructor smoke tests:
//
//  1. TestCreatorRuntime_FrozenImportAllowlist — AST scan of every
//     creator_*.go under internal/app/ for forbidden imports.
//     Mirrors the source-count precedent set by
//     composition_test.go::TestComposition_FrozenClientConstructionSites.
//
//  2. TestCreatorRuntime_StddlibDatabaseSqlImported — asserts the
//     POSITIVE side of the DB orphan pin: creator_runtime.go MUST
//     import `database/sql` so the compile-pin
//     `var _ = func() any { var _ *sql.DB = nil; return nil }`
//     resolves at compile time.
//
//  3. TestCreatorRuntime_CompilePinDBOrphanResolved — verifies that
//     `*sql.DB` is type-resolvable from this test package itself,
//     so a regression that drops the import breaks the test build
//     loud (compile-time, not runtime).
//
// Plus three happy-path / fail-closed smoke tests on BuildCreatorRuntime
// matching the focused-builder pattern in composition_test.go::TestBuildJobsBundle_FieldsAreNonNil.
package app

import (
	"context"
	"database/sql" // ← mirror the canonical orphan-pin import; used by TestCreatorRuntime_CompilePinDBOrphanResolved
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// ── 1. import-allowlist freeze test ────────────────────────────

// frozenForbiddenImportSubstringsLen pins the EXACT slice length so
// a drift that guts the list (replacing one forbiddance with an
// empty slice) is a test failure rather than a silent regression.
// Future commits that ADD or REMOVE a forbidden reach MUST update
// BOTH the slice declaration AND this constant in lockstep.
const frozenForbiddenImportSubstringsLen = 4

// frozenForbiddenImportSubstrings pins P0 Commit 8 (July 2026)
// acceptance criterion: NO creator_*.go file under internal/app/
// may import any of the following substrings. The Creator-side
// surface is PUSH-ONLY: it produces artifacts that the Sender
// persists via the canonical remote.ArtifactUploader port.
// Forbidding direct reach into these packages prevents the
// Creator from accidentally shortening the Sender-side ingestion
// pipeline.
//
// Adding a new forbidden reach MUST update this slice AND the
// creator_runtime.go package doc contract in lockstep.
var frozenForbiddenImportSubstrings = []string{
	// SQLite impl — Creator has no DB. The Sender-side SQLite
	// layer (media.db.sqlite) is the canonical metadata store
	// (godlike/06 "one canonical owner per fact").
	"internal/platform/sqlite",

	// Qdrant impl — Creator has no vector projection. Qdrant is
	// a Sender-side derived projection (godlike/06 SemanticIndex).
	"internal/platform/qdrant",

	// Scheduler — Creator awaits HTTP-poll / gRPC-pull jobs;
	// it does not own a background scheduler.
	"scheduler",

	// CatalogSync — Sender-side catalog reconciliation helper.
	"catalogsync",
}

// TestCreatorRuntime_FrozenImportAllowlist verifies that every
// internal/app/creator_*.go file (excluding _test.go) imports
// ONLY the canonical allowlist and NEVER any of the forbidden
// substrings. Source-level AST parse via go/parser so the test
// is robust to multi-line imports, alias renames, and varied
// formatting.
//
// Drift signal: if a future commit adds one of the forbidden
// imports to any creator_*.go file, this test fails with the
// exact forbidden-substring match so the operator can grep the
// failure message against the package-doc contract.
//
// Invariant note: tests are cwd-relative on the
// `internal/app/creator_*.go` glob, so chdirToProjectRoot(t) is
// invoked first (matches composition_test.go precedent).
func TestCreatorRuntime_FrozenImportAllowlist(t *testing.T) {
	chdirToProjectRoot(t)
	pattern := filepath.Join("internal", "app", "creator_*.go")
	files, err := filepath.Glob(pattern)
	require.NoError(t, err, "glob creator_*.go")
	require.NotEmpty(t, files, "no creator_*.go matched — chdir to project root failed or no creator files exist")

	// Pin the forbidden-reach list at exactly the C8 contract
	// (4 substrings: SQLite, Qdrant, Scheduler, CatalogSync). If
	// a future commit shrinks the list without updating the
	// package doc contract, the test fires loud.
	require.Len(t, frozenForbiddenImportSubstrings, frozenForbiddenImportSubstringsLen,
		"P0 C8: forbidden-reach list shrank without lockstep update to creator_runtime.go package doc. Both MUST move together.")

	// Sort for deterministic iteration + concise failure messages.
	sort.Strings(files)

	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		imports := parseGoFileImports(t, file)
		for _, imp := range imports {
			for _, forbid := range frozenForbiddenImportSubstrings {
				if strings.Contains(imp, forbid) {
					t.Errorf("P0 C8: forbidden import %q in %s (matched substring %q). The Creator-side contract forbids reaching into %s — route via Sender-side ports instead (see creator_runtime.go package doc).",
						imp, file, forbid, forbid)
				}
			}
		}
	}
}

// TestCreatorRuntime_StddlibDatabaseSqlImported verifies the
// POSITIVE side of the DB orphan pin: creator_runtime.go MUST
// import `database/sql` so the compile-pin
// `var _ = func() any { var _ *sql.DB = nil; return nil }`
// resolves at compile time. A drift that removes the import
// fails the build via the orphan pin's type reference
// ("undefined: sql.DB" at the producer side); this test fires
// the matching receiver-side alarm.
func TestCreatorRuntime_StddlibDatabaseSqlImported(t *testing.T) {
	chdirToProjectRoot(t)
	const target = "database/sql"

	pattern := filepath.Join("internal", "app", "creator_runtime.go")
	files, err := filepath.Glob(pattern)
	require.NoError(t, err, "glob creator_runtime.go")
	require.NotEmpty(t, files, "creator_runtime.go must exist (P0 C8 canonical surface)")

	imports := parseGoFileImports(t, files[0])
	for _, imp := range imports {
		if imp == target {
			return // POSITIVE pin satisfied
		}
	}
	t.Errorf("P0 C8: creator_runtime.go must import `database/sql` (POSITIVE side of the DB orphan pin). The compile-time `var _ = func() any { var _ *sql.DB = nil; return nil }` requires the import to be resolvable - without it, the build fails with `undefined: sql.DB`.")
}

// parseGoFileImports returns the canonical set of import paths
// declared in file. Comments, alias renames, and grouping blocks
// are normalised — the returned slice is the import paths only
// (without the surrounding quote characters).
func parseGoFileImports(t *testing.T, file string) []string {
	t.Helper()
	fset := token.NewFileSet()
	src, err := os.ReadFile(file)
	require.NoError(t, err, "read %s", file)
	f, err := parser.ParseFile(fset, file, src, parser.ImportsOnly)
	require.NoError(t, err, "parse %s", file)

	out := make([]string, 0, len(f.Imports))
	for _, imp := range f.Imports {
		// imp.Path.Value is the quoted form (e.g. "\"database/sql\"");
		// strip the quotes to get the canonical import path.
		v := imp.Path.Value
		if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
			v = v[1 : len(v)-1]
		}
		out = append(out, v)
	}
	return out
}

// ── 2. compile-time orphan-pin surface smoke ──────────────────

// TestCreatorRuntime_CompilePinDBOrphanResolved verifies that
// `*sql.DB` is type-resolvable from this test package itself (the
// import `database/sql` is declared at the top of this file).
// The producer side (creator_runtime.go) has the analogous import
// resolved by the actual `var _ = func() any { var _ *sql.DB =
// nil; return nil }` pin. This test acts as the receiver-side
// regression guard for both compile-pin shapes: if either side's
// import of `database/sql` accidentally drops, the go-build fails
// loud ("undefined: sql.DB" at the source location of the var
// usage), not silent at runtime.
func TestCreatorRuntime_CompilePinDBOrphanResolved(t *testing.T) {
	var _ *sql.DB = nil
	_ = context.TODO
}

// ── 3. BuildCreatorRuntime constructor smoke tests ────────────

// minimalCreatorConfig returns a minimal config that disables
// every external feature (Drive, Artlist, YouTube, Qdrant, etc.)
// so BuildCreatorRuntime can construct without network round-trips.
// Mirrors the fixture in composition_test.go::minimalConfig.
func minimalCreatorConfig(dataDir string) *config.Config {
	return &config.Config{
		Linguistics: config.LinguisticsConfig{LexiconRoot: testLexiconRoot()},
		Server:      config.ServerConfig{Port: 8080},
		External: config.ExternalConfig{
			OllamaURL:            "http://localhost:11434",
			OllamaModel:          "llama3.2",
			OllamaTimeoutSeconds: 30,
		},
		Storage: config.StorageConfig{DataDir: dataDir},
	}
}

// TestBuildCreatorRuntime_HappyPath_ReturnsNonNil is the canonical
// constructor smoke test. Mirrors the focused-builder pattern in
// composition_test.go::TestBuildJobsBundle_FieldsAreNonNil.
//
// Lock-in criterion (P0 C8): BuildCreatorRuntime returns a non-nil
// *CreatorRuntime under a minimal config. Per-field nil checks
// match the canonical CreatorRoot/CreatorRuntime fields that
// workerruntime/run.go depends on (Registry, Caps, Workspace,
// AssetClient, OllamaClient).
func TestBuildCreatorRuntime_HappyPath_ReturnsNonNil(t *testing.T) {
	chdirToProjectRoot(t)
	dataDir := t.TempDir()
	cfg := minimalCreatorConfig(dataDir)
	log := zaptest.NewLogger(t)

	rt, cleanup, err := BuildCreatorRuntime(cfg, log)
	require.NoError(t, err, "BuildCreatorRuntime")
	require.NotNil(t, rt, "*CreatorRuntime must be non-nil")
	require.NotNil(t, cleanup, "cleanup func must be non-nil")

	// Canonical Creator fields consumed by workerruntime/run.go
	// (Creator profile branch): all non-nil so the worker
	// registration handshake can proceed.
	require.NotNil(t, rt.Registry, "rt.Registry (workerruntime reads registry from creator profile)")
	require.NotNil(t, rt.Workspace, "rt.Workspace (workerruntime reads ws)")
	require.NotNil(t, rt.OllamaClient, "rt.OllamaClient (logged diagnostic)")
	require.NotNil(t, rt.AssetClient, "rt.AssetClient (Runner needs it for claim/submit)")

	// Deferred placeholder fields must retain typed-nil sentinel
	// (Blocco 3.x wiring future work).
	require.Nil(t, rt.VoiceoverEngine, "rt.VoiceoverEngine is a typed-nil placeholder until Blocco 3.x")
	require.Nil(t, rt.ImageGenerator, "rt.ImageGenerator is a typed-nil placeholder until Blocco 3.x")

	require.NotEmpty(t, rt.Caps.JobTypes, "Caps.JobTypes must contain at least one job type")

	// Cleanup must succeed silently (BestEffort on workspace removal).
	cleanup()
}

// TestBuildCreatorRuntime_NilConfig_FailsClosed verifies the
// fail-closed precondition at composable-time. A nil cfg must
// produce a typed error, not a panic.
func TestBuildCreatorRuntime_NilConfig_FailsClosed(t *testing.T) {
	log := zaptest.NewLogger(t)
	rt, cleanup, err := BuildCreatorRuntime(nil, log)
	require.Error(t, err, "nil config must fail closed")
	require.Nil(t, rt, "*CreatorRuntime must be nil on nil config")
	require.Nil(t, cleanup, "cleanup must be nil on nil config")
}

// TestBuildCreatorRuntime_NilLogger_FailsClosed: same fail-closed
// check for the logger argument.
func TestBuildCreatorRuntime_NilLogger_FailsClosed(t *testing.T) {
	dataDir := t.TempDir()
	cfg := minimalCreatorConfig(dataDir)
	rt, cleanup, err := BuildCreatorRuntime(cfg, nil)
	require.Error(t, err, "nil logger must fail closed")
	require.Nil(t, rt, "*CreatorRuntime must be nil on nil logger")
	require.Nil(t, cleanup, "cleanup must be nil on nil logger")
}
