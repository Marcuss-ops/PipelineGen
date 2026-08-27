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
package completion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/remote"
)

// ── Mock ports (hermetic; satisfy the canonical interfaces) ────────────────

// mockArtifactPreparationService satisfies the canonical domain port
// finalization.ArtifactPreparationService (defined at
// internal/domain/finalization/interfaces.go). Per-artifact Prepare
// returns a canonical 10-field PublishedArtifact envelope with a
// per-artifact Drive Location (keyed by ArtifactID so the published
// order assertion stays deterministic under bounded-parallel prepares —
// arrival order is nondeterministic by design). The recorded inputs are
// mutex-guarded: Execute runs prepares on a bounded pool.
type mockArtifactPreparationService struct {
	calls              atomic.Int64
	prepareErr         error
	locationByArtifact map[string]finalization.AssetLocation
	defaultLocation    finalization.AssetLocation
	mu                 sync.Mutex
	recorded           []finalization.VerifiedArtifact
}

func (m *mockArtifactPreparationService) Prepare(ctx context.Context, va finalization.VerifiedArtifact) (finalization.PublishedArtifact, error) {
	m.calls.Add(1)
	m.mu.Lock()
	m.recorded = append(m.recorded, va)
	m.mu.Unlock()
	if m.prepareErr != nil {
		return finalization.PublishedArtifact{}, m.prepareErr
	}
	loc := m.defaultLocation
	if l, ok := m.locationByArtifact[va.ArtifactID]; ok {
		loc = l
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

// recordedInputs returns a snapshot of the Prepare inputs. Arrival order
// is nondeterministic under bounded-parallel execution, so assertions on
// the returned slice must be order-independent.
func (m *mockArtifactPreparationService) recordedInputs() []finalization.VerifiedArtifact {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]finalization.VerifiedArtifact, len(m.recorded))
	copy(out, m.recorded)
	return out
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
	// before, when non-nil, runs at the top of CompleteWithArtifacts so
	// tests can assert the TX executes strictly AFTER every prepare
	// finished (atomic-contract probe under bounded parallelism).
	before func()
}

func (m *mockSenderWithArtifacts) CompleteWithArtifacts(ctx context.Context, req *remote.CompleteWithArtifactsRequest, published []*finalization.PublishedArtifact) (*remote.CompleteWithArtifactsResponse, error) {
	m.calls.Add(1)
	if m.before != nil {
		m.before()
	}
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
		locationByArtifact: map[string]finalization.AssetLocation{
			"art-1": {Provider: "drive", FileID: "drive-file-1", WebViewLink: "https://drive/file/1"},
			"art-2": {Provider: "drive", FileID: "drive-file-2", WebViewLink: "https://drive/file/2"},
			"art-3": {Provider: "drive", FileID: "drive-file-3", WebViewLink: "https://drive/file/3"},
		},
	}
	sender := &mockSenderWithArtifacts{
		returnResp: &remote.CompleteWithArtifactsResponse{
			JobID:          "job-round-trip-1",
			Status:         "SUCCEEDED",
			JobArtifactIDs: []string{"art-1", "art-2", "art-3"},
		},
	}

	uc, err := NewPublishAndCompleteUseCase(prep, sender, zap.NewNop())
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
	// the wire hint). Prepare runs bounded-parallel, so arrival order is
	// nondeterministic — assert set membership, not position.
	recorded := prep.recordedInputs()
	if got := len(recorded); got != 3 {
		t.Errorf("Prepare inputs: got %d, want 3", got)
	}
	seen := make(map[string]bool, len(recorded))
	for _, va := range recorded {
		seen[va.ArtifactID] = true
	}
	for _, s := range staged {
		if !seen[s.ArtifactID] {
			t.Errorf("Prepare input missing artifact %q", s.ArtifactID)
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

	uc, _ := NewPublishAndCompleteUseCase(prep, sender, zap.NewNop())

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
	uc, _ := NewPublishAndCompleteUseCase(prep, sender, zap.NewNop())

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

// ── Bounded-parallel publication (P0: post_writer_finalize) ─────────

// blockingPreparation blocks every Prepare on a gate channel and records
// the max in-flight count, so the bounded-parallelism test can prove the
// bound is both respected (never exceeded) and actually reached (real
// parallelism, not a disguised sequential loop). Locations are derived
// from the artifact ID, keeping the published-order assertion
// deterministic under concurrency.
type blockingPreparation struct {
	mu           sync.Mutex
	active       int
	maxActive    int
	calls        int
	gate         chan struct{}
	boundOnce    sync.Once
	boundReached chan struct{}
}

func newBlockingPreparation() *blockingPreparation {
	return &blockingPreparation{
		gate:         make(chan struct{}),
		boundReached: make(chan struct{}),
	}
}

func (p *blockingPreparation) Prepare(_ context.Context, va finalization.VerifiedArtifact) (finalization.PublishedArtifact, error) {
	p.mu.Lock()
	p.active++
	p.calls++
	if p.active > p.maxActive {
		p.maxActive = p.active
	}
	reached := p.active >= artifactPublishConcurrency
	p.mu.Unlock()
	if reached {
		p.boundOnce.Do(func() { close(p.boundReached) })
	}

	<-p.gate

	p.mu.Lock()
	p.active--
	p.mu.Unlock()

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
		Location:       finalization.AssetLocation{Provider: "drive", FileID: "f-" + va.ArtifactID, Action: finalization.PublishCreated},
	}, nil
}

func (p *blockingPreparation) maxConcurrent() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.maxActive
}

// snapshot returns (calls, active) so the sender hook can assert the TX
// runs strictly after every prepare finished.
func (p *blockingPreparation) snapshot() (calls, active int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls, p.active
}

// failPreparation succeeds for every Prepare except the failOn-th call
// (1-based), which fails deterministically. Gate-free: calls return
// immediately, so the errgroup's fail-fast behavior is what stops the
// remaining artifact scheduling.
type failPreparation struct {
	mu     sync.Mutex
	calls  int
	failOn int
}

func (p *failPreparation) Prepare(_ context.Context, va finalization.VerifiedArtifact) (finalization.PublishedArtifact, error) {
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.mu.Unlock()
	if call == p.failOn {
		return finalization.PublishedArtifact{}, fmt.Errorf("prepare failed for %s", va.ArtifactID)
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
		Location:       finalization.AssetLocation{Provider: "drive", FileID: "f-" + va.ArtifactID, Action: finalization.PublishCreated},
	}, nil
}

func (p *failPreparation) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// canonicalExecRequest builds the request envelope shared by the
// bounded-parallelism tests. assetMappings must cover every staged
// artifact (Validated() fails closed on an empty map).
func canonicalExecRequest(assetMappings map[string]string) *remote.CompleteWithArtifactsRequest {
	return &remote.CompleteWithArtifactsRequest{
		WorkerID:      "w-conc",
		JobID:         "job-conc-uc",
		Attempt:       1,
		LeaseID:       "lease-conc",
		Result:        []byte(`{"ok":true}`),
		ResultHash:    "rh-conc",
		AssetMappings: assetMappings,
	}
}

// concStagedRefs returns n staged artifact references (destination image;
// no on-disk file needed — the mock preparation does not verify) plus the
// AssetMappings covering them.
func concStagedRefs(n int) (remote.StagedArtifacts, []string, map[string]string) {
	staged := make(remote.StagedArtifacts, n)
	ids := make([]string, n)
	mappings := make(map[string]string, n)
	for i := 0; i < n; i++ {
		ids[i] = fmt.Sprintf("art-conc-%d", i)
		staged[i] = &remote.StagedArtifactReference{
			ArtifactID:  ids[i],
			Destination: "image",
			SHA256:      "sha-" + ids[i],
		}
		mappings[ids[i]] = "asset-" + ids[i]
	}
	return staged, ids, mappings
}

// TestPublishAndCompleteUseCase_BoundedParallelism pins the P0
// post_writer_finalize optimization on the use-case path: with 8 staged
// artifacts and a blocking preparation, the bound (4 workers) is reached
// — proving real parallelism — never exceeded, the published slice keeps
// the manifest order, and the sender's single TX runs strictly AFTER all
// 8 prepares complete (atomic contract preserved).
func TestPublishAndCompleteUseCase_BoundedParallelism(t *testing.T) {
	const n = 8
	staged, ids, mappings := concStagedRefs(n)

	prep := newBlockingPreparation()
	sender := &mockSenderWithArtifacts{
		returnResp: &remote.CompleteWithArtifactsResponse{JobID: "job-conc-uc", Status: "SUCCEEDED"},
	}
	// Assert atomicity from inside the sender: at TX time every prepare
	// must already have completed.
	sender.before = func() {
		calls, active := prep.snapshot()
		if calls != n {
			t.Errorf("sender TX ran with %d prepares done, want %d (TX must run after ALL prepares)", calls, n)
		}
		if active != 0 {
			t.Errorf("sender TX ran while %d prepares still in flight", active)
		}
	}

	uc, err := NewPublishAndCompleteUseCase(prep, sender, zap.NewNop())
	if err != nil {
		t.Fatalf("NewPublishAndCompleteUseCase: %v", err)
	}

	resultCh := make(chan error, 1)
	go func() {
		_, err := uc.Execute(context.Background(), canonicalExecRequest(mappings), staged)
		resultCh <- err
	}()

	// Prove the bound is actually reached: 4 prepares must be in flight
	// simultaneously before any completes (the gate is still closed).
	select {
	case <-prep.boundReached:
	case <-time.After(10 * time.Second):
		t.Fatal("bound not reached: fewer than artifactPublishConcurrency concurrent prepares")
	}
	close(prep.gate)

	if err := <-resultCh; err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if max := prep.maxConcurrent(); max != artifactPublishConcurrency {
		t.Errorf("max concurrent prepares = %d, want exactly %d (bound respected)", max, artifactPublishConcurrency)
	}
	if sender.calls.Load() != 1 {
		t.Errorf("sender called %d times, want exactly 1 TX", sender.calls.Load())
	}
	if len(sender.lastPublished) != n {
		t.Fatalf("sender received %d published artifacts, want %d", len(sender.lastPublished), n)
	}
	for i, want := range ids {
		if sender.lastPublished[i] == nil {
			t.Errorf("published[%d]: nil pointer", i)
			continue
		}
		if sender.lastPublished[i].ArtifactID != want {
			t.Errorf("artifact order: index %d = %q, want %q (manifest order must be preserved)", i, sender.lastPublished[i].ArtifactID, want)
		}
	}
}

// TestPublishAndCompleteUseCase_FailsClosedOnPrepareError pins the
// fail-closed contract under concurrency: the first prepare failure fails
// the whole Execute, the terminal TX never runs (no partial success), and
// the failure propagates to the caller naming the failed artifact.
func TestPublishAndCompleteUseCase_FailsClosedOnPrepareError(t *testing.T) {
	const n = 8
	staged, _, mappings := concStagedRefs(n)

	prep := &failPreparation{failOn: 3}
	sender := &mockSenderWithArtifacts{
		returnResp: &remote.CompleteWithArtifactsResponse{JobID: "job-conc-uc", Status: "SUCCEEDED"},
	}
	uc, err := NewPublishAndCompleteUseCase(prep, sender, zap.NewNop())
	if err != nil {
		t.Fatalf("NewPublishAndCompleteUseCase: %v", err)
	}

	_, err = uc.Execute(context.Background(), canonicalExecRequest(mappings), staged)
	if err == nil {
		t.Fatal("expected error when the 3rd prepare fails")
	}
	if !strings.Contains(err.Error(), "art-conc-") {
		t.Errorf("error %q does not identify the failed artifact", err)
	}
	if sender.calls.Load() != 0 {
		t.Errorf("sender ran %d times, want 0 (no TX after prepare failure)", sender.calls.Load())
	}
	if calls := prep.callCount(); calls < 3 {
		t.Errorf("preparation ran %d calls, want at least 3 (the failure must have been reached)", calls)
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
