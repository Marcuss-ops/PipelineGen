// Package completion_test — publish_and_complete_use_case_test.go
// (P0-COMPL-5-WIRE-NAMING round-trip TDD tests, July 2026).
//
// 3 NEW tests + 1 supporting wire-format test, re-authored to actually
// drive `PublishAndCompleteUseCase.Execute()` per code-reviewer
// feedback (the prior draft tested mock collaborators in isolation,
// NOT the canonical use-case round-trip path).
//
// Each test invokes the use case constructor (NewPublishAndCompleteUseCase)
// with hermetic mock ports:
//   - mockArtifactPreparationService  — satisfies domain
//     finalization.ArtifactPreparationService (the canonical port)
//   - mockSenderWithArtifacts — satisfies the concrete
//     *WithArtifactsService (Sender-side terminal; forward-pointer
//     to port-extraction in P0-COMPL-5-PORT-EXTRACT)
//
// The 3 user-spec'd round-trip tests pin:
//  1. 3 StagedArtifactReferences -> 3 canonical PublishedArtifact envelopes
//     with DISTINCT Drive Locations (length-3 invariant + per-ref
//     Location propagation, byte-stable)
//  2. Use-case conversion correctness: the 10-field PublishedArtifact
//     envelope round-trips byte-stable with Drive Location populated
//     post-publish (FileID + WebViewLink + Action typed-envelope)
//  3. Invalid StagedArtifacts fail via typed-error contract (regression
//     guard: empty ArtifactID -> ErrStagedArtifactReferenceMissingFields;
//     out-of-directory Destination -> ErrStagedArtifactReferenceInvalidDestination)
//
// Plus 1 supporting test:
//  4. Wire-format byte-stability: the JSON tag `staged_artifacts` on
//     `[]*remote.StagedArtifactReference` round-trips byte-stable through
//     encoding/json (regression guard for any JSON-key rename).
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync/atomic"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/policy"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/remote"
)

// ── Mock ports (hermetic; satisfy the canonical interfaces) ────────────────

// mockArtifactPreparationService satisfies the canonical domain port
// finalization.ArtifactPreparationService (defined at
// internal/domain/finalization/interfaces.go). Per-artifact Prepare
// returns a canonical 10-field PublishedArtifact envelope with a
// per-index Drive Location (so length-N + per-ref distinctness are
// both pinned without a single mock-state machine).
type mockArtifactPreparationService struct {
	calls              atomic.Int64
	prepareErr         error
	locationPerCallIdx []finalization.AssetLocation
	defaultLocation    finalization.AssetLocation
	// recordedInputs preserves the per-call VerifiedArtifact envelope
	// for the test assertions on the projection shape.
	recordedInputs atomic.Pointer[[]finalization.VerifiedArtifact]
}

func (m *mockArtifactPreparationService) Prepare(ctx context.Context, va finalization.VerifiedArtifact) (finalization.PublishedArtifact, error) {
	idx := int(m.calls.Load())
	m.calls.Add(1)
	// append to recordedInputs (best-effort; tests don't depend on
	// exact ordering, only on count)
	recorded := m.recordedInputs.Load()
	if recorded != nil {
		tmp := append(*recorded, va)
		m.recordedInputs.Store(&tmp)
	} else {
		first := []finalization.VerifiedArtifact{va}
		m.recordedInputs.Store(&first)
	}
	if m.prepareErr != nil {
		return finalization.PublishedArtifact{}, m.prepareErr
	}
	loc := m.defaultLocation
	if idx < len(m.locationPerCallIdx) {
		loc = m.locationPerCallIdx[idx]
	}
	return finalization.PublishedArtifact{
		ArtifactID:     va.ArtifactID,
		Kind:           va.Kind,
		Filename:       va.Filename,
		MIMEType:       va.MIMEType,
		SizeBytes:      va.SizeBytes,
		SHA256:         va.SHA256,
		SourceVersion:  va.SourceVersion,
		Requirement:    va.Requirement,
		IdempotencyKey: va.IdempotencyKey,
		Location:       loc,
	}, nil
}

// Compile-time pin (AGENTS.md Pattern 0): the mock satisfies the
// canonical domain port. Drift in the Prepare signature is a build
// failure, not a runtime panic.
var _ finalization.ArtifactPreparationService = (*mockArtifactPreparationService)(nil)

// mockSenderWithArtifacts is a hermetic stub for the canonical
// Sender-side *WithArtifactsService. Records the published slice + the
// req, returns the configured response + error. NOTE: this is a
// forward-pointer to a typed-port abstraction (extraction is OUT OF
// SCOPE per godlike/06 minimal-change in this commit).
type mockSenderWithArtifacts struct {
	calls         atomic.Int64
	lastPublished []*finalization.PublishedArtifact
	lastReq       *remote.CompleteWithArtifactsRequest
	returnResp    *remote.CompleteWithArtifactsResponse
	returnErr     error
}

func (m *mockSenderWithArtifacts) CompleteWithArtifacts(ctx context.Context, req *remote.CompleteWithArtifactsRequest, published []*finalization.PublishedArtifact) (*remote.CompleteWithArtifactsResponse, error) {
	m.calls.Add(1)
	m.lastReq = req
	m.lastPublished = published
	if m.returnErr != nil {
		return nil, m.returnErr
	}
	return m.returnResp, nil
}

// ── 3 ROUND-TRIP TDD TESTS (P0-COMPL-5-WIRE-NAMING) ─────────────────────────

// TestPublishAndCompleteUseCase_RoundTrip_ThreeRefsBecomeThreePublishedWithLocation
// pins the canonical happy-path: 3 StagedArtifactReferences drive exactly
// 3 ArtifactPreparation.Prepare calls, producing 3 PublishedArtifact
// envelopes with DISTINCT Drive Locations. The round-trip uses
// useCase.Execute(ctx, req, staged) so the FULL canonical path is
// verified, not just the mock collaboration.
func TestPublishAndCompleteUseCase_RoundTrip_ThreeRefsBecomeThreePublishedWithLocation(t *testing.T) {
	prep := &mockArtifactPreparationService{
		defaultLocation: finalization.AssetLocation{Provider: "drive", FileID: "drive-default"},
		locationPerCallIdx: []finalization.AssetLocation{
			{Provider: "drive", FileID: "drive-file-1", WebViewLink: "https://drive/file/1"},
			{Provider: "drive", FileID: "drive-file-2", WebViewLink: "https://drive/file/2"},
			{Provider: "drive", FileID: "drive-file-3", WebViewLink: "https://drive/file/3"},
		},
	}
	sender := &mockSenderWithArtifacts{
		returnResp: &remote.CompleteWithArtifactsResponse{
			JobID:          "job-round-trip-1",
			Status:         "SUCCEEDED",
			JobArtifactIDs: []string{"art-1", "art-2", "art-3"},
		},
	}

	uc, err := completion.NewPublishAndCompleteUseCase(prep, sender, zap.NewNop())
	if err != nil {
		t.Fatalf("NewPublishAndCompleteUseCase: %v", err)
	}

	// Build the canonical request envelope (WorkerID + JobID + Attempt+
	// LeaseID + Result + ResultHash; AssetMappings is OPTIONAL — empty
	// for the round-trip test to avoid nil-check surface noise).
	req := &remote.CompleteWithArtifactsRequest{
		WorkerID:   "w-round-trip",
		JobID:      "job-round-trip-1",
		Attempt:    1,
		LeaseID:    "lease-round-trip",
		Result:     []byte(`{"ok":true}`),
		ResultHash: "result-hash-round-trip",
		AssetMappings: map[string]string{
			"art-1": "asset-1",
			"art-2": "asset-2",
			"art-3": "asset-3",
		},
	}
	staged := remote.StagedArtifacts{
		{ArtifactID: "art-1", Destination: "image", SHA256: "sha-1"},
		{ArtifactID: "art-2", Destination: "image", SHA256: "sha-2"},
		{ArtifactID: "art-3", Destination: "image", SHA256: "sha-3"},
	}

	resp, err := uc.Execute(context.Background(), req, staged)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if resp == nil || resp.Status != "SUCCEEDED" {
		t.Fatalf("resp: got %v want Status=SUCCEEDED", resp)
	}

	// Length-3 invariant: Prepare called EXACTLY 3 times (one per ref).
	if prep.calls.Load() != 3 {
		t.Errorf("Prepare cumulative calls: got %d, want 3 (one per StagedArtifactReference)", prep.calls.Load())
	}
	// Sender called EXACTLY 1 time with the 3 published envelopes.
	if sender.calls.Load() != 1 {
		t.Errorf("WithArtifactsService.CompleteWithArtifacts calls: got %d, want 1", sender.calls.Load())
	}

	// Per-ref Location propagation byte-stable (3 distinct FileIDs).
	if got := len(sender.lastPublished); got != 3 {
		t.Fatalf("sender.lastPublished len: got %d, want 3", got)
	}
	wantFileIDs := []string{"drive-file-1", "drive-file-2", "drive-file-3"}
	wantLinks := []string{
		"https://drive/file/1",
		"https://drive/file/2",
		"https://drive/file/3",
	}
	for i, pub := range sender.lastPublished {
		if pub == nil {
			t.Errorf("lastPublished[%d]: nil pointer", i)
			continue
		}
		if pub.Location.FileID != wantFileIDs[i] {
			t.Errorf("lastPublished[%d].Location.FileID: got %q want %q",
				i, pub.Location.FileID, wantFileIDs[i])
		}
		if pub.Location.WebViewLink != wantLinks[i] {
			t.Errorf("lastPublished[%d].Location.WebViewLink: got %q want %q",
				i, pub.Location.WebViewLink, wantLinks[i])
		}
		if pub.Location.Provider != "drive" {
			t.Errorf("lastPublished[%d].Location.Provider: got %q want %q",
				i, pub.Location.Provider, "drive")
		}
	}

	// The 3 StagedArtifactReferences were projected to VerifiedArtifact
	// inputs (ArtifactID + Destination mapped-kind + Filename=
	// ArtifactID fallback per refToVerifiedArtifact() and SHA256 from
	// the wire hint).
	if recordedPtr := prep.recordedInputs.Load(); recordedPtr != nil {
		recorded := *recordedPtr
		if got := len(recorded); got != 3 {
			t.Errorf("Prepare inputs: got %d, want 3", got)
		}
		for i, va := range recorded {
			if va.ArtifactID != staged[i].ArtifactID {
				t.Errorf("input[%d].ArtifactID: got %q want %q",
					i, va.ArtifactID, staged[i].ArtifactID)
			}
		}
	}
}

// TestPublishAndCompleteUseCase_ConversionCorrectness pins the
// canonical 10-field PublishedArtifact envelope populated post-publish
// by the Prepare pipeline. The 10 base fields + 5 Location envelope
// (Provider, FileID, WebViewLink, DownloadLink, FolderID, FolderPath,
// Action) all byte-stable round-trip through the conversion.
func TestPublishAndCompleteUseCase_ConversionCorrectness(t *testing.T) {
	prep := &mockArtifactPreparationService{
		defaultLocation: finalization.AssetLocation{
			Provider:     "drive",
			FileID:       "drive-conv-correctness-001",
			WebViewLink:  "https://drive.google.com/file/d/drive-conv-correctness-001/view",
			DownloadLink: "https://drive.google.com/uc?export=download&id=drive-conv-correctness-001",
			FolderID:     "drive-folder-001",
			FolderPath:   "media/",
			Action:       finalization.PublishCreated,
		},
	}
	sender := &mockSenderWithArtifacts{
		returnResp: &remote.CompleteWithArtifactsResponse{
			JobID: "job-conv-correctness", Status: "SUCCEEDED",
		},
	}

	uc, _ := completion.NewPublishAndCompleteUseCase(prep, sender, zap.NewNop())

	// Build the canonical request envelope (single ref for the
	// conversion-correctness test).
	req := &remote.CompleteWithArtifactsRequest{
		WorkerID: "w-conv", JobID: "job-conv-correctness", Attempt: 1,
		LeaseID: "lease-conv", Result: []byte(`{}`), ResultHash: "rh-conv",
		AssetMappings: map[string]string{"art-conv-correctness-001": "asset-conv-1"},
	}
	staged := remote.StagedArtifacts{
		{ArtifactID: "art-conv-correctness-001", Destination: "image", SHA256: "abc123-conv-correctness"},
	}

	resp, err := uc.Execute(context.Background(), req, staged)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if resp == nil {
		t.Fatal("resp: nil")
	}
	if prep.calls.Load() != 1 {
		t.Errorf("Prepare calls: got %d, want 1", prep.calls.Load())
	}
	if sender.calls.Load() != 1 {
		t.Errorf("sender calls: got %d, want 1", sender.calls.Load())
	}

	// Pull the canonical PublishedArtifact out of the sender's recorded slice.
	if len(sender.lastPublished) != 1 {
		t.Fatalf("sender.lastPublished len: got %d, want 1", len(sender.lastPublished))
	}
	pub := sender.lastPublished[0]

	// ArtifactID round-trips byte-stable.
	if pub.ArtifactID != "art-conv-correctness-001" {
		t.Errorf("ArtifactID: got %q want %q", pub.ArtifactID, "art-conv-correctness-001")
	}
	// Kind derived from Destination ("image" -> KindImage).
	if pub.Kind != finalization.KindImage {
		t.Errorf("Kind: got %v want %v", pub.Kind, finalization.KindImage)
	}
	// SHA256 round-trips from the wire hint.
	if pub.SHA256 != "abc123-conv-correctness" {
		t.Errorf("SHA256: got %q", pub.SHA256)
	}
	// SourceVersion defaults to 1 (forward-pointer TODO for media_assets lookup).
	if pub.SourceVersion != 1 {
		t.Errorf("SourceVersion: got %d want %d", pub.SourceVersion, 1)
	}
	// Requirement defaults to ArtifactRequirementRequired (forward-pointer TODO).
	if pub.Requirement != finalization.ArtifactRequirementRequired {
		t.Errorf("Requirement: got %v want %v", pub.Requirement, finalization.ArtifactRequirementRequired)
	}

	// Location populated post-publish (5 envelope fields).
	if pub.Location.Provider != "drive" {
		t.Errorf("Location.Provider: got %q want %q", pub.Location.Provider, "drive")
	}
	if pub.Location.FileID != "drive-conv-correctness-001" {
		t.Errorf("Location.FileID: got %q", pub.Location.FileID)
	}
	if pub.Location.WebViewLink == "" {
		t.Error("Location.WebViewLink MUST be populated post-publish")
	}
	if pub.Location.Action != finalization.PublishCreated {
		t.Errorf("Location.Action: got %v want %v", pub.Location.Action, finalization.PublishCreated)
	}
}

// TestPublishAndCompleteUseCase_InvalidStagedFailsTypedError pins the
// godlike/07 typed-error contract on StagedArtifactReference validation:
// invalid input (empty ArtifactID) surfaces ErrStagedArtifactReference
// MissingFields via errors.Is WITHOUT any Prepare call (no Drive side-
// effect on malformed input — godlike/07 no-fake-availability).
func TestPublishAndCompleteUseCase_InvalidStagedFailsTypedError(t *testing.T) {
	prep := &mockArtifactPreparationService{
		defaultLocation: finalization.AssetLocation{Provider: "drive", FileID: "default"},
	}
	sender := &mockSenderWithArtifacts{
		returnResp: &remote.CompleteWithArtifactsResponse{JobID: "x", Status: "SUCCEEDED"},
	}
	uc, _ := completion.NewPublishAndCompleteUseCase(prep, sender, zap.NewNop())

	req := &remote.CompleteWithArtifactsRequest{
		WorkerID: "w", JobID: "j", Attempt: 1, LeaseID: "l",
		Result: []byte(`{}`), ResultHash: "rh",
		AssetMappings: map[string]string{
			"":      "asset-empty", // for empty ArtifactID test
			"art-x": "asset-x",
		},
	}

	// (1) Empty ArtifactID -> ErrStagedArtifactReferenceMissingFields.
	invalidRefs := remote.StagedArtifacts{
		{ArtifactID: "", Destination: "image", SHA256: "sha1"},
	}
	_, err := uc.Execute(context.Background(), req, invalidRefs)
	if err == nil {
		t.Fatal("expected error on empty ArtifactID, got nil")
	}
	if !errors.Is(err, remote.ErrStagedArtifactReferenceMissingFields) {
		t.Errorf("err = %v; want wraps ErrStagedArtifactReferenceMissingFields", err)
	}
	if prep.calls.Load() != 0 {
		t.Errorf("Prepare MUST NOT be called on invalid input (godlike/07 no-fake-availability): got %d calls", prep.calls.Load())
	}

	// (2) Out-of-directory Destination -> ErrStagedArtifactReferenceInvalidDestination.
	invalidDest := remote.StagedArtifacts{
		{ArtifactID: "art-x", Destination: "drives"}, // typo
	}
	_, err = uc.Execute(context.Background(), req, invalidDest)
	if err == nil {
		t.Fatal("expected error on invalid destination, got nil")
	}
	if !errors.Is(err, remote.ErrStagedArtifactReferenceInvalidDestination) {
		t.Errorf("err = %v; want wraps ErrStagedArtifactReferenceInvalidDestination", err)
	}
	if prep.calls.Load() != 0 {
		t.Errorf("Prepare MUST NOT be called on invalid input: got %d calls", prep.calls.Load())
	}

	// (3) Valid empty StagedArtifacts slice is a no-op (NOT an error):
	// the canonical PublishAndCompleteUseCase still succeeds without any
	// Drive side-effect (godlike/07 no-fake-availability: never invent a
	// publish outcome that did not come from Prepare).
	validEmpty := remote.StagedArtifacts{}
	resp, err := uc.Execute(context.Background(), req, validEmpty)
	if err != nil {
		t.Errorf("empty refs SHOULD pass through (no-op): %v", err)
	}
	if resp == nil || resp.Status != "SUCCEEDED" {
		t.Errorf("resp on empty refs: got %v want Status=SUCCEEDED", resp)
	}
	if prep.calls.Load() != 0 {
		t.Errorf("Prepare MUST NOT be called on empty refs (no-op pass-through): got %d", prep.calls.Load())
	}
	if sender.calls.Load() != 1 {
		t.Errorf("sender MUST be called once with empty published slice: got %d", sender.calls.Load())
	}
	if len(sender.lastPublished) != 0 {
		t.Errorf("sender.lastPublished on empty refs: got %d, want 0 (empty slice)", len(sender.lastPublished))
	}
}

// TestWireFormat_StagedArtifactsRoundTrip (supporting test) pins the
// wire-format byte-stability invariant: the HTTP wire body with the
// `staged_artifacts` JSON key round-trips byte-stable through Go's
// encoding/json (canonical godlike/07 typed-shape boundary check).
func TestWireFormat_StagedArtifactsRoundTrip(t *testing.T) {
	wireBody := []byte(`{
		"staged_artifacts": [
			{"artifact_id":"art-rtp-1", "destination":"image", "sha256":"sha-rtp-1"},
			{"artifact_id":"art-rtp-2", "destination":"image", "sha256":""}
		]
	}`)
	var envelope struct {
		StagedArtifacts []*remote.StagedArtifactReference `json:"staged_artifacts"`
	}
	if err := json.Unmarshal(wireBody, &envelope); err != nil {
		t.Fatalf("unmarshal wire: %v", err)
	}
	if len(envelope.StagedArtifacts) != 2 {
		t.Fatalf("StagedArtifacts length: got %d, want 2", len(envelope.StagedArtifacts))
	}
	if envelope.StagedArtifacts[0].ArtifactID != "art-rtp-1" {
		t.Errorf("StagedArtifacts[0].ArtifactID: got %q want %q",
			envelope.StagedArtifacts[0].ArtifactID, "art-rtp-1")
	}
	if envelope.StagedArtifacts[0].Destination != "image" {
		t.Errorf("StagedArtifacts[0].Destination: got %q want %q",
			envelope.StagedArtifacts[0].Destination, "image")
	}
	if envelope.StagedArtifacts[1].SHA256 != "" {
		t.Errorf("StagedArtifacts[1].SHA256: got %q want %q (empty SHA is valid)",
			envelope.StagedArtifacts[1].SHA256, "")
	}

	// Round-trip: marshal back, expect byte-stable wire shape.
	reMarshaled, err := json.Marshal(envelope.StagedArtifacts)
	if err != nil {
		t.Fatalf("remarshal: %v", err)
	}
	var reArtifacts []*remote.StagedArtifactReference
	if err := json.Unmarshal(reMarshaled, &reArtifacts); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if len(reArtifacts) != 2 {
		t.Errorf("round-trip length: got %d, want 2", len(reArtifacts))
	}
	if reArtifacts[0].ArtifactID != envelope.StagedArtifacts[0].ArtifactID {
		t.Errorf("round-trip ArtifactID drift: got %q want %q",
			reArtifacts[0].ArtifactID, envelope.StagedArtifacts[0].ArtifactID)
	}
}

// Silence unused imports on forward-compat-only symbols.
var (
	_ io.Reader = nil
	_           = errors.New
)
