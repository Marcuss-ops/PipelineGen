// Package app — composition FailClosed test bundle (PR-3 / VECTOR-CHAIN-VERIFY).
//
// Push 3.1g (July 2026): split from composition_test.go.
//
// This file holds the 3 fail-closed contract tests for the composition
// root (godlike/07 fail-fast-at-boot gates). The tests assert that
// operator misconfigurations abort boot with a TERMINAL error rather
// than silently degrading to a false-success runtime path:
//
//   - TestComposition_pgvectorEnabledNoClipIndexer_WriterAndDeleterWired:
//     Direction B (pgvector=true + ClipIndexer=false) — the RED POINT
//     found by the VECTOR-CHAIN-VERIFY-2026-07-04 audit.
//   - TestComposition_ClipIndexerEnabledNoVector_FailClosed:
//     Direction A (ClipIndexer=true + pgvector=false) — pre-existing check.
//   - TestComposition_pgvectorEnabledMissingAssetDeleter_FailClosed:
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
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/staging"
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
)

// ── Dummy dependencies for fail-closed builder tests ──────────────────────
//
// Push 3.1f (July 2026): BuildOutboxBundle's signature was extended in
// Push 3.1c (added `stagingSvc staging.Store`) and Push 3.1e (added
// `repo artifact.Repository` + `drivePublisher delivery.Publisher`),
// then Push 3.1k/Blocco 3.1 (July 2026) added
// `driveDeleter jobsoutbox.DriveDeleter` — now 12 args total. The pre-existing PR-3 fail-closed test at
// TestComposition_pgvectorEnabledMissingAssetDeleter_FailClosed needed an
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

// ── 4. PR 3 + VECTOR-CHAIN-VERIFY fail-closed invariants ──────────────

// TestComposition_pgvectorEnabledMissingAssetDeleter_FailClosed pins
// PR 3 #4 + #5: with cfg.pgvector.Enabled=true but repos.ClipsRepo=nil,
// BuildOutboxBundle MUST abstain boot via RegisterCoreHandlers's
// fail-closed contract. AssetDeleter=nil is one of the four mandatory
// core deps (alongside indexer / SourceVersionQuerier / VectorPointDeleter);
// the previous log.Warn("failed to register outbox events handlers")
// silently downgraded the wiring bug to a runtime dead-letter on the
// first asset.index.requested event. The composed error message must
// name the missing dep so operators can grep the boot log.
func TestComposition_pgvectorEnabledMissingAssetDeleter_FailClosed(t *testing.T) {
	t.Skip("POSTGRES-MEDIA-CUTOVER demolition: the SQLite outbox no longer registers pgvector media core handlers in ANY mode — the media index plane is the pgvector PostgresIndexWorker, so the 'pgvector enabled + nil ClipsRepo must abort core handler registration' contract is retired with the legacy media projection. The surviving fail-closed contract is the pgvector worker's nil-dep panics (outbox_worker.go).")
}
