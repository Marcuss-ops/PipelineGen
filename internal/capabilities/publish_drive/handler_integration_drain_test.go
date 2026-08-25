package publish_drive

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	artifact "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// Push E.1 (July 2026): handler_integration_drain_test.go is one of 3
// sister files split from the original single-file
// handler_integration_test.go to satisfy max_lines_per_file=500
// (cmd/archcheck). Hosts the drain-completion / no-op tests:
//
//   - TestHandlerIntegration_Drain_HappyPath_RealSQLite
//   - TestHandlerIntegration_Drain_TerminalStateFenceIsNoOp
//   - TestHandlerIntegration_Drain_IdempotencyKeyStableAcrossTwoCycles
//
// The DDL bridge (setupTestDB), artifact_stages row builder
// (validStageForTest), outbox_events envelope builder
// (validEnvelopeForTest), deterministic clock seam (nowFixed),
// and the stub Publisher (integrationStubPublisher) live in the
// sister file handler_integration_helpers_test.go — same
// `package publish_drive_test` so they are reachable without any
// cross-file import ceremony (idempotency-key test pins
// Repository.GetByID to read back the canonical hash, so the
// helpers' validStageForTest contract is the producer of the
// value read back; see TestHandlerIntegration_Drain_IdempotencyKeyStableAcrossTwoCycles
// for the round-trip-pin detail).

// ── Test 1: Happy path — drain succeeds, MarkPublished persists ────────

// TestHandlerIntegration_Drain_HappyPath_RealSQLite: a STAGED
// row pre-inserted through the real Repository is drained by
// handler.Handle with a canonical envelope. The handler MUST
// (a) call Publisher.Publish exactly once, (b) round-trip the
// returned PublishResult into a canonical artifact.PublishedLocation
// JSON, (c) issue Repository.MarkPublished with the canonical
// JSON, and (d) transition the row to state=PUBLISHED +
// non-null published_at. Direct SQLite probes below the
// application layer pin the row-level state, so any drift
// between Repository's MarkPublished SQL and artifact.ArtifactStageStatePublished
// constant surfaces here.
func TestHandlerIntegration_Drain_HappyPath_RealSQLite(t *testing.T) {
	db := setupTestDB(t)
	repo := newRepoForTest(db)
	ctx := context.Background()

	stageID := "art-integ-1"
	if err := repo.Insert(ctx, validStageForTest(stageID)); err != nil {
		t.Fatalf("setup Insert: %v", err)
	}

	pub := &integrationStubPublisher{
		result: &delivery.PublishResult{
			FileID:       "drive-file-integ-1",
			FolderID:     "drive-folder-integ-1",
			Destination:  delivery.DestinationKey("voiceover"),
			Action:       delivery.PublishActionCreated,
			PathSegments: []string{"voiceover", "test"},
		},
	}
	_, evt := validEnvelopeForTest(stageID, "job-integ-1")

	core, logs := observer.New(zapcore.InfoLevel)
	h, err := NewHandler(repo, pub, zap.New(core))
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	if err := h.Handle(ctx, evt); err != nil {
		t.Fatalf("Handle real-SQLite drain: %v (handler MUST NOT error on a valid envelope)", err)
	}

	// Direct DB-side assertion (independent of application layer).
	var (
		gotState, gotPublishedLoc, gotPublishedAt string
	)
	row := db.QueryRowContext(ctx,
		`SELECT state, published_location, IFNULL(published_at, '') FROM artifact_stages WHERE id = ?`, stageID)
	if err := row.Scan(&gotState, &gotPublishedLoc, &gotPublishedAt); err != nil {
		t.Fatalf("SELECT state+published_location+published_at: %v", err)
	}
	if gotState != string(artifact.ArtifactStageStatePublished) {
		t.Errorf("artifact_stages.state = %q, want %q (MarkPublished CAS MUST have flipped state to PUBLISHED)", gotState, artifact.ArtifactStageStatePublished)
	}
	if gotPublishedLoc == "" {
		t.Fatalf("published_location IS empty after drain — PublishedLocation JSON MUST have been persisted via MarkPublished")
	}
	var loc artifact.PublishedLocation
	if err := json.Unmarshal([]byte(gotPublishedLoc), &loc); err != nil {
		t.Fatalf("published_location is not valid JSON: %v (raw=%q)", err, gotPublishedLoc)
	}
	if loc.ArtifactID != stageID {
		t.Errorf("PublishedLocation.ArtifactID = %q, want %q", loc.ArtifactID, stageID)
	}
	if loc.Kind != artifact.LocationKindDrive {
		t.Errorf("PublishedLocation.Kind = %q, want %q", loc.Kind, artifact.LocationKindDrive)
	}
	if loc.URI != "drive-file-integ-1" {
		t.Errorf("PublishedLocation.URI = %q, want %q (stub publisher's FileID MUST propagate into the JSON column)", loc.URI, "drive-file-integ-1")
	}
	if gotPublishedAt == "" {
		t.Errorf("published_at IS empty after drain — clock seam MUST have written the UTC RFC3339Nano stamp")
	}

	// Stub publisher called exactly once with the decoded fields.
	if got := len(pub.calls); got != 1 {
		t.Fatalf("Publisher.Publish call count = %d, want 1", got)
	}
	if got := pub.calls[0].Destination; got != delivery.DestinationKey("voiceover") {
		t.Errorf("PublishRequest.Destination = %q, want %q (after parsing 'drive:voiceover/test')", got, "voiceover")
	}
	if got := pub.calls[0].Subject; got != "test" {
		t.Errorf("PublishRequest.Subject = %q, want %q", got, "test")
	}
	if got := pub.calls[0].LocalPath; !strings.HasSuffix(got, stageID) {
		t.Errorf("PublishRequest.LocalPath = %q, want suffix %q", got, stageID)
	}

	// Canonical "artifact published" log line was emitted exactly once.
	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("log entries = %d, want 1 (drain success emits one canonical Info entry)", len(entries))
	}
	if !strings.Contains(entries[0].Message, "artifact published") {
		t.Errorf("log message = %q, want contains 'artifact published'", entries[0].Message)
	}
}

// ── Test 2: Re-delivery — terminal-state fence ⇒ handler returns nil ──

// TestHandlerIntegration_Drain_TerminalStateFenceIsNoOp: a row
// in a true terminal state (SUCCEEDED — set up via the canonical
// MarkSucceeded path) is drained again with a fresh envelope —
// the second drain MUST observe the terminal-state fence
// (MarkPublished's fenced-CAS UPDATE matches 0 rows on a
// SUCCEEDED row → ErrTerminalStateRejection), the handler MUST
// swallow it (return nil — idem re-delivery = no-op), and the
// row MUST remain unchanged.
func TestHandlerIntegration_Drain_TerminalStateFenceIsNoOp(t *testing.T) {
	db := setupTestDB(t)
	repo := newRepoForTest(db)
	ctx := context.Background()

	stageID := "art-integ-2"

	// Pre-seed: simulate a fully-completed prior drain by
	// Insert + MarkSucceeded (SUCCEEDED is the canonical
	// terminal state — MarkPublished's fenced CAS correctly
	// rejects re-promotion to PUBLISHED from this state).
	if err := repo.Insert(ctx, validStageForTest(stageID)); err != nil {
		t.Fatalf("setup Insert: %v", err)
	}
	if err := repo.MarkSucceeded(ctx, stageID); err != nil {
		t.Fatalf("setup MarkSucceeded: %v", err)
	}

	pub := &integrationStubPublisher{
		result: &delivery.PublishResult{
			FileID:       "drive-file-redelivery-1",
			FolderID:     "drive-folder-redelivery-1",
			Destination:  delivery.DestinationKey("voiceover"),
			Action:       delivery.PublishActionUpdated, // updates existing file
			PathSegments: []string{"voiceover", "test"},
		},
	}
	_, evt := validEnvelopeForTest(stageID, "job-integ-1")
	core, logs := observer.New(zapcore.InfoLevel)
	h, err := NewHandler(repo, pub, zap.New(core))
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	if err := h.Handle(ctx, evt); err != nil {
		t.Fatalf("Handle redelivery (terminal fence): err = %v, want nil (idempotent no-op contract)", err)
	}

	// Row MUST still be SUCCEEDED with EMPTY published_location
	// — the redelivery drain MUST NOT have overwritten anything.
	var gotState, gotPublishedLoc string
	row := db.QueryRowContext(ctx,
		`SELECT state, published_location FROM artifact_stages WHERE id = ?`, stageID)
	if err := row.Scan(&gotState, &gotPublishedLoc); err != nil {
		t.Fatalf("SELECT post-redelivery: %v", err)
	}
	if gotState != string(artifact.ArtifactStageStateSucceeded) {
		t.Errorf("state = %q, want %q (terminal-state fence MUST preserve SUCCEEDED)", gotState, artifact.ArtifactStageStateSucceeded)
	}
	if gotPublishedLoc != "" {
		t.Errorf("published_location = %q, want empty (terminal-state fence MUST NOT have written a fresh PublishedLocation)", gotPublishedLoc)
	}

	// Drive Publisher IS called on redelivery (the handler's
	// full drain path runs in full; only the MarkPublished CAS
	// observes the fence).
	if got := len(pub.calls); got != 1 {
		t.Errorf("Publisher.Publish call count = %d, want 1 (drain re-runs in full)", got)
	}

	// Canonical "terminal-state fence observed" log line emitted.
	hasFenceLog := false
	for _, e := range logs.All() {
		if strings.Contains(e.Message, "terminal-state fence observed") {
			hasFenceLog = true
		}
	}
	if !hasFenceLog {
		t.Errorf("expected log entry 'terminal-state fence observed'; got messages=%v", logs.All())
	}
}

// ── Test 5: IdempotencyKey stability across two drain cycles ─────────────

// TestHandlerIntegration_Drain_IdempotencyKeyStableAcrossTwoCycles:
// the canonical delivery.DeriveIdempotencyKey produces a
// deterministic identity from the per-Drain inputs
// (destination key + StageID + Hash + SourceVersion=1). The
// handler threads this key into PublishRequest.IdempotencyKey
// on every drain (handler.go:236-244), and the SAME envelope
// inputs across two drain cycles MUST produce the SAME key —
// otherwise Drive-side dedup (appProperties lookup keyed by
// pipelinegen_idempotency_key, see
// internal/infrastructure/drive/uploader_put_helpers.go:30-36)
// breaks across process restarts, retries, and the outbox
// redelivery path.
//
// Pins the cross-session dedup guarantee via two assertions:
//   - (a) pub.calls[0].IdempotencyKey == pub.calls[1].IdempotencyKey
//     across two handler.Handle calls with identical envelopes.
//   - (b) both captures equal the canonical
//     delivery.DeriveIdempotencyKey output — detects drift if
//     the production handler swapped to a different hash or
//     dropped a field from the input tuple.
//
// Test-private coupling note: the test pulls the canonical
// SHA-256 hash for `wantKey` from the actual inserted row via
// Repository.GetByID — NOT from a duplicated literal — so a
// future validStageForTest hash change cannot silently drift
// this test's expectations.
func TestHandlerIntegration_Drain_IdempotencyKeyStableAcrossTwoCycles(t *testing.T) {
	db := setupTestDB(t)
	repo := newRepoForTest(db)
	ctx := context.Background()

	stageID := "art-cycle-1"
	if err := repo.Insert(ctx, validStageForTest(stageID)); err != nil {
		t.Fatalf("setup Insert: %v", err)
	}

	pub := &integrationStubPublisher{
		result: &delivery.PublishResult{
			FileID:       "drive-file-cycle-1",
			FolderID:     "drive-folder-cycle-1",
			Destination:  delivery.DestinationKey("voiceover"),
			Action:       delivery.PublishActionCreated,
			PathSegments: []string{"voiceover", "test"},
		},
	}
	_, evt := validEnvelopeForTest(stageID, "job-cycle-1")
	core, _ := observer.New(zapcore.InfoLevel)
	h, err := NewHandler(repo, pub, zap.New(core))
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	// Canonical expected key — DeriveIdempotencyKey with the
	// publisher-visible Destination key ("voiceover" — parsed
	// from the envelope's `drive:voiceover/test`), the StageID,
	// the SHA-256 hash as actually present in the artifact_stages
	// row at drain time (read back via Repository.GetByID so the
	// test stays coupled to validStageForTest's canonical
	// literal via the Repository — NOT via a duplicated literal
	// in this test body, which would silently drift if the
	// helper's Hash ever changes), and the handler's hardcoded
	// SourceVersion=1 (handler.go:236-244).
	pre, getErr := repo.GetByID(ctx, stageID)
	if getErr != nil {
		t.Fatalf("GetByID pre-drain (to anchor canonical hash): %v", getErr)
	}
	wantKey := delivery.DeriveIdempotencyKey(
		delivery.DestinationKey("voiceover"),
		stageID,
		pre.Hash, // canonical hash from the actual inserted row
		1,        // mirrors handler.go's hardcoded SourceVersion
	)

	// Drain #1 — first drain transitions the row STAGED → PUBLISHED.
	if err := h.Handle(ctx, evt); err != nil {
		t.Fatalf("Handle drain #1: %v (must succeed on canonical envelope)", err)
	}
	// Drain #2 — same envelope, fresh call. The row is now in
	// PUBLISHED state; the handler's MarkPublished fenced-CAS
	// (Path B fixup, this test now documents the post-fix
	// behavior) is gated on
	// `state NOT IN ('PUBLISHED','SUCCEEDED','FAILED_PERMANENT')`.
	// The CAS matches ZERO rows on a re-delivery to PUBLISHED
	// and returns ErrTerminalStateRejection, which the handler
	// safely swallows as a no-op success (the canonical
	// re-delivery contract — handler.go:316-321). Key derivation
	// happens BEFORE the CAS, so both drains thread the same
	// IdempotencyKey into Publisher.Publish.
	if err := h.Handle(ctx, evt); err != nil {
		t.Fatalf("Handle drain #2: %v (idempotent re-drive of canonical envelope must NOT error)", err)
	}

	if got := len(pub.calls); got != 2 {
		t.Fatalf("Publisher.Publish call count = %d, want 2 (one per drain cycle)", got)
	}
	key1, key2 := pub.calls[0].IdempotencyKey, pub.calls[1].IdempotencyKey
	if key1 == "" {
		t.Fatal("drain #1 IdempotencyKey IS empty — handler must thread a non-empty key into PublishRequest (cross-session Drive dedup contract)")
	}
	if key1 != key2 {
		t.Errorf("IdempotencyKey across two drain cycles = (%q, %q) — want EQUAL (cross-session Drive dedup broken; same envelope inputs MUST produce the same key)", key1, key2)
	}
	if key1 != wantKey {
		t.Errorf("IdempotencyKey = %q, want canonical %q (drift in handler.go's DeriveIdempotencyKey inputs — verify destination key + StageID + Hash + SourceVersion=1 contract)", key1, wantKey)
	}
}
