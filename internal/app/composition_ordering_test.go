// Package app — composition Ordering test bundle (PR4.C, June 2026 +
// FASE 3 Spina Dorsale, July 2026).
//
// Push 3.1g (July 2026): split from composition_test.go.
//
// This file holds the Build*Bundle ORDERING invariants + the
// nil-obligatory structural canary tests:
//
//   - TestComposition_NilObligatory_NewComposition — every bundle in
//     the assembled ComposeRoot is non-nil; canary fields in each
//     bundle are populated under a minimal config.
//   - TestComposition_NilObligatory_BuildRepoBundle, _BuildSearchBundle,
//     _BuildJobsBundle — per-builder nil-field invariants for the
//     leaf builders that can be exercised without external services.
//   - TestComposition_FreezeOrdering_BuildSequence — source-level
//     assertion that NewComposition's Build*Bundle call order matches
//     the 12-element frozen sequence.
//   - TestComposition_FreezeOrdering_FASE3_SpinaDorsaleSequence —
//     narrower 3-element ordering invariant for the FASE 3 Spina
//     Dorsale publication saga (Push 3.1c/3.1d/3.1e/3.1f).
//
// The nil-obligatory tests live here (not in
// composition_freeze_test.go) because they EXERCISE Build*Bundle in
// dependency-order — the test ordering mirrors the orchestrator
// graph. TestComposition_NilObligatory_BuildRepoBundle runs first
// (canonical leaf), BuildSearchBundle runs second (reads RepoBundle),
// BuildJobsBundle runs third (cross-validated by
// TestBuildJobsBundle_FieldsAreNonNil). The structural sequence is
// adjacent to the ordering-invariant pin, which keeps cohesion among
// "tests about Build*Bundle call-shape" inside this file.
//
// This file is a sister to:
//   - composition_failclosed_test.go (BuildOutboxBundle + buildQdrantDeps
//     fail-closed gates; dummies live there).
//   - composition_freeze_test.go (source-level freeze tests; package
//     doc + shared helpers chdirToProjectRoot / minimalConfig live there).
//
// Push 3.1g note: the helpers this file calls — chdirToProjectRoot,
// minimalConfig, findNextTopLevelFuncEnd, compositionSourcePath — are
// defined in composition_freeze_test.go (the primary file hosting
// package doc). Same-package Go test files share funcs implicitly, so
// callers do NOT need any cross-file import ceremony.
package app

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

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

	dbs, err := wiring.InitDatabases(context.Background(), cfg, log)
	require.NoError(t, err, "initDatabases")
	t.Cleanup(func() {
		if dbs != nil && dbs.Main != nil {
			_ = dbs.Main.Close()
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

	dbs, err := wiring.InitDatabases(context.Background(), cfg, log)
	require.NoError(t, err)
	t.Cleanup(func() {
		if dbs != nil && dbs.Main != nil {
			_ = dbs.Main.Close()
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

	dbs, err := wiring.InitDatabases(context.Background(), cfg, log)
	require.NoError(t, err)
	t.Cleanup(func() {
		if dbs != nil && dbs.Main != nil {
			_ = dbs.Main.Close()
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
	bundle, err := wiring.BuildJobsBundle(sqliteDB, zaptest.NewLogger(t), nil, nil, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, bundle)
	require.NotNil(t, bundle.Repo)
	require.NotNil(t, bundle.Dispatcher)
	require.NotNil(t, bundle.Service)
	require.NotNil(t, bundle.Facade)
}

// ── 3. freeze-ordering ───────────────────────────────────────────────────

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
//
// Push 3.1g (July 2026): MOVED from composition_test.go to
// composition_ordering_test.go (this file) alongside the BuildSequence
// test that consumes it.
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

// ── 3a. FASE 3 Spina Dorsale ordering invariant (Push 3.1c/3.1d/3.1e) ──

// frozenStagingOutboxFinalizeSequence pins the FASE 3 Spina Dorsale
// publication saga's canonical NewComposition ordering:
//
//	BuildStagingBundle  →  BuildOutboxBundle  →  BuildArtifactFinalizeBundle
//
// This 3-element sequence is the ONLY sequencing guarantee the saga's
// FASE 3 step cares about. The broader 12-element
// frozenCompositionSequence above pins the full orchestrator order;
// this slice isolates the FASE 3 ordering so a future push touching
// only the FASE 3 step (e.g. adding a new worker registration to
// BuildOutboxBundle, or a new typed port to StagingBundle) does NOT
// require touching frozenCompositionSequence — only this focused
// sequence.
//
// godlike/07 ordering history (the reason this test exists):
//   - Push 3.1b (July 2026) placed BuildStagingBundle LAST in
//     NewComposition because the staging bundle had "minimal deps
//     (only dbs.Main.DB + cfg + log), so reordering [earlier] is
//     fail-safe — no risk of breaking the existing 12-bundle
//     aggregation." That comment is the forward-pointer footgun
//     this test exists to disarm.
//   - Push 3.1c (July 2026) MOVE'd BuildStagingBundle from LAST to
//     BEFORE BuildOutboxBundle because BuildOutboxBundle now
//     consumes staging.Store (the publish_outbox worker pool target)
//     and the Publisher worker MUST be wired at composition time.
//   - Push 3.1e (July 2026) added staging.Repository to the
//     BuildOutboxBundle arg list (for the publish_drive worker's
//     MarkPublished fenced CAS) — also a hard requirement that
//     BuildStagingBundle runs first.
//   - Push 3.1d (July 2026) added BuildArtifactFinalizeBundle AFTER
//     BuildOutboxBundle because the Finalizer consumes the SAME
//     artifact.Repository port that Staging exposes.
//
// godlike/07 fail-closed consequence of a regression: re-introducing
// the old "BuildStagingBundle LAST" order makes BuildOutboxBundle
// fail at runtime with:
//
//	"BuildOutboxBundle: stagingSvc is required (FASE 3 Push 3.1c;
//	 composition must call BuildStagingBundle before BuildOutboxBundle)"
//
// …serving a fail-closed terminal error at boot. The regression is
// dramatic but late — the test below catches it at the unit-test
// step in CI, NOT in a production boot loop.
//
// Push 3.1g (July 2026): MOVED from composition_test.go to
// composition_ordering_test.go (this file) alongside the test that
// consumes it.
var frozenStagingOutboxFinalizeSequence = []string{
	"BuildStagingBundle(",
	"BuildOutboxBundle(",
	"BuildArtifactFinalizeBundle(",
}

// TestComposition_FreezeOrdering_FASE3_SpinaDorsaleSequence pins the
// 3-element FASE 3 publication-saga ordering inside NewComposition.
// A regression that re-introduces "BuildStagingBundle LAST" (the
// Push 3.1b placement that was a forward-pointer footgun and was
// corrected in Push 3.1c) would surface here BEFORE the runtime
// fail-closed "stagingSvc is required" error fires at boot.
//
// The test is intentionally narrower than
// TestComposition_FreezeOrdering_BuildSequence above: only the FASE 3
// step is pinned (the broader 12-element orchestrator sequence is
// already covered). Future FASE 3 pushes that touch only the staging
// step (e.g. adding a new typed port to StagingBundle) update THIS
// sequence in lockstep, not frozenCompositionSequence.
//
// godlike/06 SSOT: the canonical ordering is documented inline at
// the `frozenStagingOutboxFinalizeSequence` var above. If a future
// author moves BuildOutboxBundle ahead of BuildStagingBundle, the
// failure message cites both the new ordering's provenance (FASE 3
// Push 3.1c reordering) AND the runtime error the regression would
// create — so the cure is unambiguous.
func TestComposition_FreezeOrdering_FASE3_SpinaDorsaleSequence(t *testing.T) {
	chdirToProjectRoot(t)

	source, err := os.ReadFile(compositionSourcePath)
	require.NoError(t, err, "read composition.go")
	text := string(source)

	// Restrict the search to the NewComposition body — package-level
	// doc comments + helper functions may mention these names
	// (e.g. the "Future push should reverse this…" forward-pointer
	// in the BuildStagingBundle docstring). Including those references
	// would pollute the offset ordering.
	start := strings.Index(text, "func NewComposition(")
	require.GreaterOrEqual(t, start, 0, "NewComposition entry not found in composition.go")
	end := findNextTopLevelFuncEnd(text, start)
	body := text[start:end]

	prev := -1
	for _, name := range frozenStagingOutboxFinalizeSequence {
		idx := strings.Index(body, name)
		require.Greaterf(t, idx, prev,
			"%s appears out-of-order or absent in NewComposition body (last index=%d, this index=%d). The FASE 3 Spina Dorsale canonical ordering is BuildStagingBundle → BuildOutboxBundle → BuildArtifactFinalizeBundle. The Push 3.1b forward-pointer comment incorrectly placed BuildStagingBundle LAST; Push 3.1c MOVE it ahead of BuildOutboxBundle so the Publisher worker pool (registered inside BuildOutboxBundle) can consume staging.Store. A regression to the old order would crash compositing at runtime with: BuildOutboxBundle: stagingSvc is required (FASE 3 Push 3.1c; composition must call BuildStagingBundle before BuildOutboxBundle). Update frozenStagingOutboxFinalizeSequence in lockstep if you intentionally rewire the order.",
			name, prev, idx)
		prev = idx
	}
}
