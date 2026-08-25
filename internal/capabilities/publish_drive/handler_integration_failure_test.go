package publish_drive

import (
	"context"
	"errors"
	"strings"
	"testing"

	artifact "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// Push E.1 (July 2026): handler_integration_failure_test.go is one
// of 3 sister files split from the original single-file
// handler_integration_test.go to satisfy max_lines_per_file=500
// (cmd/archcheck). Hosts the drain-failure tests:
//
//   - TestHandlerIntegration_Drain_MalformedEnvelope_ErrorsBeforeAnyCAS
//   - TestHandlerIntegration_Drain_PublisherFailureLeavesRowStaged
//
// Both tests assert the godlike/07 fail-closed contract that a
// drain failure leaves the row in its prior STAGED state with no
// PublishedLocation written — so a retry can pick it up. See
// handler_integration_helpers_test.go for the DDL bridge + repo +
// envelope + stub Publisher shared across this test family.

// ── Test 3: Malformed envelope — handler errors BEFORE any CAS ─────────

// TestHandlerIntegration_Drain_MalformedEnvelope_ErrorsBeforeAnyCAS:
// a structurally invalid PayloadJSON must short-circuit the handler
// before Publisher.Publish OR Repository.MarkPublished are touched.
// State MUST stay STAGED, published_location MUST stay empty, no
// Publisher.Publish call MUST be observed. This pins the
// fail-fast-at-validation contract — a regression that lets a
// malformed envelope reach the publisher would cause Drive
// uploads from junk data; this test catches that before prod.
func TestHandlerIntegration_Drain_MalformedEnvelope_ErrorsBeforeAnyCAS(t *testing.T) {
	db := setupTestDB(t)
	repo := newRepoForTest(db)
	ctx := context.Background()

	stageID := "art-integ-3"
	if err := repo.Insert(ctx, validStageForTest(stageID)); err != nil {
		t.Fatalf("setup Insert: %v", err)
	}

	pub := &integrationStubPublisher{
		result: &delivery.PublishResult{
			FileID:      "drive-file-bad-env-1",
			FolderID:    "drive-folder-bad-env-1",
			Destination: delivery.DestinationKey("voiceover"),
		},
	}
	core, logs := observer.New(zapcore.InfoLevel)
	h, err := NewHandler(repo, pub, zap.New(core))
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	badEvt := outboxevents.Event{
		EventType:   EventTypeArtifactStaged,
		PayloadJSON: "[not-json",
		EventKey:    "stage:bad:env",
	}
	err = h.Handle(ctx, badEvt)
	if err == nil {
		t.Fatalf("Handle malformed envelope: expected error, got nil")
	}
	if !errors.Is(err, ErrInvalidPayload) {
		t.Errorf("err = %v, want chain includes ErrInvalidPayload", err)
	}

	// Repository state MUST be untouched.
	got, getErr := repo.GetByID(ctx, stageID)
	if getErr != nil {
		t.Fatalf("GetByID post-malformed-env: %v", getErr)
	}
	if got.State != artifact.ArtifactStageStateStaged {
		t.Errorf("state = %q, want STAGED (malformed envelope MUST NOT have triggered MarkPublished)", got.State)
	}
	if got.PublishedLocation != "" {
		t.Errorf("published_location = %q, want empty (no MarkPublished happened)", got.PublishedLocation)
	}
	if got := len(pub.calls); got != 0 {
		t.Errorf("Publisher.Publish calls = %d, want 0 (validate gate fires before publish)", got)
	}

	// No drain-success / fence log entry should be present.
	for _, e := range logs.All() {
		if strings.Contains(e.Message, "artifact published") || strings.Contains(e.Message, "terminal-state fence observed") {
			t.Errorf("unexpected log entry on malformed envelope: %q", e.Message)
		}
	}
}

// ── Test 4: Publisher.Publish failure — row stays STAGED, no CAS ────

// TestHandlerIntegration_Drain_PublisherFailureLeavesRowStaged:
// a Drive upload failure (stub returns canned error) MUST cause
// handler.Handle to return a wrapped error, with NO
// Repository.MarkPublished call having been issued (the row
// MUST stay STAGED + empty published_location). The publish_drive
// contract is: Publisher failure leaves the stage in the
// publishable state so a retry can pick it up; a regression that
// flips the state to FAILED_PERMANENT mid-flight would block
// retries permanently (forward-pointer — the staging→artifact
// saga would never reach its PUBLISHED end-state).
func TestHandlerIntegration_Drain_PublisherFailureLeavesRowStaged(t *testing.T) {
	db := setupTestDB(t)
	repo := newRepoForTest(db)
	ctx := context.Background()

	stageID := "art-integ-4"
	if err := repo.Insert(ctx, validStageForTest(stageID)); err != nil {
		t.Fatalf("setup Insert: %v", err)
	}

	pubErr := errors.New("simulated Drive 503 (upload service unavailable)")
	pub := &integrationStubPublisher{pubErr: pubErr}

	_, evt := validEnvelopeForTest(stageID, "job-integ-1")
	core, logs := observer.New(zapcore.InfoLevel)
	h, err := NewHandler(repo, pub, zap.New(core))
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	err = h.Handle(ctx, evt)
	if err == nil {
		t.Fatalf("Handle publisher-fail: expected non-nil error")
	}
	if !errors.Is(err, pubErr) {
		t.Errorf("err chain DOES NOT include injected sentinel: %v", err)
	}
	if !strings.Contains(err.Error(), "Publisher.Publish") {
		t.Errorf("err = %v, want contains 'Publisher.Publish'", err)
	}

	// Row MUST still be STAGED (Publisher failed BEFORE
	// MarkPublished — the publish_drive contract is to leave
	// the row publishable so a retry can pick it up).
	got, getErr := repo.GetByID(ctx, stageID)
	if getErr != nil {
		t.Fatalf("GetByID post-publisher-fail: %v", getErr)
	}
	if got.State != artifact.ArtifactStageStateStaged {
		t.Errorf("state = %q, want STAGED (Publisher-failure MUST NOT have flipped state)", got.State)
	}
	if got.PublishedLocation != "" {
		t.Errorf("published_location = %q, want empty (no MarkPublished CAS happened)", got.PublishedLocation)
	}

	// No drain-success / fence log entry.
	for _, e := range logs.All() {
		if strings.Contains(e.Message, "artifact published") || strings.Contains(e.Message, "terminal-state fence observed") {
			t.Errorf("unexpected log entry on publisher-fail: %q", e.Message)
		}
	}
}
