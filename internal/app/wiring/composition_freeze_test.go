// Package app — composition Freeze test bundle.
//
// Push 3.1g (July 2026): split from composition_test.go. The composition
// test family is now 3 files:
//
//   - composition_ordering_test.go: nil-obligatory + BuildSequence +
//     FASE 3 Spina Dorsale ordering + no-goroutines-spawned.
//   - composition_failclosed_test.go: PR-3 + VECTOR-CHAIN-VERIFY
//     fail-closed gates + the 3 dummy types.
//   - composition_freeze_test.go (this file): source-level freeze tests
//     (PR 4 single-vector-runtime, PR 6/7 + Task 3 single-declaration
//     gates) + the package-level doc + shared helpers chdirToProjectRoot
//     and minimalConfig.
//
// Files share funcs implicitly (same Go package). The freeze file is
// the "primary" file (hosts pkg doc + helpers) because it has the
// broadest helper-reuse surface.
package wiring

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	module "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ── Test helpers (package-shared across composition_*_test.go) ─────────────

// compositionSourcePath is the canonical relative path to composition.go
// from the project root. chdirToProjectRoot (below) ensures the cwd is the
// project root so the path resolves consistently regardless of where
// `go test ./internal/app/` is invoked from.
const compositionSourcePath = "internal/app/wiring/composition.go"

// chdirToProjectRoot mirrors the pattern in wire_test.go so nested
// migrations + relative-path resolution work the same way. Package-shared
// across all 3 composition_*_test.go files (same-package Go test scope).
func chdirToProjectRoot(t *testing.T) {
	t.Helper()
	projectRoot := filepath.Join("..", "..", "..")
	origDir, err := os.Getwd()
	require.NoError(t, err, "getwd")
	require.NoError(t, os.Chdir(projectRoot), "chdir to project root")
	t.Cleanup(func() { _ = os.Chdir(origDir) })
}

// minimalConfig returns a config that disables all heavy external features
// background jobs) so NewComposition runs without network round-trips.
// Mirrors the fixture in wire_test.go so composition has the same baseline.
// Package-shared across all 3 composition_*_test.go files.
func minimalConfig(dataDir string) *config.Config {
	return &config.Config{
		Security: config.SecurityConfig{EnableAuth: false},
		Server:   config.ServerConfig{Port: 8080, ReadTimeout: 600, WriteTimeout: 600},
		External: config.ExternalConfig{
			OllamaURL:            "http://localhost:11434",
			OllamaModel:          "llama3.2",
			OllamaTimeoutSeconds: 30,
			// pgvector contract gate (PR-VECTOR-CONFIG-MISMATCH-GATE)
			// demands runtime_model == schema dense model when pgvector is
			// enabled; keep the fixture in the canonical default so
			// vector-enabled composition tests are not pre-broken.
			OllamaEmbedModel: "intfloat/multilingual-e5-base",
		},
		Paths:   config.PathsConfig{},
		Storage: config.StorageConfig{DataDir: dataDir},
		Media: config.MediaConfig{
			Multilingual: config.MultilingualConfig{SourceLanguage: "en"},
		},
		Features: config.FeaturesConfig{
			DriveEnabled:   false,
			YouTubeEnabled: false,
		},
		VLM:         config.VLMConfig{Enabled: false},
		ClipIndexer: config.ClipIndexerConfig{Enabled: false},
		Reranker:    config.RerankerConfig{Enabled: false},
		Drive:       config.DriveConfig{},
		Jobs: config.JobsConfig{
			EnableBackgroundJobs: false, // suppress lifecycle spawners
		},
	}
}

// ── 2. no-goroutine-spawned ───────────────────────────────────────────────

// Frozen counts (June 2026, PR4.C). A growing count means someone added
// a new spawn to a Build*Bundle body — that is the bug this test catches.
// When a future PR4.E+ wave moves inline goroutine spawns out of
// Build*Bundle into go, decrement the constants in lockstep.
const (
	// Wave A Item 15 (June 2026): the legacy drive-style-folders SafeGo
	// goroutine was REMOVED (not moved) — BuildDriveBundle body has zero
	// spawns. Retained only as a structural marker.
	frozenGoroutineInBuildDriveBundle = 0
	// PR9-B (June 2026): concurrent.SafeGo x2 (pool start + shutdown)
	// moved from BuildOutboxBundle body into startOutboxEventsPool.
	// BON-03 decomposition (2026-11-23): the last spawn — the pgvector
	// PostgresIndexWorker drain loop — moved out of the builder body into
	// newOutboxStartClosure, so the builder now spawns nothing at all. The
	// spawn is still pinned, on the closure that owns it.
	frozenGoroutineInBuildOutboxBundle = 0
	// MEDIA-CUTOVER (September 2026): exactly one SafeGo — the pgvector
	// PostgresIndexWorker drain loop (PG mode only), launched from the start
	// closure the composition root runs after boot.
	frozenGoroutineInNewOutboxStartClosure = 1
)

// frozenZeroSpawnBuilders lists the Build*Bundle family members that
// MUST have zero goroutine spawns in their bodies. Explicitly enumerated
// (not derived) so a new builder must declare its spawn count.
var frozenZeroSpawnBuilders = []string{
	"BuildRepoBundle",
	"BuildSearchBundle",
	"BuildDriveBundle",
	"BuildProcessBundle",
	"BuildAIBundle",
	"BuildDomainBundle",
	"BuildJobsBundle",
	// BuildOutboxBundle moved OUT of the zero-spawn list (MEDIA-CUTOVER,
	// September 2026): it spawns exactly 1 goroutine via SafeGo — the
	// pgvector PostgresIndexWorker drain loop (PG mode only), counted by
	// frozenGoroutineInBuildOutboxBundle above.
	"BuildArtifactFinalizeBundle",
	"BuildStagingBundle",
	"BuildTextTrackBundle",
	"BuildMaintBundle",
	"BuildUtilityBundle",
	"BuildStockBundle",
}

// Regex detectors for enumerating + counting goroutine-spawn sites inside
// Build*Bundle bodies. Source-level (not runtime goroutine count) so the
// test is deterministic across CI runners and refactors.
//
//   - goStmtRegex: matches `go <ident>` + `go func() { ... }()` literals
//     but does NOT match `goto <label>`.
//   - buildFuncRegex: enumerates every `func Build*Bundle(<args>)` entry.
var (
	goStmtRegex    = regexp.MustCompile(`(?m)^\s*go\s+(?:\w|func\()`)
	buildFuncRegex = regexp.MustCompile(`(?m)^\s*func\s+(Build\w+Bundle)\(`)
)

// TestComposition_NoGoroutinesSpawned_FrozenSiteCount asserts that the
// number of goroutine-spawn statements inside each Build*Bundle body
// matches the frozen count above. Source-level regex matches (not
// runtime goroutine count) so deterministic across CI runners.
func TestComposition_NoGoroutinesSpawned_FrozenSiteCount(t *testing.T) {
	chdirToProjectRoot(t)

	files := compositionBundleSourceFiles(t)

	counts := make(map[string]int)
	for _, file := range files {
		src := readSourceSilent(file)
		for _, m := range buildFuncRegex.FindAllStringSubmatch(src, -1) {
			counts[m[1]] += countGoroutineSpawns(m[1], src)
		}
	}

	require.Equal(t, frozenGoroutineInBuildDriveBundle, counts["BuildDriveBundle"],
		"BuildDriveBundle spawn count drifted (expected 0 after Wave A Item 15, June 2026 — the drive-style-folders SafeGo goroutine was REMOVED, not just moved).")
	require.Equal(t, frozenGoroutineInBuildOutboxBundle, counts["BuildOutboxBundle"],
		"BuildOutboxBundle must not spawn goroutines: composition-time spawns belong to the start closure, not to a builder body.")

	// The bundle's only spawn lives in the start closure (BON-03 moved it
	// there so the builder body stayed pure assembly). Pin it where it is.
	closureSpawns := -1
	for _, file := range files {
		if n := countGoroutineSpawns("newOutboxStartClosure", readSourceSilent(file)); n >= 0 {
			closureSpawns = n
		}
	}
	require.Equal(t, frozenGoroutineInNewOutboxStartClosure, closureSpawns,
		"newOutboxStartClosure must own exactly one spawn: the pgvector PostgresIndexWorker drain loop.")

	for _, name := range frozenZeroSpawnBuilders {
		require.Equal(t, 0, counts[name],
			"%s must have zero goroutine spawns in its body; recorded spawn count = %d",
			name, counts[name])
	}

	// Every builder discovered in source must appear in the freeze table.
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
// non-test .go source file in internal/app/ that may host a Build*Bundle.
// Test files excluded; list sorted for deterministic iteration.
func compositionBundleSourceFiles(t *testing.T) []string {
	t.Helper()
	pattern := "internal/app/wiring/*.go"
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

// ── 5. PR 4 / PR 6 / PR 7 / Task 3 source-level freeze invariants ────────

// PR 4 (June 2026, refactor/single-pgvector-runtime): the VectorRuntime
// facade is the single construction site for pgvector.NewClient / Schema
// / NewRuntime in production composition paths. Update constants in
// lockstep with the table below if a future PR adds new pgvector ops
// that legitimately cannot share the canonical runtime.
func TestRegisterYouTubeClip_EnabledButMediaServiceUnavailable_SkipsRoute(t *testing.T) {
	cases := map[string]*ComposeRoot{
		"nil root":                   nil,
		"nil domains":                {},
		"domains without yt service": {Domains: &DomainBundle{}},
	}
	for name, root := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := minimalConfig(t.TempDir())
			cfg.Features.YouTubeEnabled = true
			regWiring := &RegistryWiring{}
			err := registerYouTubeClip(module.NewRegistry(), zap.NewNop(), cfg, root, regWiring, nil, nil, nil)
			require.NoError(t, err,
				"an enabled YouTube flag with an unavailable media service must degrade gracefully, not fail the boot")
			require.Nil(t, regWiring.YouTubeClip,
				"the YouTubeClip wiring slot must stay empty when the media service is unavailable")
		})
	}
}

// ── Brace-counting helpers (test-private) ────────────────────────────────

// countGoroutineSpawns returns the number of goroutine-spawn statements
// inside `func <name>(...) {…}`. Returns -1 if the function entry is
// not found (caller treats as test drift).
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

// readSourceSilent reads a file relative to the project root and silently
// swallows errors — used in source-level counting tests that surface drift
// via the count itself rather than a panic.
func readSourceSilent(relPath string) string {
	b, _ := os.ReadFile(relPath)
	return string(b)
}

// findNextTopLevelFuncEnd returns the byte index just after the closing
// brace terminating `func <name>(` at offset `start`. Brace-counting is
// naive (does not skip strings/comments) but composition.go has no
// unbalanced braces inside string literals, so safe for this test file.
// Package-shared (also used by composition_ordering_test.go).
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
