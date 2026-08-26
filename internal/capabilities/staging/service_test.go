// Package staging — service_test.go (FASE 3 / Push 3.1b, July 2026).
//
// Hermetic tests for StoreService. Uses t.TempDir() for the
// workspace (auto-cleanup via t.Cleanup) + a fakeRepository
// implementing artifact.ArtifactStageRepository in-memory (no SQLite
// dependency for the application-layer test).
//
// Coverage (~7 cases):
//   - Happy path: valid StageRequest → file matches content,
//     hash is computed, Repository.Insert called, receipt
//     round-trips Validated()
//   - 0-byte reader: ErrSourceEmpty + no file on disk
//   - Reader error mid-stream: ErrSourceRead + file removed
//     (no orphan)
//   - Validate fail (empty JobID): ErrInvalidRequest + no
//     file created
//   - Repository.Insert fail: typed error + file removed
//     (no orphan-on-error)
//   - Path-traversal JobID: ErrPathInvalid (safe-path gate)
//   - Deterministic ID generation: stub idGen returns a fixed
//     string; receipt.ID matches
package staging

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	artifact "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// ── fakeRepository — in-memory implementation of artifact.ArtifactStageRepository ─

type fakeRepository struct {
	mu        sync.Mutex
	rows      map[string]*artifact.ArtifactStage
	events    []capturedEvent // append-only log of InsertWithOutbox events
	insertErr error           // optional injected error
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{rows: map[string]*artifact.ArtifactStage{}}
}

func (f *fakeRepository) Insert(_ context.Context, stage *artifact.ArtifactStage) error {
	if f.insertErr != nil {
		return f.insertErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, exists := f.rows[stage.ID]; exists {
		return artifact.ErrArtifactStageIDCollision
	}
	// Copy so callers cannot mutate after Insert.
	cp := *stage
	f.rows[stage.ID] = &cp
	return nil
}

// Push 3.1c: the TX-aware Stage commit goes through
// InsertWithOutbox instead of Insert. The stub records the
// emitted event_type + payload + canonical event_key so
// service_test.go can assert the artifact.staged.v1 emission
// without spinning up SQLite.
func (f *fakeRepository) InsertWithOutbox(_ context.Context, stage *artifact.ArtifactStage, eventType string, payload []byte) (string, error) {
	if f.insertErr != nil {
		return "", f.insertErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, exists := f.rows[stage.ID]; exists {
		return "", artifact.ErrArtifactStageIDCollision
	}
	cp := *stage
	f.rows[stage.ID] = &cp
	f.events = append(f.events, capturedEvent{
		EventType: eventType,
		EventKey:  fmt.Sprintf("stage:%s:%s", stage.JobID, stage.ID),
		Payload:   append([]byte(nil), payload...),
	})
	return fmt.Sprintf("stage:%s:%s", stage.JobID, stage.ID), nil
}

// capturedEvent is the per-call InsertWithOutbox record kept
// by fakeRepository for test assertions on emitted outbox
// events (event_type, payload, canonical event_key). Concurrency-
// safe via fakeRepository.mu.
type capturedEvent struct {
	EventType string
	EventKey  string
	Payload   []byte
}

func (f *fakeRepository) GetByID(_ context.Context, id string) (*artifact.ArtifactStage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.rows[id]
	if !ok {
		return nil, artifact.WrapArtifactStageNotFound(id)
	}
	cp := *row
	return &cp, nil
}

func (f *fakeRepository) ListByJob(_ context.Context, jobID string) ([]artifact.ArtifactStage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []artifact.ArtifactStage
	for _, r := range f.rows {
		if r.JobID == jobID {
			out = append(out, *r)
		}
	}
	return out, nil
}

func (f *fakeRepository) ListByState(_ context.Context, state artifact.ArtifactStageState, limit int) ([]artifact.ArtifactStage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []artifact.ArtifactStage
	for _, r := range f.rows {
		if r.State == state {
			out = append(out, *r)
		}
	}
	return out, nil
}

func (f *fakeRepository) MarkPublished(_ context.Context, id, publishedLocation string, publishedAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.rows[id]
	if !ok {
		return artifact.WrapArtifactStageNotFound(id)
	}
	row.State = artifact.ArtifactStageStatePublished
	row.PublishedLocation = publishedLocation
	row.PublishedAt = &publishedAt
	return nil
}

func (f *fakeRepository) MarkSucceeded(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.rows[id]
	if !ok {
		return artifact.WrapArtifactStageNotFound(id)
	}
	row.State = artifact.ArtifactStageStateSucceeded
	return nil
}

func (f *fakeRepository) MarkFailedPermanent(_ context.Context, id, lastError string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.rows[id]
	if !ok {
		return artifact.WrapArtifactStageNotFound(id)
	}
	row.State = artifact.ArtifactStageStateFailedPermanent
	row.LastError = lastError
	return nil
}

func (f *fakeRepository) IncrementAttemptCount(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.rows[id]
	if !ok {
		return artifact.WrapArtifactStageNotFound(id)
	}
	row.AttemptCount++
	return nil
}

// Compile-time anchor.
var _ artifact.ArtifactStageRepository = (*fakeRepository)(nil)

// ── Test helpers ──────────────────────────────────────────────────────

// newTestService returns a StoreService with a deterministic
// idGen + clock + t.TempDir() workspace.
func newTestService(t *testing.T) (*StoreService, *fakeRepository) {
	t.Helper()
	repo := newFakeRepository()
	ws := t.TempDir()
	svc, err := NewStoreService(repo, ws)
	if err != nil {
		t.Fatalf("NewStoreService: %v", err)
	}
	// Deterministic idGen: "art-test-N".
	var counter int
	svc.idGen = func() string {
		counter++
		return "art-test-" + string(rune('0'+counter))
	}
	// Fixed clock for CreatedAt assertions.
	base := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	svc.clock = func() time.Time { return base }
	return svc, repo
}

// validRequest returns a canonical StageRequest with the
// given content (string for tests; bytes.NewReader wraps it).
func validRequest(content string) StageRequest {
	return StageRequest{
		Content:     strings.NewReader(content),
		JobID:       "job-test-1",
		Mime:        "audio/mpeg",
		Requirement: artifact.RequirementRequired,
		Destination: "drive:voiceover/test",
	}
}

// ── Test 1: Happy path ───────────────────────────────────────────────

func TestStoreService_Stage_HappyPath(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := context.Background()
	payload := "hello-fase3-staging"
	req := validRequest(payload)
	receipt, err := svc.Stage(ctx, req)
	if err != nil {
		t.Fatalf("Stage: unexpected error: %v", err)
	}
	if err := receipt.Validated(); err != nil {
		t.Errorf("receipt.Validated() = %v, want nil", err)
	}
	// ID: deterministic counter from the stub idGen.
	if receipt.ID != "art-test-1" {
		t.Errorf("ID = %q, want art-test-1", receipt.ID)
	}
	// Size matches payload.
	if int(receipt.Size) != len(payload) {
		t.Errorf("Size = %d, want %d", receipt.Size, len(payload))
	}
	// Hash: SHA-256 of payload (lower hex).
	wantHash := sha256Hex(payload)
	if receipt.Hash != wantHash {
		t.Errorf("Hash = %q, want %q", receipt.Hash, wantHash)
	}
	// LocalPath: {workspace}/{jobID}/{stageID}.
	expectedPath := filepath.Join(svc.workspaceDir, "job-test-1", "art-test-1")
	if receipt.LocalPath != expectedPath {
		t.Errorf("LocalPath = %q, want %q", receipt.LocalPath, expectedPath)
	}
	// File content on disk matches the payload.
	gotBytes, err := os.ReadFile(receipt.LocalPath)
	if err != nil {
		t.Fatalf("read staged file: %v", err)
	}
	if string(gotBytes) != payload {
		t.Errorf("staged file content = %q, want %q", string(gotBytes), payload)
	}
	// Repository.Insert was called: row exists with State=STAGED.
	stored, err := repo.GetByID(ctx, receipt.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if stored.State != artifact.ArtifactStageStateStaged {
		t.Errorf("stored.State = %q, want STAGED", stored.State)
	}
	if stored.Requirement != artifact.RequirementRequired {
		t.Errorf("stored.Requirement = %q, want required", stored.Requirement)
	}
	if stored.Destination != req.Destination {
		t.Errorf("stored.Destination = %q, want %q", stored.Destination, req.Destination)
	}
}

// ── Test 2: 0-byte reader → ErrSourceEmpty + no file on disk ─────

func TestStoreService_Stage_RejectsEmptySource(t *testing.T) {
	svc, repo := newTestService(t)
	repo.mu.Lock()
	_ = repo // silence unused warning
	repo.mu.Unlock()
	ctx := context.Background()
	req := validRequest("")
	_, err := svc.Stage(ctx, req)
	if !errors.Is(err, ErrSourceEmpty) {
		t.Errorf("Stage empty source: err = %v, want ErrSourceEmpty", err)
	}
	// JobID sub-dir may have been created (the MkdirAll is part
	// of the pipeline); assert it is empty.
	jobDir := filepath.Join(svc.workspaceDir, "job-test-1")
	entries, _ := os.ReadDir(jobDir)
	if len(entries) != 0 {
		t.Errorf("expected empty jobDir after 0-byte rejection; got %d entries", len(entries))
	}
}

// ── Test 3: Reader error mid-stream → ErrSourceRead + file removed ─

type errReader struct {
	data      []byte
	pos       int
	failAfter int
}

func (r *errReader) Read(p []byte) (int, error) {
	if r.pos >= r.failAfter {
		return 0, io.ErrUnexpectedEOF
	}
	// Bytes left to read BEFORE we trip the injected failure.
	// The test contract guarantees failAfter <= len(r.data) so
	// the r.pos+remaining slice stays within r.data after the
	// cap on len(p) below.
	remaining := r.failAfter - r.pos
	// Cap at len(p): never extend past the caller's buffer.
	if remaining > len(p) {
		remaining = len(p)
	}
	n := copy(p, r.data[r.pos:r.pos+remaining])
	r.pos += n
	return n, nil
}

func TestStoreService_Stage_SourceReadError_RemovesFile(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	payload := []byte("partial-content-that-will-fail")
	req := StageRequest{
		Content:     &errReader{data: payload, failAfter: 5},
		JobID:       "job-test-1",
		Mime:        "audio/mpeg",
		Requirement: artifact.RequirementRequired,
		Destination: "drive:voiceover/test",
	}
	_, err := svc.Stage(ctx, req)
	if !errors.Is(err, ErrSourceRead) {
		t.Errorf("Stage reader error: err = %v, want ErrSourceRead", err)
	}
	// File MUST be removed (no orphan on error).
	jobDir := filepath.Join(svc.workspaceDir, "job-test-1")
	entries, _ := os.ReadDir(jobDir)
	if len(entries) != 0 {
		t.Errorf("expected empty jobDir after reader error; got %d entries (orphan file leaked)", len(entries))
	}
}

// ── Test 4: Validate fail (empty JobID) → ErrInvalidRequest ────────

func TestStoreService_Stage_RejectsInvalidRequest_EmptyJobID(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	req := validRequest("payload")
	req.JobID = "" // invalid
	_, err := svc.Stage(ctx, req)
	if !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("Stage empty JobID: err = %v, want ErrInvalidRequest", err)
	}
	// No file should have been created (validate gate runs first).
	entries, _ := os.ReadDir(svc.workspaceDir)
	if len(entries) != 0 {
		t.Errorf("expected empty workspace after validate-reject; got %d entries", len(entries))
	}
}

func TestStoreService_Stage_RejectsInvalidRequest_NilContent(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	req := validRequest("payload")
	req.Content = nil // invalid
	_, err := svc.Stage(ctx, req)
	if !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("Stage nil Content: err = %v, want ErrInvalidRequest", err)
	}
}

func TestStoreService_Stage_RejectsInvalidRequest_BadMime(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	req := validRequest("payload")
	req.Mime = "not-a-valid-mime" // fails the IANA regex
	_, err := svc.Stage(ctx, req)
	if !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("Stage bad Mime: err = %v, want ErrInvalidRequest", err)
	}
}

// ── Test 5: Repository.Insert fail → file removed (no orphan) ─────

func TestStoreService_Stage_RepoInsertFail_RemovesFile(t *testing.T) {
	svc, repo := newTestService(t)
	// Inject a Repository failure (simulate disk-full / locked row / etc.).
	repo.insertErr = errors.New("simulated repo failure")
	ctx := context.Background()
	payload := "payload"
	req := validRequest(payload)
	_, err := svc.Stage(ctx, req)
	if err == nil {
		t.Fatalf("Stage with injected repo error: expected non-nil error")
	}
	// File MUST be removed (no orphan-on-error).
	jobDir := filepath.Join(svc.workspaceDir, "job-test-1")
	entries, _ := os.ReadDir(jobDir)
	if len(entries) != 0 {
		t.Errorf("expected empty jobDir after repo-fail; got %d entries (orphan file leaked)", len(entries))
	}
}

// ── Test 6: Path-traversal JobID → ErrPathInvalid ──────────────────

func TestStoreService_Stage_RejectsPathTraversal(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	payload := "payload"
	req := validRequest(payload)
	// JobID with "../escape" — the safe-path gate MUST reject.
	// (Note: filepath.Clean normalises "../escape" to "../escape",
	// so isSafeSubpath detects the parent-escape and returns false.)
	req.JobID = "../escape"
	_, err := svc.Stage(ctx, req)
	if !errors.Is(err, ErrPathInvalid) {
		t.Errorf("Stage path-traversal JobID: err = %v, want ErrPathInvalid", err)
	}
}

// ── Test 7: Deterministic ID generation ─────────────────────────────

func TestStoreService_Stage_DeterministicID(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	r1, err := svc.Stage(ctx, validRequest("payload-1"))
	if err != nil {
		t.Fatalf("Stage 1: %v", err)
	}
	r2, err := svc.Stage(ctx, validRequest("payload-2"))
	if err != nil {
		t.Fatalf("Stage 2: %v", err)
	}
	// The stub idGen returns "art-test-1" + "art-test-2".
	if r1.ID != "art-test-1" {
		t.Errorf("receipt 1 ID = %q, want art-test-1", r1.ID)
	}
	if r2.ID != "art-test-2" {
		t.Errorf("receipt 2 ID = %q, want art-test-2", r2.ID)
	}
	// Receipts differ (different content → different hash).
	if r1.Hash == r2.Hash {
		t.Errorf("receipts share hash %q (collision? different content)", r1.Hash)
	}
}

// ── Test 8: ErrIDGenerator when idGen returns empty ───────────────

func TestStoreService_Stage_RejectsEmptyIDGenerator(t *testing.T) {
	svc, _ := newTestService(t)
	// Override idGen to return empty.
	svc.idGen = func() string { return "" }
	ctx := context.Background()
	_, err := svc.Stage(ctx, validRequest("payload"))
	if !errors.Is(err, ErrIDGenerator) {
		t.Errorf("Stage empty idGen: err = %v, want ErrIDGenerator", err)
	}
}

// ── Helper: SHA-256 hex of a string (for receipt.Hash assertion) ──

// sha256Hex is a tiny stdlib-only helper to compute the
// expected hash for the happy-path test.
func sha256Hex(s string) string {
	h := sha256.New()
	_, _ = h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
}
