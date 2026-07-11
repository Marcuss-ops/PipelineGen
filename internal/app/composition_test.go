// Package app — composition tests (PR4.C, June 2026).
//
// This file freezes the structural invariants of the post-PR4 bundle
// decomposition in composition.go. It complements (does NOT replace):
//   - wire_test.go: end-to-end smoke via WireServices (no per-bundle field
//     assertions, no goroutine assertions, no ordering assertions).
//   - TestBuildJobsBundle_FieldsAreNonNil: focused BuildJobsBundle field
//   - nil-input checks (cross-validates the canary assertions below).
//
// What this file adds:
//  1. TestComposition_NilObligatory_NewComposition — every bundle in the
//     assembled ComposeRoot is non-nil; canary fields in each bundle are
//     populated under a minimal config (no Drive/Artlist/Youtube features).
//  2. TestComposition_NilObligatory_Build*Bundle — per-builder nil-field
//     invariants for the leaf builders that can be exercised without
//     external services (RepoBundle, SearchBundle, JobsBundle).
//  3. TestComposition_NoGoroutinesSpawned_FrozenSiteCount — source-level
//     assertion that the goroutine-spawn sites inside Build*Bundle bodies
//     match the frozen count. Today: 1 site inside BuildDriveBundle +
//     2 sites inside BuildOutboxBundle = 3 total; zero in every other
//     builder. Migrating these to lifecycle.go (the proper home for
//     goroutines that need ctx-bound lifecycle) is a future PR4.E+ wave;
//     updating the frozen constants in this file is the documented signal.
//  4. TestComposition_FreezeOrdering_BuildSequence — source-level
//     assertion that NewComposition's Build*Bundle call order matches the
//     frozen sequence (Repo → Search → Drive → Process → Jobs → AI →
//     Outbox → Domain → Sync → Maint → Utility). The order encodes the
//     dependency graph; any change is a refactor signal. PR-12d
//     (June 2026) swapped Domain and Outbox so the canonical outbox
//     dispatcher is available when images.Service is constructed via
//     constructor injection (closing the late-bind ordering hazard).
package app

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// ── Test helpers ─────────────────────────────────────────────────────────

// compositionSourcePath is the canonical relative path to composition.go
// from the project root. chdirToProjectRoot (below) ensures the cwd is the
// project root so the path resolves consistently regardless of where
// `go test ./internal/app/` is invoked from.
const compositionSourcePath = "internal/app/composition.go"

// chdirToProjectRoot mirrors the pattern in wire_test.go so nested
// migrations + relative-path resolution work the same way.
func chdirToProjectRoot(t *testing.T) {
	t.Helper()
	projectRoot := filepath.Join("..", "..")
	origDir, err := os.Getwd()
	require.NoError(t, err, "getwd")
	require.NoError(t, os.Chdir(projectRoot), "chdir to project root")
	t.Cleanup(func() { _ = os.Chdir(origDir) })
}

// minimalConfig returns a config that disables all heavy external features
// (Drive, Artlist, YouTube, VectorSearch, Reranker, VLM, ClipIndexer,
// background jobs) so NewComposition runs without network round-trips.
// Mirrors the fixture in wire_test.go so composition has the same baseline.
func minimalConfig(dataDir string) *config.Config {
	return &config.Config{
		Security: config.SecurityConfig{EnableAuth: false},
		Server:   config.ServerConfig{Port: 8080, ReadTimeout: 600, WriteTimeout: 600},
		External: config.ExternalConfig{
			OllamaURL:            "http://localhost:11434",
			OllamaModel:          "llama3.2",
			OllamaTimeoutSeconds: 30,
		},
		Paths:   config.PathsConfig{},
		Storage: config.StorageConfig{DataDir: dataDir},
		Features: config.FeaturesConfig{
			DriveEnabled:   false,
			ArtlistEnabled: false,
			YouTubeEnabled: false,
		},
		VLM:         config.VLMConfig{Enabled: false},
		ClipIndexer: config.ClipIndexerConfig{Enabled: false},
		Reranker:    config.RerankerConfig{Enabled: false},
		Drive:       config.DriveConfig{},
		Books:       config.BooksConfig{ScriptPath: "scripts/bridges/book_summarizer.py"},
		Jobs: config.JobsConfig{
			EnableBackgroundJobs: false, // suppress lifecycle spawners
		},
	}
}

// ── 1. nil-obligatory ────────────────────────────────────────────────────

// TestComposition_NilObligatory_NewComposition asserts every bundle
// pointer on the AssembleRoot after NewComposition is non-nil and the
// canary fields on each bundle are populated. This is the regression
// guard for "build a bundle, forget to wire its inner service" — a
// historically common drift in this codebase (see CHANGELOG_BATCH_*
// lessons merged April-June 2026).
func TestComposition_NilObligatory_NewComposition(t *testing.T) {
	chdirToProjectRoot(t)

	dataDir := t.TempDir()
	cfg := minimalConfig(dataDir)
	log := zaptest.NewLogger(t)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	dbs, err := initDatabases(cfg, log)
	require.NoError(t, err, "initDatabases")
	t.Cleanup(func() {
		if dbs != nil && dbs.main != nil {
			_ = dbs.main.Close()
		}
	})

	root, err := NewComposition(ctx, cfg, dbs, log)
	require.NoError(t, err, "NewComposition")
	require.NotNil(t, root, "root")
	require.NotNil(t, root.DB, "root.DB")

	// ComposeRoot child bundles.
	require.NotNil(t, root.Drive, "root.Drive")
	require.NotNil(t, root.Repos, "root.Repos")
	require.NotNil(t, root.Search, "root.Search")
	require.NotNil(t, root.Process, "root.Process")
	require.NotNil(t, root.AI, "root.AI")
	require.NotNil(t, root.Domains, "root.Domains")
	require.NotNil(t, root.Jobs, "root.Jobs")
	require.NotNil(t, root.Outbox, "root.Outbox")
	require.NotNil(t, root.Sync, "root.Sync")
	require.NotNil(t, root.Maint, "root.Maint")
	require.NotNil(t, root.Utility, "root.Utility")
	require.NotNil(t, root.Ctx, "root.Ctx")
	// PR9-A (June 2026): DriveStart closure is always non-nil when Drive bundle is built.
	// Lifecycle-runtime-ownership (June 2026): IOpaqueStartFunc now returns error.
	require.NotNil(t, root.DriveStart, "root.DriveStart (PR9-A deferred side-effect closure)")
	// PR9-B (June 2026): OutboxStart closure is always non-nil when Outbox bundle is built.
	require.NotNil(t, root.OutboxStart, "root.OutboxStart (PR9-B deferred side-effect closure)")
	// PR9-C (June 2026, June 2026 Onda 5): root.ProcessStart was a
	// planned-but-never-implemented PR9-C deferred-start closure on the
	// Process bundle. BuildProcessBundle returns only (*ProcessBundle,
	// error) (no IOpaqueStartFunc — Process has no async startup pool),
	// so the field was never added to ComposeRoot. The original test
	// assertion referenced a phantom field and broke `go vet ./internal/app/`
	// — removed here to align with the actual ComposeRoot shape. If a
	// future PR9-C+ wave reintroduces ProcessStart, re-add the
	// assertion in lockstep with the new field.

	// RepoBundle canaries (8 fields).
	require.NotNil(t, root.Repos.ScriptsRepo, "root.Repos.ScriptsRepo")
	require.NotNil(t, root.Repos.ImageRepo, "root.Repos.ImageRepo")
	require.NotNil(t, root.Repos.ClipsRepo, "root.Repos.ClipsRepo")
	require.NotNil(t, root.Repos.Assets, "root.Repos.Assets")
	require.NotNil(t, root.Repos.MonitorsRepo, "root.Repos.MonitorsRepo")
	require.NotNil(t, root.Repos.VoiceoverRepo, "root.Repos.VoiceoverRepo")
	require.NotNil(t, root.Repos.CatalogRepo, "root.Repos.CatalogRepo")
	require.NotNil(t, root.Repos.SQRepo, "root.Repos.SQRepo")

	// SearchBundle canaries (4 fields).
	require.NotNil(t, root.Search.AssetIndexService, "root.Search.AssetIndexService")
	require.NotNil(t, root.Search.AssetTreeService, "root.Search.AssetTreeService")
	require.NotNil(t, root.Search.AssetResolver, "root.Search.AssetResolver")
	require.NotNil(t, root.Search.ProviderRegistry, "root.Search.ProviderRegistry")

	// AIBundle canaries (4 fields, includes PR4.A-relocated MemoryRepo).
	// Commit H Phase 2 (June 2026): MemoryService field was removed
	// (gemmamemory gate service + its narrow-cache wrapper gone).
	require.NotNil(t, root.AI.OllamaClient, "root.AI.OllamaClient")
	require.NotNil(t, root.AI.ScriptGen, "root.AI.ScriptGen")
	require.NotNil(t, root.AI.MemoryRepo, "root.AI.MemoryRepo (PR4.A)")
	require.NotNil(t, root.AI.ScriptEngine, "root.AI.ScriptEngine")

	// JobsBundle canaries (4 fields; cross-validated by
	// TestBuildJobsBundle_FieldsAreNonNil).
	require.NotNil(t, root.Jobs.Repo, "root.Jobs.Repo")
	require.NotNil(t, root.Jobs.Dispatcher, "root.Jobs.Dispatcher")
	require.NotNil(t, root.Jobs.Service, "root.Jobs.Service")
	require.NotNil(t, root.Jobs.Facade, "root.Jobs.Facade")

	// OutboxBundle canaries (4 fields).
	require.NotNil(t, root.Outbox.Dispatcher, "root.Outbox.Dispatcher")
	require.NotNil(t, root.Outbox.EventsRepo, "root.Outbox.EventsRepo")
	require.NotNil(t, root.Outbox.EventsRegistry, "root.Outbox.EventsRegistry")
	require.NotNil(t, root.Outbox.EventsPool, "root.Outbox.EventsPool")

	// DomainBundle / SyncBundle / MaintBundle / UtilityBundle fields that
	// represent capabilities enabled under minimal cfg may legitimately be
	// nil (e.g. VoiceoverSync is only built when voFolder != "" and the
	// voiceover repo exists). The "bundle pointer is non-nil" check above
	// is the structural invariant; per-field checks here are deliberately
	// limited to fields unconditionally populated under minimal cfg.
	// DriveBundle fields driveClient/driveUploader/docClient MAY be nil
	// under no-Drive-features config; we do not assert them.
}

// TestComposition_NilObligatory_BuildRepoBundle tests BuildRepoBundle in
// isolation — mirrors the focused-builder pattern in
// TestBuildJobsBundle_FieldsAreNonNil.
// RepoBundle is the canonical leaf (no other-bundle deps).
func TestComposition_NilObligatory_BuildRepoBundle(t *testing.T) {
	chdirToProjectRoot(t)

	dataDir := t.TempDir()
	cfg := minimalConfig(dataDir)
	log := zaptest.NewLogger(t)

	dbs, err := initDatabases(cfg, log)
	require.NoError(t, err)
	t.Cleanup(func() {
		if dbs != nil && dbs.main != nil {
			_ = dbs.main.Close()
		}
	})

	bundle, err := BuildRepoBundle(context.Background(), cfg, dbs, log)
	require.NoError(t, err)
	require.NotNil(t, bundle)
	require.NotNil(t, bundle.ScriptsRepo)
	require.NotNil(t, bundle.ImageRepo)
	require.NotNil(t, bundle.ClipsRepo)
	require.NotNil(t, bundle.Assets)
	require.NotNil(t, bundle.MonitorsRepo)
	require.NotNil(t, bundle.VoiceoverRepo)
	require.NotNil(t, bundle.CatalogRepo)
	require.NotNil(t, bundle.SQRepo)
}

// TestComposition_NilObligatory_BuildSearchBundle tests BuildSearchBundle
// in isolation with a BuildRepoBundle-built deps. Validates that the
// canonical SearchBundle wiring (asset index + tree + resolver + provider
// registry) is preserved under a minimal config.
func TestComposition_NilObligatory_BuildSearchBundle(t *testing.T) {
	chdirToProjectRoot(t)

	dataDir := t.TempDir()
	cfg := minimalConfig(dataDir)
	log := zaptest.NewLogger(t)

	dbs, err := initDatabases(cfg, log)
	require.NoError(t, err)
	t.Cleanup(func() {
		if dbs != nil && dbs.main != nil {
			_ = dbs.main.Close()
		}
	})

	repos, err := BuildRepoBundle(context.Background(), cfg, dbs, log)
	require.NoError(t, err)
	require.NotNil(t, repos)

	bundle, err := BuildSearchBundle(context.Background(), cfg, dbs, log, repos)
	require.NoError(t, err)
	require.NotNil(t, bundle)
	require.NotNil(t, bundle.AssetIndexService)
	require.NotNil(t, bundle.AssetTreeService)
	require.NotNil(t, bundle.AssetResolver)
	require.NotNil(t, bundle.ProviderRegistry)
}

// TestComposition_NilObligatory_BuildJobsBundle cross-validates the
// canonical TestBuildJobsBundle_FieldsAreNonNil so a regression in either
// file is detected by both. Drift between the two is a signal that one
// side has been updated without the other.
func TestComposition_NilObligatory_BuildJobsBundle(t *testing.T) {
	chdirToProjectRoot(t)

	// PG-011 typed-handle migration (June 2026): the fixture is
	// *storage.SQLiteDB; the underlying *sql.DB handle is reached via
	// the embedded field (.DB) so BuildJobsBundle — which still takes
	// a raw handle — receives the same connection without leaking
	// the database/sql import into this file.
	sqliteDB, err := storage.OpenSQLiteDB(":memory:", zaptest.NewLogger(t))
	require.NoError(t, err, "open SQLiteDB")
	t.Cleanup(func() { _ = sqliteDB.Close() })

	// PG-011 typed-handle migration (June 2026): BuildJobsBundle
	// signature is now `*storage.SQLiteDB`, so we pass the typed
	// handle directly. Underlying *sql.DB is reached via the
	// embedded `.DB` accessor only for callers (e.g.
	// clipindexer.NewService) that have not yet been migrated.
	bundle, err := BuildJobsBundle(sqliteDB, zaptest.NewLogger(t), nil, nil, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, bundle)
	require.NotNil(t, bundle.Repo)
	require.NotNil(t, bundle.Dispatcher)
	require.NotNil(t, bundle.Service)
	require.NotNil(t, bundle.Facade)
}

// ── 2. no-goroutine-spawned ──────────────────────────────────────────────

// Frozen counts (June 2026, PR4.C). When a future PR4.E+ wave moves
// the inline goroutine spawns out of Build*Bundle into lifecycle.go,
// decrement these constants and re-run the suite. A growing count means
// someone added a new spawn to a Build*Bundle body — that is the bug
// this test is here to catch.
//
// Wave A Item 15 (June 2026): BuildDriveBundle moved from the documented-
// spawn list into the zero-spawn list. The legacy "drive-style-folders"
// concurrent.SafeGo goroutine was DELETED outright (not just moved) —
// the build_drive_startup.go closure that used to host it now only
// validates Drive folders + creates local storage dirs. BuildDriveBundle
// body is side-effect-free, and the test freeze table below treats it
// like every other builder in frozenZeroSpawnBuilders.
const (
	// Wave A Item 15 (June 2026): the legacy ensureStyleDriveFolders
	// + concurrent.SafeGo("drive-style-folders", ...) site has been
	// REMOVED (not moved — the legacy style-folder pre-creation was
	// the wrong composition-time concern entirely). BuildDriveBundle
	// body has zero spawns and now belongs in frozenZeroSpawnBuilders.
	// The constant is retained only as a structural marker; the
	// assertion below uses it for sanity (`counts["BuildDriveBundle"]
	// should equal zero`).
	frozenGoroutineInBuildDriveBundle = 0
	// PR9-B (June 2026): concurrent.SafeGo x2 (pool start + shutdown)
	// moved from BuildOutboxBundle body into the standalone
	// startOutboxEventsPool function.
	frozenGoroutineInBuildOutboxBundle = 0
)

// frozenZeroSpawnBuilders lists the Build*Bundle family members that
// MUST have zero goroutine spawns in their bodies. The list is
// explicitly enumerated (not derived) so adding a new builder without
// declaring it is a test failure that forces the author to either
// declare zero spawns or move the spawn to lifecycle.go.
//
// Wave A Item 15 (June 2026): BuildDriveBundle added to this list — the
// drive-style-folders SafeGo goroutine was deleted outright, so
// BuildDriveBundle is structurally indistinguishable from every other
// builder in the family.
var frozenZeroSpawnBuilders = []string{
	"BuildRepoBundle",
	"BuildSearchBundle",
	"BuildDriveBundle",
	"BuildProcessBundle",
	"BuildAIBundle",
	"BuildDomainBundle",
	"BuildJobsBundle",
	"BuildOutboxBundle",
	"BuildSyncBundle",
	"BuildMaintBundle",
	"BuildUtilityBundle",
	"BuildStockBundle",
}

// Regex detectors used to enumerate and count goroutine-spawn sites
// inside Build*Bundle bodies. Source-level (not runtime goroutine
// count) so the test is deterministic across CI runners and refactors.
//
//   - goStmtRegex: matches both bare `go <ident>` statements and
//     `go func() { ... }()` literal expressions but does NOT match
//     `goto <label>` (no space after `go`).
//   - buildFuncRegex: enumerates every `func Build*Bundle(<args>)`
//     entry across all scanned source files.
var (
	goStmtRegex    = regexp.MustCompile(`(?m)^\s*go\s+(?:\w|func\()`)
	buildFuncRegex = regexp.MustCompile(`(?m)^\s*func\s+(Build\w+Bundle)\(`)
)

// TestComposition_NoGoroutinesSpawned_FrozenSiteCount asserts that the
// number of goroutine-spawn statements inside each Build*Bundle body
// matches the frozen count above. We inspect source-level regex matches
// (not runtime goroutine count) so the test is deterministic across
// CI runners and refactors alike.
//
// Regex used: `(?m)^\s*go\s+(\w|func\()` matches both bare `go <ident>`
// statements and `go func() { ... }()` literal expressions but does NOT
// match `goto <label>` (no space after `go`). The regex is declared
// alongside buildFuncRegex in the var (...) block above; the lone
// `var goStmtRegex = ...` was the original declaration and was removed
// when this section was restructured for cross-file enumeration.
//
// TestComposition_NoGoroutinesSpawned_FrozenSiteCount asserts the freeze
// semantics described above by scanning ALL non-test .go files in
// internal/app/ via buildFuncRegex. This way the test is robust to
// future file moves (e.g. BuildJobsBundle lives in module_media.go, not
// composition.go) without re-listing source paths.
func TestComposition_NoGoroutinesSpawned_FrozenSiteCount(t *testing.T) {
	chdirToProjectRoot(t)

	files := compositionBundleSourceFiles(t)

	// Per-builder spawn count summed across all source files. Since
	// each builder is defined exactly once, the map has one entry per
	// builder discovered.
	counts := make(map[string]int)
	for _, file := range files {
		src := readSourceSilent(file)
		for _, m := range buildFuncRegex.FindAllStringSubmatch(src, -1) {
			counts[m[1]] += countGoroutineSpawns(m[1], src)
		}
	}

	// Documented spawn sites: per-builder frozen expectations for the
	// sites that currently exist (today: OutboxBundle only — DriveBundle
	// migrated into frozenZeroSpawnBuilders via Wave A Item 15).
	require.Equal(t, frozenGoroutineInBuildDriveBundle, counts["BuildDriveBundle"],
		"BuildDriveBundle spawn count drifted (expected 0 after Wave A Item 15, June 2026 — the drive-style-folders SafeGo goroutine was REMOVED, not just moved).")
	require.Equal(t, frozenGoroutineInBuildOutboxBundle, counts["BuildOutboxBundle"],
		"BuildOutboxBundle spawn count drifted. Migrating outbox-pool SafeGo x2 to lifecycle.go is documented in lifecycle.go comment line ~294; update `frozenGoroutineInBuildOutboxBundle` in lockstep.")

	// The remaining family members MUST have zero spawns.
	for _, name := range frozenZeroSpawnBuilders {
		require.Equal(t, 0, counts[name],
			"%s must have zero goroutine spawns in its body; recorded spawn count = %d",
			name, counts[name])
	}

	// Every builder discovered in source must appear in the freeze
	// table — either the documented-spawn list or the zero-spawn list.
	// A new builder that is neither is an undocumented drift.
	known := map[string]bool{
		"BuildDriveBundle":  true,
		"BuildOutboxBundle": true,
	}
	for _, name := range frozenZeroSpawnBuilders {
		known[name] = true
	}
	for name := range counts {
		require.True(t, known[name],
			"uncovered Build*Bundle %q — add it to either frozenZeroSpawnBuilders (if 0 spawns) or to the documented-spawn block (if it currently spawns in body)",
			name)
	}
}

// compositionBundleSourceFiles returns the relative paths of every
// non-test Go source file in internal/app/ that may host a Build*Bundle
// definition. Test files (*_test.go) are excluded so fixtures do not
// pollute spawn counts. The list is sorted for deterministic iteration.
func compositionBundleSourceFiles(t *testing.T) []string {
	t.Helper()
	pattern := "internal/app/*.go"
	files, err := filepath.Glob(pattern)
	require.NoError(t, err, "glob composition sources")
	out := make([]string, 0, len(files))
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		out = append(out, f)
	}
	sort.Strings(out)
	require.NotEmpty(t, out, "no composition source files matched — chdir to project root failed?")
	return out
}

// ── 3. freeze-ordering ───────────────────────────────────────────────────

// ── 5. PR 4 single-runtime invariants (refactor/single-qdrant-runtime) ─

// frozenQdrantClientSites pins PR 4 (June 2026,
// refactor/single-qdrant-runtime) section #2 acceptance criterion:
// exactly ONE qdrant.NewClient(...) call in production composition paths
// (internal/app/*.go excluding _test.go). The QdrantRuntime facade is the
// single construction site; BuildProcessBundle + buildQdrantDeps both
// source from qd.Runtime.Client after the refactor.
//
// If a future PR adds a second qdrant.NewClient(...) call in production
// paths, the test fails and the author must either:
// (a) Promote the new client into QdrantRuntime via a new field +
//
//	builder method (preferred); or
//
// (b) Update frozenQdrantClientSites and document the rationale
//
//	in the PR description (e.g. a sidecar that cannot share the
//	canonical runtime because of Https-only Network config).
const frozenQdrantClientSites = 1

// frozenQdrantDefaultV3SchemaSites pins PR 4 section #2 acceptance
// criterion: exactly ONE qdrant.DefaultV3Schema() call in production
// composition paths. The QdrantRuntime facade is the single source;
// ProcessBundle, BuildOutboxBundle, IndexWriter, CollectionManager all
// share runtime.Schema.
const frozenQdrantDefaultV3SchemaSites = 1

// frozenQdrantNewRuntimeSites pins PR 4: exactly ONE qdrant.NewRuntime
// construction site. composition.go::buildQdrantDeps is the canonical
// caller; BuildProcessBundle reads the same runtime via qd.Runtime.
const frozenQdrantNewRuntimeSites = 1

// TestComposition_FrozenClientConstructionSites verifies that qdrant.NewClient(...)
// is called from exactly one site in production composition paths. The
// pre-PR4 state had TWO sites (composition.go::buildQdrantDeps +
// build_bundles_process.go::BuildProcessBundle) — source-level count
// was 2. After PR 4 QdrantRuntime.NewRuntime owns the single site.
func TestComposition_FrozenClientConstructionSites(t *testing.T) {
	chdirToProjectRoot(t)

	files := compositionBundleSourceFiles(t)
	matches := 0
	for _, f := range files {
		src, _ := os.ReadFile(f)
		matches += strings.Count(string(src), "qdrant.NewClient(")
	}
	require.Equalf(t, frozenQdrantClientSites, matches,
		"PR 4: qdrant.NewClient(%d expected call sites in internal/app/*.go; found %d. QdrantRuntime.NewRuntime is the single construction site. Add the subsystem to QdrantRuntime rather than constructing a second Client — see composition_test.go::TestComposition_FrozenClientConstructionSites for the acceptance criterion.",
		frozenQdrantClientSites, matches)
}

// TestComposition_FrozenIndexSchemaSites verifies that qdrant.DefaultV3Schema()
// is called from exactly one site in production composition paths. The
// pre-PR4 state had TWO sites (composition.go::buildQdrantDeps +
// build_bundles_process.go::BuildProcessBundle) — both constructed
// their own IndexSchema, so the schema-version ratchet could drift
// between the two (one might pin V3 + sparse, the other V3 + dense-only).
// After PR 4 the runtime holds a single Schema instance shared by
// every subsystem.
func TestComposition_FrozenIndexSchemaSites(t *testing.T) {
	chdirToProjectRoot(t)

	files := compositionBundleSourceFiles(t)
	matches := 0
	for _, f := range files {
		src, _ := os.ReadFile(f)
		matches += strings.Count(string(src), "qdrant.DefaultV3Schema(")
	}
	require.Equalf(t, frozenQdrantDefaultV3SchemaSites, matches,
		"PR 4: qdrant.DefaultV3Schema(%d expected call site in internal/app/*.go; found %d. QdrantRuntime.NewRuntime is the single construction site. Promote schema wiring through runtime.Schema rather than calling DefaultV3Schema twice.",
		frozenQdrantDefaultV3SchemaSites, matches)
}

// TestComposition_FrozenNewRuntimeSites verifies that qdrant.NewRuntime(...)
// is called from exactly one site in production composition paths.
// composition.go::buildQdrantDeps is the canonical constructor.
// BuildProcessBundle + BuildOutboxBundle + any future bundle must
// source the runtime via qd.Runtime, never build their own.
func TestComposition_FrozenNewRuntimeSites(t *testing.T) {
	chdirToProjectRoot(t)

	files := compositionBundleSourceFiles(t)
	matches := 0
	for _, f := range files {
		src, _ := os.ReadFile(f)
		matches += strings.Count(string(src), "qdrant.NewRuntime(")
	}
	require.Equalf(t, frozenQdrantNewRuntimeSites, matches,
		"PR 4: qdrant.NewRuntime(%d expected call site in internal/app/*.go; found %d. composition.go::buildQdrantDeps is the canonical constructor; other bundles must read runtime via QdrantDeps.Runtime rather than re-constructing.",
		frozenQdrantNewRuntimeSites, matches)
}

// TestComposition_FrozenQdrantHealthProbeAny verifies PR 4 section #6
// acceptance criterion: `QdrantHealthProbe any` (the loose type-assertion
// escape hatch) is gone. After PR 4 ProcessBundle.QdrantHealthProbe is
// concretely typed as *qdrant.HealthProbe.
func TestComposition_FrozenQdrantHealthProbeAny(t *testing.T) {
	chdirToProjectRoot(t)

	files := compositionBundleSourceFiles(t)
	matches := 0
	for _, f := range files {
		src, _ := os.ReadFile(f)
		matches += strings.Count(string(src), "QdrantHealthProbe any")
	}
	require.Equalf(t, 0, matches,
		"PR 4: `QdrantHealthProbe any` must NOT appear in internal/app/*.go (acceptance criterion #2 of #6). Replace with *qdrant.HealthProbe concrete; the compile-time assertion in internal/infrastructure/qdrant/health.go pins the Probe contract.")
}

// TestComposition_FrozenVectorPointDeleterPort verifies PR 4 acceptance
// criterion #1 of #6: there is exactly ONE VectorPointDeleter port
// definition in internal/, declared in
// internal/application/jobs/outbox/ports.go per AGENTS.md Pattern 0.
//
// The previous state had TWO duplicate `QdrantDeleter` interfaces
// (one in internal/infrastructure/qdrant/types.go and one local to
// internal/application/jobs/outbox/index_delete.go) — both deleted
// by PR 4. The source-level search here uses the concrete port name
// to canonicalise the count.
func TestComposition_FrozenVectorPointDeleterPort(t *testing.T) {
	chdirToProjectRoot(t)

	src, err := os.ReadFile("internal/application/jobs/outbox/ports.go")
	require.NoError(t, err, "read canonical VectorPointDeleter port file")
	matches := strings.Count(string(src), "type VectorPointDeleter interface")
	require.Equalf(t, 1, matches,
		"PR 4: exactly 1 `type VectorPointDeleter interface` declaration in internal/application/jobs/outbox/ports.go; found %d. The canonical port lives in the application layer per AGENTS.md Pattern 0.",
		matches)
}

// ── 3. freeze-ordering ─────────────────────────────────────────────────

// frozenCompositionSequence is the canonical NewComposition build order.
// The order encodes the dependency graph; runs left-to-right produce a
// fully-resolved ComposeRoot. Any reordering instruction that does not
// also update this slice is a refactor regression the test will flag.
//
// PR-12d (June 2026): Domain and Outbox swapped so the canonical
// outbox dispatcher is available at the moment images.Service is
// constructed (closing the SetDispatcher late-bind ordering hazard).
// PR 8 (June 2026, codex/qdrant-app-writers-fail-closed): Outbox +
// Process swapped (Outbox now BEFORE Process) and a new buildQdrantDeps
// pre-phase introduced to feed BuildOutboxBundle's ClipIndexerService +
// QdrantDeleter deps. Confirmed DAG:
//
//	qdrantDeps(no deps) -> outbox(reads qd) -> process(reads outbox+qd) ->
//	  domains(reads process+outbox) -> sync/maint/utility.
var frozenCompositionSequence = []string{
	"BuildRepoBundle(",
	"BuildSearchBundle(",
	"BuildDriveBundle(",
	"BuildJobsBundle(",
	"BuildAIBundle(",
	"buildQdrantDeps(",
	"BuildOutboxBundle(",
	"BuildProcessBundle(",
	"BuildDomainBundle(",
	"BuildSyncBundle(",
	"BuildMaintBundle(",
	"BuildUtilityBundle(",
}

// TestComposition_FreezeOrdering_BuildSequence asserts that the
// Build*Bundle calls inside NewComposition occur in the order declared
// in frozenCompositionSequence. Restricted to the NewComposition body
// so a `BuildRepoBundle` reference in a doc comment does not pollute.
func TestComposition_FreezeOrdering_BuildSequence(t *testing.T) {
	chdirToProjectRoot(t)

	source, err := os.ReadFile(compositionSourcePath)
	require.NoError(t, err, "read composition.go")
	text := string(source)

	start := strings.Index(text, "func NewComposition(")
	require.GreaterOrEqual(t, start, 0, "NewComposition entry not found in composition.go")
	end := findNextTopLevelFuncEnd(text, start)
	body := text[start:end]

	prev := -1
	for _, name := range frozenCompositionSequence {
		idx := strings.Index(body, name)
		require.Greater(t, idx, prev,
			"%s appears out-of-order or absent in NewComposition body (last index=%d, this index=%d); reorder+update frozenCompositionSequence in lockstep or this signals a dependency-graph regression.",
			name, prev, idx)
		prev = idx
	}
}

// ── 4. PR 3 fail-closed invariants (fix/qdrant-outbox-fail-closed) ──────

// TestComposition_QdrantEnabledNoClipIndexer_WriterAndDeleterWired
// pins the RED-POINT-direction B fail-closed contract (PR-QDRANT-CONFIG-
// MISMATCH-GATE, arch/current.yaml#QDRANT-CHAIN-VERIFY-2026-07-04.linked_issues[0]):
// with cfg.Qdrant.Enabled=true AND cfg.ClipIndexer.Enabled=false,
// buildQdrantDeps MUST abort boot with a terminal error rather than
// silently constructing a QdrantRuntime that the outbox IndexingHandler
// cannot drive (IndexClip short-circuits when ClipIndexer is disabled).
//
// Prior: PR 3 (#3 from verdict Qdrant, June 2026) wrote this test to pin
// the OPPOSITE behaviour — "buildQdrantDeps must return a non-nil
// QdrantDeps and a non-nil QdrantDeleter even when ClipIndexer is
// disabled" — because the IndexDeleteHandler delete path needed its
// mandatory VectorPointDeleter slot wired. The runtime-correct outcome
// of that concern (Qdrant=true + ClipIndexer=false) was a half-built
// Qdrant stack: QDRANT runtime was constructed (delete-path was valid)
// but IndexWrite was unwired on the sidecar (the AI indexing chain).
//
// Now: PR-QDRANT-CONFIG-MISMATCH-GATE (July 2026) promotes the godlike/07
// no-fake-availability contract: a Qdrant+ClipIndexer mismatch is a
// misconfiguration that MUST be surface at boot, NOT absorbed as a
// half-built capability that silently degrades at first
// asset.index.requested event. The half-built shape (PR 3 #3) is the
// false-success path; the QDRANT-CHAIN-VERIFY-2026-07-04 audit closed
// it. This test pins the new contract: buildQdrantDeps returns
// (nil, error) so the operator's cfg is rejected loudly with
// actionable env-var hints. Direction A (ClipIndexer=true +
// Qdrant=false) is covered by TestComposition_ClipIndexerEnabledNoQdrant_FailClosed
// below — both directions fail-closed through the SAME canonical helper
// internal/app/build_bundles_qdrant_gates.go::validateQdrantIndexerCompatibility.
func TestComposition_QdrantEnabledNoClipIndexer_WriterAndDeleterWired(t *testing.T) {
	chdirToProjectRoot(t)

	dataDir := t.TempDir()
	cfg := minimalConfig(dataDir)
	cfg.Qdrant.Enabled = true       // Qdrant feature ON
	cfg.ClipIndexer.Enabled = false // sidecar OFF (the RED-POINT under test)
	log := zaptest.NewLogger(t)

	dbs, err := initDatabases(cfg, log)
	require.NoError(t, err, "initDatabases")
	t.Cleanup(func() {
		if dbs != nil && dbs.main != nil {
			_ = dbs.main.Close()
		}
	})

	qd, err := buildQdrantDeps(context.Background(), cfg, dbs, log)
	require.Error(t, err,
		"PR-QDRANT-CONFIG-MISMATCH-GATE: cfg.Qdrant.Enabled=true + cfg.ClipIndexer.Enabled=false must abort buildQdrantDeps (terminal fail-closed at composition root; godlike/07 no-fake-availability)")
	require.Nil(t, qd,
		"PR-QDRANT-CONFIG-MISMATCH-GATE: buildQdrantDeps must return nil QdrantDeps alongside the error (fail-closed, no partial-bundle shape that the QDRANT-CHAIN-VERIFY-2026-07-04 audit identified as false-success)")

	// 5-substring canonical contract (godlike/07 fail-closed coupling).
	require.Contains(t, err.Error(), "QdrantEnabled=true",
		"error must name the failing condition (the Qdrant feature flag was on)")
	require.Contains(t, err.Error(), "ClipIndexerEnabled=false",
		"error must name the failing field (the ClipIndexer sidecar was off)")
	require.Contains(t, err.Error(), "QDRANT-CHAIN-VERIFY-2026-07-04 P0",
		"error must cite the wave-tracker anchor for audit traceability")
	require.Contains(t, err.Error(), "VELOX_FEATURE_CLIP_INDEXER_ENABLED=true",
		"error must name the env-var fix hint (start the AI sidecar)")
	require.Contains(t, err.Error(), "VELOX_FEATURE_QDRANT_ENABLED=false",
		"error must name the alternative env-var fix hint (disable the vector store)")
}

// TestComposition_ClipIndexerEnabledNoQdrant_FailClosed pins
// BLOCKER #3 (Qdrant Verdetto, July 2026): with cfg.ClipIndexer.Enabled=true
// but cfg.Qdrant.Enabled=false, buildQdrantDeps MUST abort boot with a
// terminal error. The ClipIndexer vector-store write path requires Qdrant
// for UpsertVectorStore completion; without it, every indexing job would
// dead-letter on the nil vectorStore or silently skip the Qdrant write.
// The previous code merely logged a warning and continued, which produced
// a false-success path (asset marked INDEXED but never actually written
// to Qdrant). Fail-closed at composition time per godlike/07.
func TestComposition_ClipIndexerEnabledNoQdrant_FailClosed(t *testing.T) {
	chdirToProjectRoot(t)

	dataDir := t.TempDir()
	cfg := minimalConfig(dataDir)
	cfg.ClipIndexer.Enabled = true // ClipIndexer ON
	cfg.Qdrant.Enabled = false     // Qdrant OFF — the BLOCKER #3 trigger
	log := zaptest.NewLogger(t)

	dbs, err := initDatabases(cfg, log)
	require.NoError(t, err, "initDatabases")
	t.Cleanup(func() {
		if dbs != nil && dbs.main != nil {
			_ = dbs.main.Close()
		}
	})

	qd, err := buildQdrantDeps(context.Background(), cfg, dbs, log)
	require.Error(t, err,
		"PR-QDRANT-CONFIG-MISMATCH-GATE (Direction A): cfg.ClipIndexer.Enabled=true + cfg.Qdrant.Enabled=false must abort buildQdrantDeps (terminal fail-closed at composition root)")
	require.Nil(t, qd,
		"PR-QDRANT-CONFIG-MISMATCH-GATE: buildQdrantDeps must return nil QdrantDeps alongside the error (fail-closed, no partial bundle)")
	// Substring contract (godlike/07 fail-closed coupling). The substring
	// names follow the canonical camelCase format from
	// internal/app/build_bundles_qdrant_gates.go::validateQdrantIndexerCompatibility
	// — operators can grep `QDRANT-CHAIN-VERIFY-2026-07-04 P0` in the boot
	// log to identify the failing path. Per godlike/06 SSOT one-owner-per-
	// fact: the helper is the SOLE canonical source of this error envelope.
	require.Contains(t, err.Error(), "ClipIndexerEnabled=true",
		"error must name the offending flag so operators can grep the boot log and correct config:\n\tgot: %v", err)
	require.Contains(t, err.Error(), "QdrantEnabled=false",
		"error must name the missing flag so operators can grep the boot log and correct config:\n\tgot: %v", err)
	require.Contains(t, err.Error(), "QDRANT-CHAIN-VERIFY-2026-07-04 P0",
		"error must cite the wave-tracker anchor for audit traceability:\n\tgot: %v", err)
	require.Contains(t, err.Error(), "VELOX_FEATURE_QDRANT_ENABLED=true",
		"error must name the env-var fix hint (start the vector store) so operators can copy/paste:\n\tgot: %v", err)
	require.Contains(t, err.Error(), "VELOX_FEATURE_CLIP_INDEXER_ENABLED=false",
		"error must name the alternative env-var fix hint (disable the sidecar) so operators can copy/paste:\n\tgot: %v", err)
}

// TestComposition_QdrantEnabledMissingAssetDeleter_FailClosed pins
// PR 3 #4 + #5: with cfg.Qdrant.Enabled=true but repos.ClipsRepo=nil,
// BuildOutboxBundle MUST abort boot via RegisterCoreHandlers's
// fail-closed contract. AssetDeleter=nil is one of the four mandatory
// core deps (alongside indexer / SourceVersionQuerier / QdrantDeleter);
// the previous log.Warn("failed to register outbox events handlers")
// silently downgraded the wiring bug to a runtime dead-letter on the
// first asset.index.requested event. The composed error message must
// name the missing dep so operators can grep the boot log.
func TestComposition_QdrantEnabledMissingAssetDeleter_FailClosed(t *testing.T) {
	chdirToProjectRoot(t)

	dataDir := t.TempDir()
	cfg := minimalConfig(dataDir)
	cfg.Qdrant.Enabled = true
	cfg.ClipIndexer.Enabled = true
	log := zaptest.NewLogger(t)

	dbs, err := initDatabases(cfg, log)
	require.NoError(t, err)
	t.Cleanup(func() {
		if dbs != nil && dbs.main != nil {
			_ = dbs.main.Close()
		}
	})

	repos, err := BuildRepoBundle(context.Background(), cfg, dbs, log)
	require.NoError(t, err)

	qd, err := buildQdrantDeps(context.Background(), cfg, dbs, log)
	require.NoError(t, err)
	require.NotNil(t, qd.QdrantDeleter)

	jobsBundle, err := BuildJobsBundle(dbs.main, log, nil, nil, nil, nil)
	require.NoError(t, err)

	// Force a nil AssetDeleter dep at the BuildOutboxBundle call site
	// by zeroing ClipsRepo. The fact that the *assets.ClipsRepository
	// also implements SourceVersionQuerier means BOTH gates fire here,
	// which is fine: the first required-dep error is still surfaced,
	// and the fail-closed semantic (BuildOutboxBundle returning err)
	// is the contract under test, regardless of WHICH dep was first
	// to trigger.
	repos.ClipsRepo = nil

	_, _, err = BuildOutboxBundle(context.Background(), cfg, dbs, log, repos, qd, jobsBundle, nil)
	require.Error(t, err,
		"PR 3: cfg.Qdrant.Enabled=true + nil ClipsRepo must abort BuildOutboxBundle (fail-closed at boot, never warn-as-warning)")
	require.Contains(t, err.Error(), "core outbox handlers",
		"error must originate from RegisterCoreHandlers so operators grep distinctively:\n\tgot: %v", err)
}

// PR 7 #7.1 — chore/remove-qdrant-legacy freeze test.
// The legacy internal/infrastructure/qdrant/reconciler.go (and
// its now-absent reconciler_test.go) were deleted. This test
// pins both the file-existence AND zero-stale-callers invariant
// so future commits cannot silently re-introduce the type.
//
// The canonical reconciler lives at
// internal/application/qdrant/reconciler/.
func TestComposition_FrozenQdrantReconcilerDeleted(t *testing.T) {
	chdirToProjectRoot(t)

	// (1) The deleted file path must NOT exist.
	const reconcilerPath = "internal/infrastructure/qdrant/reconciler.go"
	if _, err := os.Stat(reconcilerPath); err == nil {
		t.Fatalf("PR 7 #7.1: %s must NOT exist (deleted in chore/remove-qdrant-legacy; canonical path is internal/application/qdrant/reconciler/)", reconcilerPath)
	} else if !os.IsNotExist(err) {
		t.Fatalf("PR 7 #7.1: unexpected stat error for %s: %v", reconcilerPath, err)
	}

	// (2) Zero call sites of the OLD constructor in internal/app/*.go.
	// Any match means a caller was reintroduced without restoring
	// the deleted file (build would break on `go build ./...`).
	files := compositionBundleSourceFiles(t)
	newReconcilerCalls := 0
	for _, f := range files {
		src, _ := os.ReadFile(f)
		newReconcilerCalls += strings.Count(string(src), "qdrant.NewReconciler(")
	}
	require.Equalf(t, 0, newReconcilerCalls,
		"PR 7 #7.1: `qdrant.NewReconciler` call sites in internal/app/*.go must equal 0; found %d. The OLD reconciler was deleted; callers must migrate to internal/application/qdrant/reconciler/.",
		newReconcilerCalls)
}

// PR 6 #1 (refactor/qdrant-index-document) — canonical Qdrant
// wire-shape freeze test. The IndexDocument / IndexedMetadata /
// EmbeddingArtifact / VectorChannel types declared in
// internal/infrastructure/qdrant/index_document.go are the canonical
// airlock vocabulary. If any of these MOVE or get RENAMED, update
// this test AND index_document.go's doctrine comment together.
//
// The forbidden-fields SSOT (ForbiddenIndexDocumentFields) lives in
// the same file; a future PR adding a new forbidden field must update
// BOTH the slice declaration AND this freeze-test marker.
func TestComposition_FrozenQdrantIndexDocumentCanonicalTypes(t *testing.T) {
	chdirToProjectRoot(t)

	const file = "internal/infrastructure/qdrant/index_document.go"
	src, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("PR 6 #1: canonical wire-shape file %s must exist (created in refactor/qdrant-index-document): %v", file, err)
	}

	body := string(src)

	// Exactly-one declaration of each canonical type — a future split
	// across multiple files MUST update this freeze-test alongside.
	for _, marker := range []string{
		"type IndexDocument struct",
		"type VectorChannel string",
		"type EmbeddingArtifact struct",
		"type IndexedMetadata struct",
		"var ForbiddenIndexDocumentFields = []string{",
	} {
		n := strings.Count(body, marker)
		require.Equalf(t, 1, n,
			"PR 6 #1: marker %q expected exactly once in %s; found %d. If you ADDED/RENAMED a canonical type, update this freeze-test AND index_document.go's doctrine comment in lockstep.",
			marker, file, n)
	}

	// The canonical VectorChannel constants (ChannelText, etc.) must
	// each be declared exactly once. The marker pattern matches
	// "\tConstName VectorChannel" inside the const block.
	for _, ch := range []string{"ChannelText", "ChannelTranscript", "ChannelVisual", "ChannelAudio", "ChannelBM25Text"} {
		pattern := "\t" + ch + " VectorChannel"
		n := strings.Count(body, pattern)
		require.Equalf(t, 1, n,
			"PR 6 #1: canonical channel constant %q expected exactly once in %s; found %d. The channel vocabulary is the writer↔search-adapter contract.",
			ch, file, n)
	}

	// BuildPayloadFromDocument (the canonical writer-side payload
	// emitter) must exist exactly once in payload_mapper.go.
	b, err := os.ReadFile("internal/infrastructure/qdrant/payload_mapper.go")
	require.NoError(t, err, "read canonical qdrant payload mapper")
	emitterCount := strings.Count(string(b), "func BuildPayloadFromDocument")
	require.Equalf(t, 1, emitterCount,
		"PR 6 #1: exactly 1 `func BuildPayloadFromDocument` declaration expected in internal/infrastructure/qdrant/payload_mapper.go; found %d. The canonical writer-side payload emitter lives in payload_mapper.go.",
		emitterCount)

	// The forbidden-fields SSOT slice must contain EXACTLY the SSOT
	// markers (3 entries today: Status, DriveLink, LocalPath). If a
	// future PR adds a new forbidden field, append it to the slice
	// in index_document.go AND update this freeze-test list.
	for _, forbidden := range []string{"\"Status\"", "\"DriveLink\"", "\"LocalPath\""} {
		n := strings.Count(body, forbidden)
		require.Equalf(t, 1, n,
			"PR 6 #1: forbidden field %q expected exactly once in ForbiddenIndexDocumentFields SSOT; found %d",
			forbidden, n)
	}
}

// ── Task 3: AssetIDToQdrantPointID single-declaration gate ──────────────

// frozenAssetIDToQdrantPointIDSites pins Task 3 (July 2026): exactly ONE
// production declaration of AssetIDToQdrantPointID exists in the codebase.
// The canonical implementation is UUID v5 SHA-1 in
// internal/infrastructure/qdrant/schema/pointid.go. All callers MUST route
// through this single function; ad-hoc point ID generation is forbidden.
const frozenAssetIDToQdrantPointIDSites = 1

// TestComposition_AssetIDToQdrantPointID_SingleDeclaration verifies
// Task 3's invariant: exactly 1 `func AssetIDToQdrantPointID` production
// declaration exists. Walks the entire internal/ tree (filepath.WalkDir
// is used instead of filepath.Glob because Go's Glob does NOT support **
// recursive matching). Excludes _test.go files (test stubs like the
// reconciler's canonicalPointID helper are allowed). The aliases.go
// redirect `var AssetIDToQdrantPointID = schema.AssetIDToQdrantPointID`
// is NOT a declaration — the check only matches `func AssetIDToQdrantPointID`.
func TestComposition_AssetIDToQdrantPointID_SingleDeclaration(t *testing.T) {
	chdirToProjectRoot(t)

	matches := 0
	var matchFiles []string
	err := filepath.WalkDir("internal", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || strings.HasSuffix(path, "_test.go") || !strings.HasSuffix(path, ".go") {
			return nil
		}
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		if strings.Contains(string(src), "func AssetIDToQdrantPointID") {
			matches++
			matchFiles = append(matchFiles, path)
		}
		return nil
	})
	require.NoError(t, err, "walkDir internal tree")
	require.Equalf(t, frozenAssetIDToQdrantPointIDSites, matches,
		"Task 3: exactly %d `func AssetIDToQdrantPointID` production declaration expected; found %d in: %v. The canonical UUID v5 SHA-1 function lives in internal/infrastructure/qdrant/schema/pointid.go. All callers must route through it — never create ad-hoc point ID generation.",
		frozenAssetIDToQdrantPointIDSites, matches, matchFiles)
}

// ── Brace-counting helpers (test-private) ────────────────────────────────

// countGoroutineSpawns returns the number of goroutine-spawn statements
// in the body of `func <name>(...) {...}` in the given source text.
// Returns -1 if the function entry is not found (caller treats as test
// drift; should never happen for the Build*Bundle family in composition.go).
func countGoroutineSpawns(name, text string) int {
	start := strings.Index(text, "func "+name+"(")
	if start < 0 {
		return -1
	}
	end := findNextTopLevelFuncEnd(text, start)
	body := text[start:end]
	goCount := len(goStmtRegex.FindAllString(body, -1))
	safeGoCount := strings.Count(body, "concurrent.SafeGo(")
	return goCount + safeGoCount
}

// readSourceSilent reads a file relative to the project root and
// silently swallows errors — used in goroutine-counting tests that
// surface a drift signal via the count itself rather than a panic.
func readSourceSilent(relPath string) string {
	b, _ := os.ReadFile(relPath)
	return string(b)
}

// findNextTopLevelFuncEnd returns the byte index right after the
// closing brace that terminates the function whose `func <name>(` token
// starts at offset `start`. Brace counting is naive (does not skip
// strings/comments) but the composition.go source has no unbalanced
// braces inside string literals, so it is safe for this test file.
func findNextTopLevelFuncEnd(text string, start int) int {
	depth := 0
	for i := start; i < len(text); i++ {
		switch text[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return len(text)
}
