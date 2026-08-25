// Package app — rendering_runtime_test.go (RenderingGen overlay contract).
//
// RenderingGen is the GPU-only overlay worker profile. BuildRenderingRuntime
// owns a SEPARATE worker registry (no script generation, TTS, NLP, or
// Qdrant) and registers exactly two job types using chronon-render as the
// pixel renderer:
//
//	overlay.prepare
//	overlay.render
//
// This test file locks that contract in three layers, mirroring the
// creator_runtime_test.go precedent:
//
//  1. TestRenderingRuntime_FrozenImportAllowlist — AST scan of every
//     rendering_*.go file under internal/app/ for forbidden imports
//     (script generation, TTS/voiceover, NLP, Qdrant, SQLite, Drive,
//     scheduler). RenderingGen is push-only: it renders pixels and emits
//     an ArtifactManifest; it never owns script/voiceover/NLP/Qdrant reach.
//
//  2. TestBuildRenderingRuntime_RegistersOnlyOverlayHandlers — the registry
//     holds EXACTLY overlay.prepare + overlay.render, marks overlay.render
//     as artifact-producing, and rejects creator/TTS/NLP/Qdrant job types.
//
//  3. Fail-closed constructor smoke tests (nil config / nil logger).
package wiring

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"

	capoverlays "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// ── 1. import-allowlist freeze test ────────────────────────────

// frozenRenderingForbiddenImportSubstringsLen pins the EXACT slice length so
// a drift that guts the list is a test failure rather than a silent
// regression. Future commits that ADD or REMOVE a forbidden reach MUST
// update BOTH the slice declaration AND this constant in lockstep.
const frozenRenderingForbiddenImportSubstringsLen = 7

// frozenRenderingForbiddenImportSubstrings pins the RenderingGen overlay
// contract: NO rendering_*.go file under internal/app/ may import any of
// the following substrings. The RenderingGen worker owns ONLY overlay
// rendering — PipelineGen owns the OverlayPlan, Chronon3d owns the pixels.
// Script generation, TTS, NLP and Qdrant belong to other worker profiles
// (creator / sender), never to the renderer profile.
//
// Adding a new forbidden reach MUST update this slice AND the
// rendering_runtime.go package doc contract in lockstep.
var frozenRenderingForbiddenImportSubstrings = []string{
	// Script generation — Creator-side capability, not RenderingGen.
	"internal/capabilities/scripts",

	// Script domain model — same rationale.
	"internal/kernel/script",

	// TTS / voiceover — Creator-side capability.
	"voiceover",

	// NLP entity extraction — Creator-side capability.
	"internal/infrastructure/nlp",

	// Qdrant — Sender-side derived projection, not RenderingGen.
	"internal/platform/qdrant",

	// SQLite — RenderingGen has no DB; the Sender owns persistence.
	"internal/platform/sqlite",

	// Drive / scheduler — Sender-side concerns.
	"internal/platform/drive",
}

// TestRenderingRuntime_FrozenImportAllowlist verifies that every
// internal/app/rendering_*.go file (excluding _test.go) imports ONLY the
// overlay-rendering surface and NEVER any of the forbidden substrings.
// Source-level AST parse via go/parser so the test is robust to multi-line
// imports, alias renames, and varied formatting.
func TestRenderingRuntime_FrozenImportAllowlist(t *testing.T) {
	chdirToProjectRoot(t)
	pattern := filepath.Join("internal", "app", "wiring", "rendering_*.go")
	files, err := filepath.Glob(pattern)
	require.NoError(t, err, "glob rendering_*.go")
	require.NotEmpty(t, files, "no rendering_*.go matched — chdir to project root failed or no rendering files exist")

	require.Len(t, frozenRenderingForbiddenImportSubstrings, frozenRenderingForbiddenImportSubstringsLen,
		"RenderingGen: forbidden-reach list drifted without lockstep update to rendering_runtime.go package doc. Both MUST move together.")

	sort.Strings(files)
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		imports := parseGoFileImports(t, file)
		for _, imp := range imports {
			for _, forbid := range frozenRenderingForbiddenImportSubstrings {
				if strings.Contains(imp, forbid) {
					t.Errorf("RenderingGen: forbidden import %q in %s (matched substring %q). The renderer profile is overlay-only — route script/TTS/NLP/Qdrant/DB/Drive/scheduler reach through other worker profiles (see rendering_runtime.go package doc).",
						imp, file, forbid)
				}
			}
		}
	}
}

// ── 2. registry composition contract ──────────────────────────

// renderingForbiddenJobTypes enumerates job types that MUST NOT appear in
// the RenderingGen registry. These belong to other profiles (creator /
// sender); a regression that registers any of them breaks the renderer
// profile's overlay-only contract.
var renderingForbiddenJobTypes = []string{
	"script.generate",
	"voiceover.generate_item",
	"image.generate.google",
	"media.stock",
}

// TestBuildRenderingRuntime_RegistersOnlyOverlayHandlers is the canonical
// registry contract test. BuildRenderingRuntime must build a frozen registry
// containing EXACTLY overlay.prepare + overlay.render, mark overlay.render
// as artifact-producing, and never register creator/TTS/NLP/Qdrant job types.
func TestBuildRenderingRuntime_RegistersOnlyOverlayHandlers(t *testing.T) {
	cfg := &config.Config{}
	log := zaptest.NewLogger(t)

	rt, cleanup, err := BuildRenderingRuntime(cfg, log)
	require.NoError(t, err, "BuildRenderingRuntime")
	require.NotNil(t, rt, "*RenderingRuntime must be non-nil")
	require.NotNil(t, cleanup, "cleanup func must be non-nil")
	defer cleanup()

	require.NotNil(t, rt.Registry, "rt.Registry must be non-nil")
	require.NotNil(t, rt.Workspace, "rt.Workspace must be non-nil")
	require.NotNil(t, rt.Cache, "rt.Cache must be non-nil")

	// Exactly the two overlay job types — no more, no fewer.
	jobs := rt.Registry.JobTypes()
	require.ElementsMatch(t, []string{capoverlays.JobTypePrepare, capoverlays.JobTypeRender}, jobs,
		"RenderingGen registry must register exactly overlay.prepare + overlay.render")

	// overlay.render produces artifacts (drives CompleteWithArtifacts).
	require.True(t, rt.Registry.ProducesArtifacts(capoverlays.JobTypeRender),
		"overlay.render must be marked as artifact-producing")
	require.False(t, rt.Registry.ProducesArtifacts(capoverlays.JobTypePrepare),
		"overlay.prepare must NOT be marked as artifact-producing")

	// Negative contract: no creator/TTS/NLP/Qdrant job types.
	for _, jt := range renderingForbiddenJobTypes {
		require.False(t, rt.Registry.Has(jt), "RenderingGen registry must NOT register %q", jt)
	}

	// Caps derive from the registry (single source of truth).
	require.ElementsMatch(t, []string{capoverlays.JobTypePrepare, capoverlays.JobTypeRender}, rt.Caps.JobTypes,
		"Caps.JobTypes must derive from the registry")
	require.True(t, rt.Caps.GPU, "renderer profile requires GPU")
	require.True(t, rt.Caps.FFmpeg, "renderer profile requires FFmpeg")
}

// ── 3. fail-closed constructor smoke tests ────────────────────

func TestBuildRenderingRuntime_NilConfig_FailsClosed(t *testing.T) {
	log := zaptest.NewLogger(t)
	rt, cleanup, err := BuildRenderingRuntime(nil, log)
	require.Error(t, err, "nil config must fail closed")
	require.Nil(t, rt, "*RenderingRuntime must be nil on nil config")
	require.Nil(t, cleanup, "cleanup must be nil on nil config")
}

func TestBuildRenderingRuntime_NilLogger_FailsClosed(t *testing.T) {
	rt, cleanup, err := BuildRenderingRuntime(&config.Config{}, nil)
	require.Error(t, err, "nil logger must fail closed")
	require.Nil(t, rt, "*RenderingRuntime must be nil on nil logger")
	require.Nil(t, cleanup, "cleanup must be nil on nil logger")
}
