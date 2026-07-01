package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	channels "github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	ytdomain "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	assetsdb "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	"go.uber.org/zap"
)

// monitor_enqueue_test.go — Blocco 3 (July 2026) outbox-based emission tests.
//
// enqueueFromAnalysis (enqueue.go) now delegates to the YoutubeDiscoveriesPort
// via CommitEnqueueOutbox (atomic MarkEnqueued + INSERT outbox). The
// JobEnqueuer port is called asynchronously by the outbox drainer
// (startOutboxDrainer in scheduler.go), NOT directly from enqueueFromAnalysis.
//
// The 2 cases below pin the canonical contract on the outbox path:
//
//  1. Happy path: enqueueFromAnalysis builds the correct payload, commits
//     the outbox entry atomically, and returns nil.
//
//  2. Outbox commit failure: when CommitEnqueueOutbox returns an error,
//     enqueueFromAnalysis records a rejection on the ledger (MarkRejected)
//     and returns the error so the orchestrator classifies OutcomeRejected.
//
// Note: idempotent retry (duplicate idempotency_key) is tested at the
// SQLite integration level (see monitor_outbox_test.go and
// youtube_discoveries_test.go).

func enqueueChannelFixture() channels.Channel {
	return channels.Channel{
		ID:            "ch-1",
		ChannelURL:    "https://www.youtube.com/@Test",
		Category:      "test-cat",
		DriveFolderID: "test-folder",
	}
}

func enqueueVideoFixture() downloader.VideoInfo {
	return downloader.VideoInfo{ID: "vid-1", Title: "Test Title"}
}

func enqueueAnalysisFixture() Analysis {
	return Analysis{
		Score:    85,
		Category: "test-cat",
		Segments: []ytdomain.Segment{
			{Start: "00:01", End: "00:30", Name: "Segment One"},
			{Start: "00:30", End: "01:00", Name: "Segment Two"},
		},
	}
}

// stubDiscoveriesForEnqueue records CommitEnqueueOutbox calls and
// returns the configured error. Satisfies YoutubeDiscoveriesPort.
type stubDiscoveriesForEnqueue struct {
	commitCalls    int
	committedIDs   []string
	committedKeys  []string
	committedJSONs []string
	rejectedIDs    []string
	rejectedErrors []string

	// commitErr, if set, is returned by CommitEnqueueOutbox.
	commitErr error
}

func (s *stubDiscoveriesForEnqueue) TryReserve(_ context.Context, _, _, _, _, _, _ string) (string, bool, int, error) {
	return "", false, 0, nil
}
func (s *stubDiscoveriesForEnqueue) MarkEnqueued(_ context.Context, _, _ string) error { return nil }
func (s *stubDiscoveriesForEnqueue) MarkRejected(_ context.Context, id, reason string, _ bool) error {
	s.rejectedIDs = append(s.rejectedIDs, id)
	s.rejectedErrors = append(s.rejectedErrors, reason)
	return nil
}
func (s *stubDiscoveriesForEnqueue) MaxDiscoveredAt(_ context.Context, _ string) (string, error) {
	return "", nil
}
func (s *stubDiscoveriesForEnqueue) CommitEnqueueOutbox(_ context.Context, discoveryID, enqueuedAt, idempotencyKey, payloadJSON string) error {
	s.commitCalls++
	s.committedIDs = append(s.committedIDs, discoveryID)
	s.committedKeys = append(s.committedKeys, idempotencyKey)
	s.committedJSONs = append(s.committedJSONs, payloadJSON)
	if s.commitErr != nil {
		return s.commitErr
	}
	return nil
}
func (s *stubDiscoveriesForEnqueue) DrainPendingOutbox(_ context.Context, _ int) ([]assetsdb.OutboxEntry, error) {
	return nil, nil
}
func (s *stubDiscoveriesForEnqueue) MarkOutboxDispatched(_ context.Context, _ int64, _ string) error {
	return nil
}
func (s *stubDiscoveriesForEnqueue) MarkOutboxFailed(_ context.Context, _ int64, _ string) error {
	return nil
}

// TestEnqueueFromAnalysis_CommitsOutboxWithCorrectPayload verifies
// that on the happy path, enqueueFromAnalysis:
//   - Calls CommitEnqueueOutbox exactly once
//   - Passes the correct discovery_id (ledgerID)
//   - Builds the idempotency key = "youtube-extract:{ledgerID}:{policyVersion}"
//   - Marshals a valid EnqueueExtractRequest with all fields populated
//   - Returns nil
func TestEnqueueFromAnalysis_CommitsOutboxWithCorrectPayload(t *testing.T) {
	stub := &stubDiscoveriesForEnqueue{}
	m := &ChannelMonitor{
		log:         zap.NewNop(),
		discoveries: stub,
	}
	ch := enqueueChannelFixture()
	info := enqueueVideoFixture()
	analysis := enqueueAnalysisFixture()
	ledgerID := "disc_test123"

	err := m.enqueueFromAnalysis(context.Background(), info, ch, analysis, ledgerID)
	if err != nil {
		t.Fatalf("enqueueFromAnalysis returned error on happy path: %v", err)
	}

	if stub.commitCalls != 1 {
		t.Fatalf("CommitEnqueueOutbox should be called exactly once, got %d", stub.commitCalls)
	}
	if stub.committedIDs[0] != ledgerID {
		t.Errorf("committed discovery_id = %q, want %q", stub.committedIDs[0], ledgerID)
	}

	// Verify idempotency key format.
	wantKey := fmt.Sprintf("youtube-extract:%s:%s", ledgerID, ChannelMonitorPolicyVersion)
	if stub.committedKeys[0] != wantKey {
		t.Errorf("idempotency_key = %q, want %q", stub.committedKeys[0], wantKey)
	}

	// Verify the marshaled payload.
	var gotReq EnqueueExtractRequest
	if err := json.Unmarshal([]byte(stub.committedJSONs[0]), &gotReq); err != nil {
		t.Fatalf("failed to unmarshal committed payload: %v", err)
	}
	if gotReq.VideoID != info.ID {
		t.Errorf("VideoID = %q, want %q", gotReq.VideoID, info.ID)
	}
	if gotReq.Title != info.Title {
		t.Errorf("Title = %q, want %q", gotReq.Title, info.Title)
	}
	if want := "https://www.youtube.com/watch?v=" + info.ID; gotReq.URL != want {
		t.Errorf("URL = %q, want %q", gotReq.URL, want)
	}
	if gotReq.Group != analysis.Category {
		t.Errorf("Group = %q, want %q (analysis.Category)", gotReq.Group, analysis.Category)
	}
	if gotReq.DriveFolderID != ch.DriveFolderID {
		t.Errorf("DriveFolderID = %q, want %q", gotReq.DriveFolderID, ch.DriveFolderID)
	}
	if len(gotReq.Segments) != len(analysis.Segments) {
		t.Errorf("Segments len = %d, want %d", len(gotReq.Segments), len(analysis.Segments))
	}
	if gotReq.Channel.ID != ch.ID {
		t.Errorf("Channel.ID = %q, want %q", gotReq.Channel.ID, ch.ID)
	}
}

// TestEnqueueFromAnalysis_CommitFailureMarksRejected verifies that
// when CommitEnqueueOutbox returns an error, enqueueFromAnalysis:
//   - Calls MarkRejected on the ledger with the error message
//   - Returns the error so the orchestrator classifies OutcomeRejected
func TestEnqueueFromAnalysis_CommitFailureMarksRejected(t *testing.T) {
	commitErr := errors.New("sqlite: database is locked")
	stub := &stubDiscoveriesForEnqueue{
		commitErr: commitErr,
	}
	m := &ChannelMonitor{
		log:         zap.NewNop(),
		discoveries: stub,
	}
	ch := enqueueChannelFixture()
	info := enqueueVideoFixture()
	analysis := enqueueAnalysisFixture()
	ledgerID := "disc_test456"

	err := m.enqueueFromAnalysis(context.Background(), info, ch, analysis, ledgerID)
	if err == nil {
		t.Fatal("enqueueFromAnalysis should return error on CommitEnqueueOutbox failure")
	}

	// Verify CommitEnqueueOutbox was called.
	if stub.commitCalls != 1 {
		t.Fatalf("CommitEnqueueOutbox should be called once, got %d", stub.commitCalls)
	}

	// Verify MarkRejected was called.
	if len(stub.rejectedIDs) != 1 {
		t.Fatalf("MarkRejected should be called once on commit failure, got %d", len(stub.rejectedIDs))
	}
	if stub.rejectedIDs[0] != ledgerID {
		t.Errorf("rejected discovery_id = %q, want %q", stub.rejectedIDs[0], ledgerID)
	}
	if stub.rejectedErrors[0] == "" {
		t.Error("rejection error should not be empty")
	}
}

// TestEnqueueFromAnalysis_NilDiscoveriesReturnsError verifies that
// when discoveries port is not wired, enqueueFromAnalysis returns
// an error early (no panic, no nil deref).
func TestEnqueueFromAnalysis_NilDiscoveriesReturnsError(t *testing.T) {
	m := &ChannelMonitor{
		log: zap.NewNop(),
		// discoveries nil
	}
	ch := enqueueChannelFixture()
	info := enqueueVideoFixture()
	analysis := enqueueAnalysisFixture()

	err := m.enqueueFromAnalysis(context.Background(), info, ch, analysis, "disc_test")
	if err == nil {
		t.Fatal("enqueueFromAnalysis should return error when discoveries port is nil")
	}
}
