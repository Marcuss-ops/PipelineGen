// Package app — composition Freeze test bundle.
//
// Push 3.1g (July 2026): split from composition_test.go. The composition
// test family is now 3 files:
//
//   - composition_ordering_test.go: nil-obligatory + BuildSequence +
//     FASE 3 Spina Dorsale ordering + no-goroutines-spawned.
//   - composition_failclosed_test.go: PR-3 + QDRANT-CHAIN-VERIFY
//     fail-closed gates + the 3 dummy types.
//   - composition_freeze_test.go (this file): source-level freeze tests
//     (PR 4 single-Qdrant-runtime, PR 6/7 + Task 3 single-declaration
//     gates) + the package-level doc + shared helpers chdirToProjectRoot
//     and minimalConfig.
//
// Files share funcs implicitly (same Go package). The freeze file is
// the "primary" file (hosts pkg doc + helpers) because it has the
// broadest helper-reuse surface.
package app

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/stretchr/testify/require"
)

// ── Test helpers (package-shared across composition_*_test.go) ─────────────

// compositionSourcePath is the canonical relative path to composition.go
// from the project root. chdirToProjectRoot (below) ensures the cwd is the
// project root so the path resolves consistently regardless of where
// `go test ./internal/app/` is invoked from.
const compositionSourcePath = "internal/app/composition.go"

// chdirToProjectRoot mirrors the pattern in wire_test.go so nested
// migrations + relative-path resolution work the same way. Package-shared
// across all 3 composition_*_test.go files (same-package Go test scope).
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
// Package-shared across all 3 composition_*_test.go files.
func minimalConfig(dataDir string) *config.Config {
	return &config.Config{
		Security: config.SecurityConfig{EnableAuth: false},
		Server:   config.ServerConfig{Port: 8080, ReadTimeout: 600, WriteTimeout: 600},
		External: config.ExternalConfig{
			OllamaURL:            "http://localhost:11434",
			OllamaModel:          "llama3.2",
			OllamaTimeoutSeconds: 30,
			// Qdrant contract gate (PR-QDRANT-CONFIG-MISMATCH-GATE)
			// demands runtime_model == schema dense model when Qdrant is
			// enabled; keep the fixture in the canonical default so
			// Qdrant-enabled composition tests are not pre-broken.
			OllamaEmbedModel: "intfloat/multilingual-e5-base",
		},
		Paths:   config.PathsConfig{},
		Storage: config.StorageConfig{DataDir: dataDir},
		Media: config.MediaConfig{
			Multilingual: config.MultilingualConfig{SourceLanguage: "en"},
		},
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

// ── 2. no-goroutine-spawned ───────────────────────────────────────────────

// Frozen counts (June 2026, PR4.C). A growing count means someone added
// a new spawn to a Build*Bundle body — that is the bug this test catches.
// When a future PR4.E+ wave moves inline goroutine spawns out of
// Build*Bundle into lifecycle.go, decrement the constants in lockstep.
const (
	// Wave A Item 15 (June 2026): the legacy drive-style-folders SafeGo
	// goroutine was REMOVED (not moved) — BuildDriveBundle body has zero
	// spawns. Retained only as a structural marker.
	frozenGoroutineInBuildDriveBundle = 0
	// PR9-B (June 2026): concurrent.SafeGo x2 (pool start + shutdown)
	// moved from BuildOutboxBundle body into startOutboxEventsPool.
	frozenGoroutineInBuildOutboxBundle = 0
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
	"BuildOutboxBundle",
	"BuildArtifactFinalizeBundle",
	"BuildStagingBundle",
	"BuildTextTrackBundle",
	"BuildSyncBundle",
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
		"BuildOutboxBundle spawn count drifted. Migrating outbox-pool SafeGo x2 to lifecycle.go is documented in lifecycle.go comment line ~294; update `frozenGoroutineInBuildOutboxBundle` in lockstep.")

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

// ── 5. PR 4 / PR 6 / PR 7 / Task 3 source-level freeze invariants ────────

// PR 4 (June 2026, refactor/single-qdrant-runtime): the QdrantRuntime
// facade is the single construction site for qdrant.NewClient / Schema
// / NewRuntime in production composition paths. Update constants in
// lockstep with the table below if a future PR adds new Qdrant ops
// that legitimately cannot share the canonical runtime.
const (
	frozenQdrantClientSites          = 0
	frozenQdrantDefaultV3SchemaSites = 1
	frozenQdrantNewRuntimeSites      = 1
)

// TestComposition_FrozenClientConstructionSites: no qdrant.NewClient(...)
// call in internal/app/*.go (test files excluded). The transport-layer
// client now lives outside the composition package, so this guard keeps
// the app layer from reintroducing its own construction site.
func TestComposition_FrozenClientConstructionSites(t *testing.T) {
	chdirToProjectRoot(t)
	files := compositionBundleSourceFiles(t)
	matches := 0
	for _, f := range files {
		src, _ := os.ReadFile(f)
		matches += strings.Count(string(src), "qdrant.NewClient(")
	}
	require.Equalf(t, frozenQdrantClientSites, matches,
		"PR 4: qdrant.NewClient(%d expected call sites in internal/app/*.go; found %d. QdrantRuntime.NewRuntime is the single construction site. Add the subsystem to QdrantRuntime rather than constructing a second Client — see composition_freeze_test.go::TestComposition_FrozenClientConstructionSites for the acceptance criterion.",
		frozenQdrantClientSites, matches)
}

// TestComposition_FrozenIndexSchemaSites: exactly ONE qdrant.DefaultV3Schema()
// call site. Pre-PR4 state had TWO sites — schema version could drift
// between them. After PR 4 the runtime holds a single Schema instance.
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

// TestComposition_FrozenNewRuntimeSites: exactly ONE qdrant.NewRuntime
// construction site. composition.go::buildQdrantDeps is the canonical
// constructor; BuildProcessBundle reads via qd.Runtime, never re-builds.
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

// TestComposition_FrozenQdrantHealthProbeAny: PR 4 section #6 — the loose
// type-assertion escape hatch `QdrantHealthProbe any` is gone; after PR 4
// ProcessBundle.QdrantHealthProbe is concretely typed *qdrant.HealthProbe.
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

// TestComposition_FrozenVectorPointDeleterPort: PR 4 acceptance #1 —
// exactly ONE VectorPointDeleter port in internal/, declared in
// internal/application/jobs/outbox/ports.go per AGENTS.md Pattern 0.
// Pre-PR4 had two duplicate QdrantDeleter interfaces (both deleted).
func TestComposition_FrozenVectorPointDeleterPort(t *testing.T) {
	chdirToProjectRoot(t)
	src, err := os.ReadFile("internal/application/jobs/outbox/ports.go")
	require.NoError(t, err, "read canonical VectorPointDeleter port file")
	matches := strings.Count(string(src), "type VectorPointDeleter interface")
	require.Equalf(t, 1, matches,
		"PR 4: exactly 1 `type VectorPointDeleter interface` declaration in internal/application/jobs/outbox/ports.go; found %d. The canonical port lives in the application layer per AGENTS.md Pattern 0.",
		matches)
}

// PR 7 #7.1 (chore/remove-qdrant-legacy): the legacy
// internal/infrastructure/qdrant/reconciler.go (and absent test) were
// deleted. This test pins both the file-existence AND zero-stale-caller
// invariant. The canonical reconciler lives at
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

// PR 6 #1 (refactor/qdrant-index-document): canonical Qdrant wire-shape
// freeze. The IndexDocument / IndexedMetadata / EmbeddingArtifact /
// VectorChannel types (and the ForbiddenIndexDocumentFields SSOT slice)
// declared in internal/infrastructure/qdrant/indexing/index_document.go
// are the airlock vocabulary. If any of these MOVE or get RENAMED,
// update this test AND the file's doctrine comment together.
func TestComposition_FrozenQdrantIndexDocumentCanonicalTypes(t *testing.T) {
	chdirToProjectRoot(t)

	const file = "internal/infrastructure/qdrant/indexing/index_document.go"
	src, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("PR 6 #1: canonical wire-shape file %s must exist (created in refactor/qdrant-index-document): %v", file, err)
	}
	body := string(src)

	// Exactly-one declaration of each canonical type.
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

	// The canonical VectorChannel constants must each be declared once.
	for _, ch := range []string{"ChannelText", "ChannelTranscript", "ChannelVisual", "ChannelAudio", "ChannelBM25Text"} {
		pattern := "\t" + ch + " VectorChannel"
		n := strings.Count(body, pattern)
		require.Equalf(t, 1, n,
			"PR 6 #1: canonical channel constant %q expected exactly once in %s; found %d. The channel vocabulary is the writer↔search-adapter contract.",
			ch, file, n)
	}

	// BuildPayloadFromDocument (canonical writer-side payload emitter)
	// must exist exactly once in payload_builder.go.
	b, err := os.ReadFile("internal/infrastructure/qdrant/indexing/payload_builder.go")
	require.NoError(t, err, "read canonical qdrant payload mapper")
	emitterCount := strings.Count(string(b), "func BuildPayloadFromDocument")
	require.Equalf(t, 1, emitterCount,
		"PR 6 #1: exactly 1 `func BuildPayloadFromDocument` declaration expected in internal/infrastructure/qdrant/payload_mapper.go; found %d. The canonical writer-side payload emitter lives in payload_mapper.go.",
		emitterCount)

	// The forbidden-fields SSOT slice must contain EXACTLY the SSOT
	// markers (2 entries today: Status, LocalPath).
	for _, forbidden := range []string{"\"Status\"", "\"LocalPath\""} {
		n := strings.Count(body, forbidden)
		require.Equalf(t, 1, n,
			"PR 6 #1: forbidden field %q expected exactly once in ForbiddenIndexDocumentFields SSOT; found %d", forbidden, n)
	}
}

// Task 3 (July 2026): exactly ONE production declaration of
// AssetIDToQdrantPointID. Canonical implementation is UUID v5 SHA-1 in
// internal/infrastructure/qdrant/schema/pointid.go. All callers MUST
// route through this single function; ad-hoc point ID generation is
// forbidden. The aliases.go redirect `var ... = schema.AssetIDToQdrantPointID`
// is NOT a declaration — the check only matches `func AssetIDToQdrantPointID`.
const frozenAssetIDToQdrantPointIDSites = 1

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
