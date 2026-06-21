// Package app — composition tests (PR4.C, June 2026).
//
// This file freezes the structural invariants of the post-PR4 bundle
// decomposition in composition.go. It complements (does NOT replace):
//   - wire_test.go: end-to-end smoke via WireServices (no per-bundle field
//     assertions, no goroutine assertions, no ordering assertions).
//   - module_jobs_test.go: focused BuildJobsBundle field + nil-input checks.
//
// What this file adds:
//   1. TestComposition_NilObligatory_NewComposition — every bundle in the
//      assembled ComposeRoot is non-nil; canary fields in each bundle are
//      populated under a minimal config (no Drive/Artlist/Youtube features).
//   2. TestComposition_NilObligatory_Build*Bundle — per-builder nil-field
//      invariants for the leaf builders that can be exercised without
//      external services (RepoBundle, SearchBundle, JobsBundle).
//   3. TestComposition_NoGoroutinesSpawned_FrozenSiteCount — source-level
//      assertion that the goroutine-spawn sites inside Build*Bundle bodies
//      match the frozen count. Today: 1 site inside BuildDriveBundle +
//      2 sites inside BuildOutboxBundle = 3 total; zero in every other
//      builder. Migrating these to lifecycle.go (the proper home for
//      goroutines that need ctx-bound lifecycle) is a future PR4.E+ wave;
//      updating the frozen constants in this file is the documented signal.
//   4. TestComposition_FreezeOrdering_BuildSequence — source-level
//      assertion that NewComposition's Build*Bundle call order matches the
//      frozen sequence (Repo → Search → Drive → Process → Jobs → AI →
//      Domain → Outbox → Sync → Maint → Utility). The order encodes the
//      dependency graph; any change is a refactor signal.
package app

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
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
		VectorSearch: config.VectorSearchConfig{
			Enabled:         false,
			RealtimeEnabled: false,
		},
		Reranker: config.RerankerConfig{Enabled: false},
		Drive:    config.DriveConfig{},
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

	// AIBundle canaries (5 fields, includes PR4.A-relocated MemoryRepo).
	require.NotNil(t, root.AI.OllamaClient, "root.AI.OllamaClient")
	require.NotNil(t, root.AI.ScriptGen, "root.AI.ScriptGen")
	require.NotNil(t, root.AI.MemoryRepo, "root.AI.MemoryRepo (PR4.A)")
	require.NotNil(t, root.AI.MemoryService, "root.AI.MemoryService")
	require.NotNil(t, root.AI.ScriptEngine, "root.AI.ScriptEngine")

	// JobsBundle canaries (4 fields; cross-validates module_jobs_test.go).
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
// isolation — mirrors the focused-builder pattern in module_jobs_test.go.
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
// canonical module_jobs_test.go::TestBuildJobsBundle_FieldsAreNonNil so a
// regression in either file is detected by both. Drift between the two
// is a signal that one side has been updated without the other.
func TestComposition_NilObligatory_BuildJobsBundle(t *testing.T) {
	chdirToProjectRoot(t)

	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	bundle, err := BuildJobsBundle(db, zaptest.NewLogger(t))
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
const (
	frozenGoroutineInBuildDriveBundle  = 1 // `go ensureStyleDriveFolders(...)`
	frozenGoroutineInBuildOutboxBundle = 2 // concurrent.SafeGo x2 (pool + shutdown)
)

// frozenZeroSpawnBuilders lists the Build*Bundle family members that
// MUST have zero goroutine spawns in their bodies. The list is
// explicitly enumerated (not derived) so adding a new builder without
// declaring it is a test failure that forces the author to either
// declare zero spawns or move the spawn to lifecycle.go.
var frozenZeroSpawnBuilders = []string{
	"BuildRepoBundle",
	"BuildSearchBundle",
	"BuildProcessBundle",
	"BuildAIBundle",
	"BuildDomainBundle",
	"BuildJobsBundle",
	"BuildSyncBundle",
	"BuildMaintBundle",
	"BuildUtilityBundle",
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
// match `goto <label>` (no space after `go`).
var goStmtRegex = regexp.MustCompile(`(?m)^\s*go\s+(?:\w|func\()`)

// TestComposition_NoGoroutinesSpawned_FrozenSiteCount asserts the freeze
// semantics described above by scanning ALL non-test .go files in
// internal/app/ via buildFuncRegex. This way the test is robust to
// future file moves (e.g. BuildJobsBundle lives in module_jobs.go, not
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
	// sites that currently exist (today: DriveBundle + OutboxBundle).
	require.Equal(t, frozenGoroutineInBuildDriveBundle, counts["BuildDriveBundle"],
		"BuildDriveBundle spawn count drifted. Migrating ensureStyleDriveFolders to lifecycle.go is a future PR4.E+ wave; update `frozenGoroutineInBuildDriveBundle` in lockstep.")
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

// frozenCompositionSequence is the canonical NewComposition build order.
// The order encodes the dependency graph; runs left-to-right produce a
// fully-resolved ComposeRoot. Any reordering instruction that does not
// also update this slice is a refactor regression the test will flag.
var frozenCompositionSequence = []string{
	"BuildRepoBundle(",
	"BuildSearchBundle(",
	"BuildDriveBundle(",
	"BuildProcessBundle(",
	"BuildJobsBundle(",
	"BuildAIBundle(",
	"BuildDomainBundle(",
	"BuildOutboxBundle(",
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
