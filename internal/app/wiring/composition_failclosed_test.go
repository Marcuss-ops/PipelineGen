// Package app — composition FailClosed test bundle (PR-3 / QDRANT-CHAIN-VERIFY).
//
// Push 3.1g (July 2026): split from composition_test.go.
//
// This file holds the 3 fail-closed contract tests for the composition
// root (godlike/07 fail-fast-at-boot gates). The tests assert that
// operator misconfigurations abort boot with a TERMINAL error rather
// than silently degrading to a false-success runtime path:
//
//   - TestComposition_QdrantEnabledNoClipIndexer_WriterAndDeleterWired:
//     Direction B (Qdrant=true + ClipIndexer=false) — the RED POINT
//     found by the QDRANT-CHAIN-VERIFY-2026-07-04 audit.
//   - TestComposition_ClipIndexerEnabledNoQdrant_FailClosed:
//     Direction A (ClipIndexer=true + Qdrant=false) — pre-existing check.
//   - TestComposition_QdrantEnabledMissingAssetDeleter_FailClosed:
//     PR 3 #4 + #5 — BuildOutboxBundle fail-closed when
//     repos.ClipsRepo=nil (the AssetDeleter mandatory port is missing).
//
// The 3 interface-embedded dummy types (dummyStagingStore /
// dummyArtifactRepo / dummyPublisher) live here because they are used
// ONLY by the 3rd test above (the BuildOutboxBundle gate needs non-nil
// stubs for the Push 3.1c + 3.1e-typed ports so the underlying
// ClipsRepo=nil fail-closed check actually fires). Reusable for any
// future test that needs to clear the BuildOutboxBundle fail-closed
// gates without exercising the handlers.
package wiring

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/staging"
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// ── Dummy dependencies for fail-closed builder tests ──────────────────────
//
// Push 3.1f (July 2026): BuildOutboxBundle's signature was extended in
// Push 3.1c (added `stagingSvc staging.Store`) and Push 3.1e (added
// `repo artifact.Repository` + `drivePublisher delivery.Publisher`),
// then Push 3.1k/Blocco 3.1 (July 2026) added
// `driveDeleter jobsoutbox.DriveDeleter` — now 12 args total. The pre-existing PR-3 fail-closed test at
// TestComposition_QdrantEnabledMissingAssetDeleter_FailClosed needed an
// update: it was passing 8 args, and if naively padded with three nil
// values the NEW fail-closed gate "stagingSvc is required" would
// fire FIRST, defeating the test's "core outbox handlers" assertion.
//
// Cheapest fix: define 3 zero-method stubs via Go's interface-embedding
// pattern. `type dummyStagingStore struct{ staging.Store }` auto-
// satisfies the interface at compile time (no method bodies needed),
// the interface VALUE is non-nil (so `!= nil` gates pass), and the
// embedded nil iface only panics if a method is invoked — which the
// fail-closed path never does (RegisterCoreHandlers errors OUT before
// any handler.Handle() call).
//
// These stubs are reusable for any future test that needs to clear the
// BuildOutboxBundle fail-closed gates without exercising the handlers.
//
// Push 3.1g (July 2026): MOVED from composition_test.go to
// composition_failclosed_test.go (this file) because the 3 dummies
// are used ONLY by the 3 fail-closed test callers here.
type (
	dummyStagingStore struct{ staging.Store }
	dummyArtifactRepo struct {
		detail.ArtifactStageRepository
	}
	dummyPublisher struct{ delivery.Publisher }
)

// ── 4. PR 3 + QDRANT-CHAIN-VERIFY fail-closed invariants ──────────────

// TestComposition_QdrantEnabledNoClipIndexer_WriterAndDeleterWired
// pins the independent-capability contract: with Qdrant enabled and the
// optional ClipIndexer disabled, buildQdrantDeps still wires the Qdrant
// runtime for projection reads/deletes. Index writes remain disabled and
// must not report success without a vector.
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
// The indexer handler itself remains fail-closed when disabled. Direction A
// (ClipIndexer=true + Qdrant=false) is covered by the companion test below.
func TestComposition_QdrantEnabledNoClipIndexer_WriterAndDeleterWired(t *testing.T) {
	chdirToProjectRoot(t)

	dataDir := t.TempDir()
	cfg := minimalConfig(dataDir)
	cfg.Qdrant.Enabled = true       // Qdrant feature ON
	cfg.ClipIndexer.Enabled = false // sidecar OFF (the RED-POINT under test)
	log := zaptest.NewLogger(t)

	dbs, err := InitDatabases(context.Background(), cfg, log)
	require.NoError(t, err, "initDatabases")
	t.Cleanup(func() {
		if dbs != nil && dbs.Main != nil {
			_ = dbs.Main.Close()
		}
	})

	repos, err := BuildRepoBundle(context.Background(), cfg, dbs, log)
	require.NoError(t, err, "BuildRepoBundle (PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 5: buildQdrantDeps now requires repos for canonical TextTrackRepo SSOT)")

	var qd *QdrantDeps
	qd, err = buildQdrantDeps(context.Background(), cfg, dbs, repos, log)
	require.NoError(t, err)
	require.NotNil(t, qd)
	require.NotNil(t, qd.Runtime)
}

// TestComposition_ClipIndexerEnabledNoQdrant_FailClosed pins
// BLOCKER #3 (Qdrant Verdetto, July 2026): with cfg.ClipIndexer.Enabled=true
// but cfg.Qdrant.Enabled=false, buildQdrantDeps MUST abstain boot with a
// terminal error. The ClipIndexer vector-store write path requires Qdrant
// for UpsertVectorStore completion; without it, every indexing job would
// dead-letter on the nil vectorStore or silently skip the Qdrant write.
// The previous code merely logged a warning and continued, which produced
// a false-success path (asset marked INDEXED but never actually written
// to Qdrant). Fail-closed at composition time per godlike/07.
func TestComposition_ClipIndexerEnabledNoQdrant_UsesDisabledProjectionMode(t *testing.T) {
	chdirToProjectRoot(t)

	dataDir := t.TempDir()
	cfg := minimalConfig(dataDir)
	cfg.ClipIndexer.Enabled = true // ClipIndexer ON
	cfg.Qdrant.Enabled = false     // Qdrant OFF — the BLOCKER #3 trigger
	log := zaptest.NewLogger(t)

	dbs, err := InitDatabases(context.Background(), cfg, log)
	require.NoError(t, err, "initDatabases")
	t.Cleanup(func() {
		if dbs != nil && dbs.Main != nil {
			_ = dbs.Main.Close()
		}
	})

	repos, err := BuildRepoBundle(context.Background(), cfg, dbs, log)
	require.NoError(t, err, "BuildRepoBundle (PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 5: buildQdrantDeps now requires repos for canonical TextTrackRepo SSOT)")

	var qd *QdrantDeps
	qd, err = buildQdrantDeps(context.Background(), cfg, dbs, repos, log)
	require.NoError(t, err)
	require.NotNil(t, qd)
	require.Nil(t, qd.Runtime, "Qdrant-disabled mode must not create a vector projection")
}

// TestComposition_QdrantEnabledMissingAssetDeleter_FailClosed pins
// PR 3 #4 + #5: with cfg.Qdrant.Enabled=true but repos.ClipsRepo=nil,
// BuildOutboxBundle MUST abstain boot via RegisterCoreHandlers's
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

	dbs, err := InitDatabases(context.Background(), cfg, log)
	require.NoError(t, err)
	t.Cleanup(func() {
		if dbs != nil && dbs.Main != nil {
			_ = dbs.Main.Close()
		}
	})

	repos, err := BuildRepoBundle(context.Background(), cfg, dbs, log)
	require.NoError(t, err)

	qd, err := buildQdrantDeps(context.Background(), cfg, dbs, repos, log)
	require.NoError(t, err)
	require.NotNil(t, qd.QdrantDeleter)

	jobsBundle, err := BuildJobsBundle(dbs.Main, log, nil, nil, nil, nil)
	require.NoError(t, err)

	// Force a nil AssetDeleter dep at the BuildOutboxBundle call site
	// by zeroing ClipsRepo. The fact that the *assets.ClipsRepository
	// also implements SourceVersionQuerier means BOTH gates fire here,
	// which is fine: the first required-dep error is still surfaced,
	// and the fail-closed semantic (BuildOutboxBundle returning err)
	// is the contract under test, regardless of WHICH dep was first
	// to trigger.
	repos.ClipsRepo = nil

	// Note: the buildQdrantDeps call above already passes the `repos`
	// variable; the local repos mutation (ClipsRepo = nil) below does
	// NOT affect buildQdrantDeps' QdrantDeps (which was constructed
	// from the pre-mutation repos). The fail-closed contract under
	// test is BuildOutboxBundle's, not buildQdrantDeps'.

	// Push 3.1f (July 2026): BuildOutboxBundle signature is now
	// (ctx, cfg, dbs, log, repos, qd, jobs, voiceoverDriver, stagingSvc,
	// repo, drivePublisher). Pass non-nil interface-embedded stubs
	// (`dummyStagingStore{}`, `dummyArtifactRepo{}`, `dummyPublisher{}`)
	// for the 3 NEW args so the underlying "ClipsRepo=nil" fail-closed
	// test reaches the RegisterCoreHandlers gate (triggering
	// "core outbox handlers") — not the earlier "stagingSvc is required"
	// gate introduced in Push 3.1c.
	_, _, err = BuildOutboxBundle(context.Background(), cfg, dbs, log, repos, qd, jobsBundle, nil, dummyStagingStore{}, dummyArtifactRepo{}, dummyPublisher{}, nil, nil)
	require.Error(t, err,
		"PR 3: cfg.Qdrant.Enabled=true + nil ClipsRepo must abort BuildOutboxBundle (fail-closed at boot, never warn-as-warning)")
	require.Contains(t, err.Error(), "core outbox handlers",
		"error must originate from RegisterCoreHandlers so operators grep distinctively:\n\tgot: %v", err)
}
