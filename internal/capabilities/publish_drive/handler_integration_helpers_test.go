// Package publish_drive_test — handler_integration_helpers_test.go (this file) (FASE 3 / Push 3.1h, July 2026).
//
// SQLite-anchored integration tests for the publish_drive.Handler drain path. Three test files (Push E.1, July 2026):
//
//   - handler_integration_helpers_test.go (this file) — pkg doc + DDL bridge + repo/envelope builders + stub Publisher.
//   - handler_integration_drain_test.go — drain-completion / no-op tests (Tests 1, 2, 5).
//   - handler_integration_failure_test.go — drain-failure tests (Tests 3, 4). Mirrors the Push 3.1c pattern in
//
// internal/platform/sqlite/artifact_stages/repository_test.go
// (real in-memory SQLite + verbatim DDL constants + direct DB
// probes for state assertions).
//
// The drain pipeline tested end-to-end here:
//
//	real artifact_stages INSERT (via Repository.Insert)
//	  → construct canonical envelope (mirrors outbox_events.Pool.Start
//	    would feed to handler.Handle after a producer's InsertWithOutbox)
//	    → handler.Handle drains the envelope
//	      → Publisher.Publish (stub returns canned PublishResult)
//	        → Repository.MarkPublished fenced CAS
//	          → terminal-state fence (idempotent re-delivery path)
//
// godlike/06 SSOT: every assertion pins either a typed-error
// sentinel (errors.Is for ErrInvalidPayload / ErrTerminalStateRejection)
// OR a direct SQLite-anchored column value (state, published_location,
// published_at) — no application-layer abstraction is trusted as
// ground truth.
// godlike/07 fail-closed: every failure mode asserts that NO
// partial commit reaches the SQLite state (i.e., a Publisher
// failure leaves state=STAGED; a malformed envelope leaves
// state=STAGED; a successful drain leaves state=PUBLISHED +
// canonical JSON PublishedLocation + non-null published_at).
//
// DDL source-of-truth: production migrations are at
// migrations/sqlite/147_artifact_stages.sql + 092_create_outbox_events.sql.
// This test applies them via the migration bridge package
// (migrations/sqlite/embed_ddl.go) — a //go:embed directive in
// the bridge copies the .sql bytes into the test binary at
// compile time. Drift between test and production DDL becomes
// a stale-string-on-rebuild signal instead of a silent failure
// mode. godlike/07 minimum-blast-radius: only this test reads
// the bridge — other callers (artifact_stages/repository_test.go,
// artifact_finalize/service_test.go) still have their own
// constants and are out of scope for Push 3.1h.
package publish_drive

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	artifactstages "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/artifact_stages"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	artifact "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	migration "github.com/Marcuss-ops/PipelineGen/migrations/sqlite"

	_ "github.com/mattn/go-sqlite3"
)

// ── Test DDL bridge — see migrations/sqlite/embed_ddl.go docstring ──────

// setupTestDB creates an in-memory SQLite :memory: with the
// canonical artifact_stages + outbox_events schemas applied.
// DSN: parseTime=true&loc=UTC — without parseTime=true, the
// canonical TEXT-as-RFC3339Nano columns cannot be Scanned into
// time.Time values via the standard library driver. Production
// code wires the DSN with the same flag at the composition root
// (internal/app/build_bundles_*.go threading).
//
// The DDL strings come from the migration package (//go:embed of
// the canonical migrations at compile time) — drift between test
// and production is mechanically detectable on rebuild.
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:?parseTime=true&loc=UTC")
	if err != nil {
		t.Fatalf("open :memory: sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(migration.ArtifactStagesDDL); err != nil {
		t.Fatalf("apply migration.ArtifactStagesDDL: %v", err)
	}
	if _, err := db.Exec(migration.OutboxEventsDDL); err != nil {
		t.Fatalf("apply migration.OutboxEventsDDL: %v", err)
	}
	return db
}

// nowFixed is a deterministic clock for the integration tests
// (mirror of artifact_stages/repository_test.go nowFixed — kept
// local here to avoid test-cross-file constant sharing).
var nowFixed = time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)

// ── Helpers ──────────────────────────────────────────────────────────────

// newRepoForTest builds a real artifactstages.Repository pinned
// to the deterministic clock so Insert + MarkPublished assertions
// are fully reproducible. Public NewRepository is the same entry
// point repository_test.go uses (the artifact_stages package
// exposes it as a public function).
func newRepoForTest(db *sql.DB) *artifactstages.Repository {
	repo := artifactstages.NewRepository(db)
	// SetNowFn (Push 3.1h): public clock seam added to
	// artifactstages.Repository so external test packages
	// (publish_drive_test) can pin CreatedAt + UpdatedAt +
	// PublishedAt to a deterministic value. The field `nowFn`
	// itself is unexported; SetNowFn is the canonical seam
	// for crossing the package boundary.
	repo.SetNowFn(func() time.Time { return nowFixed })
	return repo
}

// validStageForTest returns a stage row ready to insert before
// the handler's drain. Mirrors validStage() in
// artifact_stages/repository_test.go (kept here to keep the
// integration test self-contained — no test-file cross-import
// coupling).
func validStageForTest(id string) *artifact.ArtifactStage {
	return &artifact.ArtifactStage{
		ID:           id,
		JobID:        "job-integ-1",
		LocalPath:    "/var/lib/pipelinegen/staging/job-integ-1/" + id,
		Hash:         "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Size:         4096,
		Mime:         "audio/mpeg",
		Requirement:  artifact.RequirementRequired,
		Destination:  "drive:voiceover/test",
		State:        artifact.ArtifactStageStateStaged,
		AttemptCount: 0,
	}
}

// validEnvelopeForTest returns the canonical TypedStageEventPayload
// + the matching outboxevents.Event envelope that outbox_events.Pool.Start
// would deliver to handler.Handle after a producer's
// InsertWithOutbox emits. The event_key shape matches the
// producer-side canonical key convention
// `stage:<jobID>:<stageID>` (artifact_stages/repository.go:
// InsertWithOutbox returns that exact key for cross-consumer
// dedupe).
func validEnvelopeForTest(stageID, jobID string) (TypedStageEventPayload, outboxevents.Event) {
	p := TypedStageEventPayload{
		StageID:     stageID,
		JobID:       jobID,
		LocalPath:   "/var/lib/pipelinegen/staging/" + jobID + "/" + stageID,
		Hash:        "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Size:        4096,
		Mime:        "audio/mpeg",
		Requirement: "required",
		Destination: "drive:voiceover/test",
		EmittedAt:   nowFixed.Format(time.RFC3339Nano),
	}
	body, _ := json.Marshal(p)
	return p, outboxevents.Event{
		EventType:   EventTypeArtifactStaged,
		PayloadJSON: string(body),
		EventKey:    fmt.Sprintf("stage:%s:%s", jobID, stageID),
	}
}

// ── integrationStubPublisher (test-private, race-safe) ──────────────────
//
// Minimal delivery.Publisher stub used ONLY by the integration
// tests. Kept orthogonal to handler_test.go's stubPublisher
// (different field shape, different name) so test-file
// cross-coupling is avoided. mu/calls are present to keep
// go test -race clean — the handler is invoked from a single
// test goroutine, but the conventional lock-on-mutation pattern
// is cheap insurance.
type integrationStubPublisher struct {
	mu     sync.Mutex
	result *delivery.PublishResult
	pubErr error
	calls  []delivery.PublishRequest
}

func (s *integrationStubPublisher) Publish(_ context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, req)
	return s.result, s.pubErr
}

func (s *integrationStubPublisher) ResolveFolder(_ context.Context, _ delivery.PublishRequest) (string, error) {
	return "", nil
}

// Compile-time conformance anchor (godlike/06 Pattern 0).
var _ delivery.Publisher = (*integrationStubPublisher)(nil)
