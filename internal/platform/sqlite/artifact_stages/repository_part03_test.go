package artifactstages

import (
	"context"
	"errors"
	artifact "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	_ "github.com/mattn/go-sqlite3"
	"testing"
	"time"
)

func TestRepository_IncrementAttemptCount_RejectsOnTerminal(t *testing.T) {
	db := setupTestDB(t)
	repo := NewRepository(db)
	repo.nowFn = func() time.Time { return nowFixed }
	ctx := context.Background()
	s := validStage()
	if err := repo.Insert(ctx, s); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := repo.MarkSucceeded(ctx, s.ID); err != nil {
		t.Fatalf("MarkSucceeded: %v", err)
	}
	if err := repo.IncrementAttemptCount(ctx, s.ID); !errors.Is(err, artifact.ErrTerminalStateRejection) {
		t.Errorf("IncrementAttemptCount on SUCCEEDED: err = %v, want ErrTerminalStateRejection", err)
	}
}

// ── Nano-precision round-trip ───────────────────────────────────────
//
// These tests assert that the artifact_stages repository preserves
// sub-second precision across Insert + GetByID. The canonical wire
// format is RFC3339Nano (per timeutil.FormatRFC3339Nano on write +
// parseRFC3339Nano on read). A regression to time.RFC3339 (no
// fractional seconds) would silently truncate the sub-second
// component and the time.Equal() assertion below would fail. The
// existing tests use a zero-nanos fixture (nowFixed) so they would
// NOT catch this regression — these tests are the canonical
// regression sentinel.

// nanoPrecisionTime is a role-neutral deterministic time with a
// non-zero sub-second component (123456789 nanos past the
// minute). The non-zero nanos are the regression sentinel: a
// switch back to time.RFC3339 would silently truncate this to
// zero, failing the time.Equal() assertion. The variable is
// role-neutral so the Insert test can use it as CreatedAt and
// any future test can repurpose it without a rename.
var nanoPrecisionTime = time.Date(2026, 7, 11, 12, 0, 0, 123456789, time.UTC)

// nanoNow is a deterministic time with a distinct non-zero
// sub-second component (987654321 nanos at 12:01:00 — a 1-minute
// gap from nanoPrecisionTime so CreatedAt < UpdatedAt is
// obvious at a glance). Used to seed the repository's nowFn
// for UpdatedAt round-trip.
var nanoNow = time.Date(2026, 7, 11, 12, 1, 0, 987654321, time.UTC)

// newNanoRepo is a tiny helper that mirrors the staging package
// pattern: build a Repository with the canonical DDL + a
// nano-precision nowFn pre-wired, so the two nano-precision
// tests share a single setup path.
func newNanoRepo(t *testing.T) *Repository {
	t.Helper()
	repo := NewRepository(setupTestDB(t))
	repo.nowFn = func() time.Time { return nanoNow }
	return repo
}

func TestRepository_Insert_NanoPrecisionRoundTrip(t *testing.T) {
	repo := newNanoRepo(t)
	ctx := context.Background()

	s := validStage()
	s.CreatedAt = nanoPrecisionTime
	if err := repo.Insert(ctx, s); err != nil {
		t.Fatalf("Insert nano-precision stage: %v", err)
	}
	got, err := repo.GetByID(ctx, s.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if !got.CreatedAt.Equal(nanoPrecisionTime) {
		t.Errorf("CreatedAt round-trip: got %v (nanos=%d), want %v (nanos=%d) — nano precision LOST",
			got.CreatedAt, got.CreatedAt.Nanosecond(), nanoPrecisionTime, nanoPrecisionTime.Nanosecond())
	}
	if !got.UpdatedAt.Equal(nanoNow) {
		t.Errorf("UpdatedAt round-trip: got %v (nanos=%d), want %v (nanos=%d) — nano precision LOST",
			got.UpdatedAt, got.UpdatedAt.Nanosecond(), nanoNow, nanoNow.Nanosecond())
	}
}

func TestRepository_MarkPublished_NanoPrecisionRoundTrip(t *testing.T) {
	repo := newNanoRepo(t)
	// nanoPublishedAt is declared local (not the package-level
	// nanoPrecisionTime) so the semantic role — the value
	// written to artifact_stages.published_at — is unambiguous.
	nanoPublishedAt := time.Date(2026, 7, 11, 12, 0, 5, 123456789, time.UTC)
	ctx := context.Background()

	s := validStage()
	if err := repo.Insert(ctx, s); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := repo.MarkPublished(ctx, s.ID, `{"kind":"drive","uri":"f-1"}`, nanoPublishedAt); err != nil {
		t.Fatalf("MarkPublished nano-precision: %v", err)
	}
	got, err := repo.GetByID(ctx, s.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.PublishedAt == nil {
		t.Fatalf("PublishedAt must be non-nil after MarkPublished")
	}
	if !got.PublishedAt.Equal(nanoPublishedAt) {
		t.Errorf("PublishedAt round-trip: got %v (nanos=%d), want %v (nanos=%d) — nano precision LOST",
			got.PublishedAt, got.PublishedAt.Nanosecond(), nanoPublishedAt, nanoPublishedAt.Nanosecond())
	}
	// UpdatedAt re-assertion is intentionally omitted: the
	// Insert test already covers UpdatedAt round-trip; this		// test's unique value-add is the PublishedAt path. Keeping
	// coverage surface disjoint keeps the regression-sentinel
	// signal unambiguous (a future regression that breaks ONLY
	// PublishedAt round-trip points straight at the Mark*
	// write path).
}

// ── InsertWithOutbox (FASE 3 / Push 3.1c hermetic TX tests) ────────
//
// These tests are the canonical SQLite-anchored regression
// sentinels for the InsertWithOutbox atomicity contract.
// They use real SQLite (in-memory) with the canonical
// production DDLs (artifact_stages migration 147 + outbox_events
// migration 092, including the ux_outbox_events_event_key
// UNIQUE index). The atomicity contract is the entire point of
// the 2-table TX primitive — a regression in the TX wrapper
// (e.g., switching to two independent Exec calls) would
// silently orphan events from their rows or vice versa; this
// test block fails LOUD on any such drift.

// eventTypeForTest is a neutral event_type used by the
// InsertWithOutbox hermetic tests. Production uses the
// `artifact.staged.v1` constant (Push 3.1c emitter) but the
// repository contract is "store the supplied bytes verbatim"
// so the test uses a separate value to avoid coupling the
// repository test to the application's event_type catalog.
const eventTypeForTest = "artifact.test.staged.v1"

// payloadForTest is the canonical payload byte slice for
// the InsertWithOutbox hermetic tests. Arbitrary well-formed
// JSON to verify the repository stores the bytes verbatim
// (no shape-interpretation, no field reordering).
var payloadForTest = []byte(`{"stage_id":"art-test-1","hash":"sha256abc","size":4096}`)

// TestRepository_InsertWithOutbox_HappyPath_AtomicCommit pins
// the 2-table atomic commit contract: BOTH the artifact_stages
// row AND the co-emitted outbox_events row commit together in
// a single SQLite TX. The test reads back via two independent
// paths (artifact.Repository.GetByID for the stage row +
// db.QueryRowContext for the outbox row) so a regression that
// commits ONE but not the OTHER surfaces immediately.
func TestRepository_InsertWithOutbox_HappyPath_AtomicCommit(t *testing.T) {
	db := setupTestDB(t)
	repo := NewRepository(db)
	repo.nowFn = func() time.Time { return nowFixed }
	ctx := context.Background()

	stage := validStage()
	eventKey, err := repo.InsertWithOutbox(ctx, stage, eventTypeForTest, payloadForTest)
	if err != nil {
		t.Fatalf("InsertWithOutbox happy: unexpected error: %v", err)
	}

	// 1. Returned eventKey MUST match the canonical convention
	//    `stage:<jobID>:<stageID>` (the producer-side dedupe
	//    anchor for downstream consumers).
	const wantEventKey = "stage:job-test-1:art-test-1"
	if eventKey != wantEventKey {
		t.Errorf("returned eventKey = %q, want %q", eventKey, wantEventKey)
	}

	// 2. artifact_stages row IS persisted (full row readable
	//    via GetByID; this is the same path the finalizer
	//    uses to scan the saga's per-job contributions).
	got, err := repo.GetByID(ctx, stage.ID)
	if err != nil {
		t.Fatalf("GetByID after InsertWithOutbox: %v (artifact row MUST be persisted)", err)
	}
	if got.State != artifact.ArtifactStageStateStaged {
		t.Errorf("artifact_stages.State = %q, want STAGED", got.State)
	}
	if !got.CreatedAt.Equal(nowFixed) {
		t.Errorf("artifact_stages.CreatedAt = %v, want %v (UTC clock source)", got.CreatedAt, nowFixed)
	}
	if !got.UpdatedAt.Equal(nowFixed) {
		t.Errorf("artifact_stages.UpdatedAt = %v, want %v (UTC clock source)", got.UpdatedAt, nowFixed)
	}

	// 3. outbox_events row IS persisted with the canonical
	//    fields. Direct SQL probe (vs the application-layer
	//    outbox.Repository abstraction) so a regression in
	//    ANY of column-name + value-mapping surfaces here.
	var (
		gotEventType, gotAggregateID, gotAggregateType, gotPayloadJSON, gotEventKey, gotStatus, gotCreatedAt string
	)
	row := db.QueryRowContext(ctx,
		`SELECT event_type, aggregate_id, aggregate_type, payload_json, event_key, status, created_at
		   FROM outbox_events WHERE event_key = ?`, wantEventKey)
	if err := row.Scan(&gotEventType, &gotAggregateID, &gotAggregateType, &gotPayloadJSON, &gotEventKey, &gotStatus, &gotCreatedAt); err != nil {
		t.Fatalf("SELECT outbox_events row after InsertWithOutbox: %v (outbox row MUST be persisted)", err)
	}
	if gotEventType != eventTypeForTest {
		t.Errorf("outbox event_type = %q, want %q", gotEventType, eventTypeForTest)
	}
	if gotAggregateID != stage.ID {
		t.Errorf("outbox aggregate_id = %q, want %q (FK-by-convention to artifact_stages row)", gotAggregateID, stage.ID)
	}
	if gotAggregateType != "artifact_stage" {
		t.Errorf("outbox aggregate_type = %q, want %q (canonical aggregate namespace)", gotAggregateType, "artifact_stage")
	}
	if gotPayloadJSON != string(payloadForTest) {
		t.Errorf("outbox payload_json = %q, want %q (verbatim round-trip)", gotPayloadJSON, string(payloadForTest))
	}
	if gotEventKey != wantEventKey {
		t.Errorf("outbox event_key = %q, want %q", gotEventKey, wantEventKey)
	}
	if gotStatus != "pending" {
		t.Errorf("outbox status = %q, want %q (initial state of the consumer drain loop)", gotStatus, "pending")
	}
	if gotCreatedAt != nowFixed.UTC().Format(time.RFC3339Nano) {
		t.Errorf("outbox created_at = %q, want %q (canonical RFC3339Nano UTC)", gotCreatedAt, nowFixed.UTC().Format(time.RFC3339Nano))
	}

	// 4. Total outbox_events row count for the canonical
	//    event_key MUST be exactly 1 (no duplicate emission).
	var rowCount int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM outbox_events WHERE event_key = ?`, wantEventKey).Scan(&rowCount); err != nil {
		t.Fatalf("COUNT outbox_events: %v", err)
	}
	if rowCount != 1 {
		t.Errorf("outbox_events row count for event_key=%q = %d, want 1", wantEventKey, rowCount)
	}
}
