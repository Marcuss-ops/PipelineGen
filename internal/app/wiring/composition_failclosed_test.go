// Package app — composition test dummies (PR-3 / VECTOR-CHAIN-VERIFY).
//
// Push 3.1g (July 2026): split from composition_test.go.
//
// The pgvector fail-closed test that originally owned these dummies was
// retired with the legacy media projection: the SQLite outbox no longer
// registers pgvector media handlers in any mode (the media index plane is
// pgvector.PostgresIndexWorker). The three interface-embedded dummies remain
// because BuildOutboxBundle tests still need non-nil stubs for the Push 3.1c
// and 3.1e typed ports without exercising the handlers.
package wiring

import (
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
// These stubs are reusable for any test that needs to clear the
// BuildOutboxBundle fail-closed gates without exercising the handlers.
//
// Push 3.1g (July 2026): moved here from composition_test.go. The remaining
// caller is TestBuildOutboxBundle_WiresDriveDeleteHandler.
type (
	dummyStagingStore struct{ staging.Store }
	dummyArtifactRepo struct {
		detail.ArtifactStageRepository
	}
	dummyPublisher struct{ delivery.Publisher }
)
