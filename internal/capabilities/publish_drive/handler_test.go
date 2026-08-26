// Package publish_drive_test — handler_test.go (FASE 3 / Push 3.1e).
//
// Hermetic tests for the artifact.staged.v1 consumer. Uses
// stubRepository (in-memory Repository with MarkPublished
// capture) + stubPublisher (canned PublishResult + canned
// error) + zaptest/observer for structured-log assertions.
// Both stubs satisfy the typed-port contracts from godlike/06
// SSOT (no infrastructure concrete leaked into the test path).
//
// Coverage (~8 cases):
//   - Fail-fast: NewHandler rejects (nil repo), (nil publisher),
//     (nil log).
//   - Routing: EventType + IdempotencyKey pinned to staged.v1
//     (no drift on rename).
//   - Happy path: payload decode → Publisher.Publish called
//     once with decoded PublishRequest fields → MarkPublished
//     called once with canonical JSON PublishedLocation →
//     return nil + canonical zap fields.
//   - Re-delivery: MarkPublished returns ErrTerminalStateRejection
//     → handler returns nil (the row is already PUBLISHED —
//     the desired end-state, NOT a retry-triggering error).
//   - Failure paths:
//   - malformed JSON → ErrInvalidPayload (no Publish or
//     MarkPublished call)
//   - empty StageID → ErrEmptyStageID
//   - Destination without `drive:` prefix → ErrDestinationFormat
//   - Publisher.Publish returning error → wrapped upstream
//     error (MarkPublished NOT called)
//   - MarkPublished returning non-TerminalStateRejection error
//     → wrapped upstream error
package publish_drive

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	artifact "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// ── Mocks ──────────────────────────────────────────────────────────────

// stubRepository is an in-memory artifact.Repository that
// captures MarkPublished calls + returns a canned error
// (default nil). Implements the full Repository interface so
// the Handler port resolves to a static implementation (compile-
// time conformance anchor at the bottom of the file).
//
// All 9 methods (post-Push 3.1c with InsertWithOutbox) are
// present so the stub satisfies the typed port without panic.
type stubRepository struct {
	mu        sync.Mutex
	markErr   error               // optional injected error
	markCalls []markPublishedCall // append-only log of MarkPublished
}

type markPublishedCall struct {
	StageID      string
	LocationJSON string
	PublishedAt  time.Time
}

func (s *stubRepository) Insert(_ context.Context, _ *detail.ArtifactStage) error {
	return nil
}
func (s *stubRepository) InsertWithOutbox(_ context.Context, _ *detail.ArtifactStage, _ string, _ []byte) (string, error) {
	return "", nil
}
func (s *stubRepository) GetByID(_ context.Context, _ string) (*detail.ArtifactStage, error) {
	return nil, detail.WrapArtifactStageNotFound("not-implemented-stub")
}
func (s *stubRepository) ListByJob(_ context.Context, _ string) ([]detail.ArtifactStage, error) {
	return nil, nil
}
func (s *stubRepository) ListByState(_ context.Context, _ detail.ArtifactStageState, _ int) ([]detail.ArtifactStage, error) {
	return nil, nil
}
func (s *stubRepository) MarkPublished(_ context.Context, id, locationJSON string, publishedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.markCalls = append(s.markCalls, markPublishedCall{
		StageID:      id,
		LocationJSON: locationJSON,
		PublishedAt:  publishedAt,
	})
	return s.markErr
}
func (s *stubRepository) MarkSucceeded(_ context.Context, _ string) error {
	return nil
}
func (s *stubRepository) MarkFailedPermanent(_ context.Context, _, _ string) error {
	return nil
}
func (s *stubRepository) IncrementAttemptCount(_ context.Context, _ string) error {
	return nil
}

// Compile-time conformance anchor.
var _ detail.ArtifactStageRepository = (*stubRepository)(nil)

// stubPublisher captures Publish calls + returns a canned
// PublishResult (default: drive-published) + a canned error
// (default nil). Implements delivery.Publisher (interface
// includes ResolveFolder; the handler doesn't call it so the
// stub returns zero values).
type stubPublisher struct {
	mu        sync.Mutex
	pubErr    error
	pubResult *delivery.PublishResult
	pubCalls  []delivery.PublishRequest
}

func (s *stubPublisher) Publish(_ context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pubCalls = append(s.pubCalls, req)
	return s.pubResult, s.pubErr
}

func (s *stubPublisher) ResolveFolder(_ context.Context, _ delivery.PublishRequest) (string, error) {
	return "", nil
}

// Compile-time conformance anchor.
var _ delivery.Publisher = (*stubPublisher)(nil)

// ── Test helpers ───────────────────────────────────────────────────────

func validPayload() TypedStageEventPayload {
	return TypedStageEventPayload{
		StageID:     "art-1234567890-abcd",
		JobID:       "job-x",
		LocalPath:   "/var/lib/pipelinegen/staging/job-x/art-1234567890-abcd",
		Hash:        "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Size:        4096,
		Mime:        "audio/mpeg",
		Requirement: "required",
		Destination: "drive:voiceover/test",
		EmittedAt:   time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
	}
}

func makeEvent(payload TypedStageEventPayload) outboxevents.Event {
	body, _ := json.Marshal(payload)
	return outboxevents.Event{
		EventType:   EventTypeArtifactStaged,
		PayloadJSON: string(body),
		EventKey:    "stage:job-x:art-1234567890-abcd",
	}
}

// newHandler constructs a Handler backed by a stubRepository +
// stubPublisher + observed zap.Logger. Tests use observerLogs to
// assert on emitted structured-log fields.
func newHandler(t *testing.T) (*Handler, *stubRepository, *stubPublisher, *observer.ObservedLogs) {
	t.Helper()
	repo := &stubRepository{}
	pub := &stubPublisher{
		pubResult: &delivery.PublishResult{
			FileID:       "drive-file-1",
			FolderID:     "drive-folder-1",
			Destination:  delivery.DestinationKey("voiceover"),
			Action:       delivery.PublishActionCreated,
			PathSegments: []string{"voiceover", "test"},
		},
	}
	core, logs := observer.New(zapcore.InfoLevel)
	log := zap.New(core)
	h, err := NewHandler(repo, pub, log)
	if err != nil {
		t.Fatalf("NewHandler (test helper): %v", err)
	}
	return h, repo, pub, logs
}

// ── Test 1: NewHandler fail-fast on nil repo ───────────────────────────

func TestNewHandler_RejectsNilRepo(t *testing.T) {
	pub := &stubPublisher{}
	core, _ := observer.New(zapcore.InfoLevel)
	_, err := NewHandler(nil, pub, zap.New(core))
	if err == nil {
		t.Fatalf("NewHandler(nil repo): expected error, got nil")
	}
}

// ── Test 2: NewHandler fail-fast on nil publisher ─────────────────────

func TestNewHandler_RejectsNilPublisher(t *testing.T) {
	repo := &stubRepository{}
	core, _ := observer.New(zapcore.InfoLevel)
	_, err := NewHandler(repo, nil, zap.New(core))
	if err == nil {
		t.Fatalf("NewHandler(nil publisher): expected error, got nil")
	}
}

// ── Test 3: NewHandler fail-fast on nil log ───────────────────────────

func TestNewHandler_RejectsNilLog(t *testing.T) {
	repo := &stubRepository{}
	pub := &stubPublisher{}
	_, err := NewHandler(repo, pub, nil)
	if err == nil {
		t.Fatalf("NewHandler(nil log): expected error, got nil")
	}
}

// ── Test 4: EventType + IdempotencyKey stable ──────────────────────────

func TestHandler_EventTypeAndIdempotencyKeyStable(t *testing.T) {
	h, _, _, _ := newHandler(t)
	if got, want := h.EventType(), EventTypeArtifactStaged; got != want {
		t.Errorf("EventType() = %q, want %q", got, want)
	}
	if got, want := h.IdempotencyKey(), EventTypeArtifactStaged; got != want {
		t.Errorf("IdempotencyKey() = %q, want %q", got, want)
	}
}

// ── Test 5: Happy path (decode → Publish → MarkPublished → nil) ──────

func TestHandler_Handle_HappyPath(t *testing.T) {
	h, repo, pub, logs := newHandler(t)
	payload := validPayload()
	beforeTime := time.Now().UTC()

	if err := h.Handle(context.Background(), makeEvent(payload)); err != nil {
		t.Fatalf("Handle happy: unexpected error: %v", err)
	}
	afterTime := time.Now().UTC()

	// 1. Publisher.Publish called exactly once with decoded fields.
	if got := len(pub.pubCalls); got != 1 {
		t.Fatalf("Publish call count = %d, want 1", got)
	}
	got := pub.pubCalls[0]
	if got.Destination != delivery.DestinationKey("voiceover") {
		t.Errorf("PublishRequest.Destination = %q, want %q (after parsing 'drive:voiceover/test')", got.Destination, "voiceover")
	}
	if got.Subject != "test" {
		t.Errorf("PublishRequest.Subject = %q, want %q (after parsing 'drive:voiceover/test')", got.Subject, "test")
	}
	if got.LocalPath != payload.LocalPath {
		t.Errorf("PublishRequest.LocalPath = %q, want %q", got.LocalPath, payload.LocalPath)
	}
	if got.Filename != payload.StageID {
		t.Errorf("PublishRequest.Filename = %q, want %q (StageID verbatim)", got.Filename, payload.StageID)
	}
	if got.ContentHash != payload.Hash {
		t.Errorf("PublishRequest.ContentHash = %q, want %q", got.ContentHash, payload.Hash)
	}
	if got.AssetID != payload.StageID {
		t.Errorf("PublishRequest.AssetID = %q, want %q", got.AssetID, payload.StageID)
	}
	if got.ProjectID != payload.JobID {
		t.Errorf("PublishRequest.ProjectID = %q, want %q", got.ProjectID, payload.JobID)
	}
	if got.Group != payload.JobID {
		t.Errorf("PublishRequest.Group = %q, want %q", got.Group, payload.JobID)
	}
	// Idempotency key: deterministic from staging payload fields.
	wantIdempKey := delivery.DeriveIdempotencyKey(
		delivery.DestinationKey("voiceover"),
		payload.StageID,
		payload.Hash,
		1,
	)
	if got.IdempotencyKey != wantIdempKey {
		t.Errorf("PublishRequest.IdempotencyKey = %q, want %q", got.IdempotencyKey, wantIdempKey)
	}
	if got.SourceVersion != 1 {
		t.Errorf("PublishRequest.SourceVersion = %d, want 1", got.SourceVersion)
	}

	// 2. Repository.MarkPublished called exactly once with
	//    canonical JSON PublishedLocation.
	if num := len(repo.markCalls); num != 1 {
		t.Fatalf("MarkPublished call count = %d, want 1", num)
	}
	mc := repo.markCalls[0]
	if mc.StageID != payload.StageID {
		t.Errorf("MarkPublished.StageID = %q, want %q", mc.StageID, payload.StageID)
	}
	// PublishedAt must be a non-zero UTC stamp sandwiched
	// between beforeTime and afterTime — the Handler uses its
	// own internal clock (forward-pointer: a testable clock
	// seam would let us pin a fixed time; current tests assert
	// the wall-clock contract instead).
	if mc.PublishedAt.IsZero() {
		t.Errorf("MarkPublished.PublishedAt is the zero time; want a real UTC stamp")
	}
	if mc.PublishedAt.Before(beforeTime) || mc.PublishedAt.After(afterTime) {
		t.Errorf("MarkPublished.PublishedAt = %v, want within [%v, %v] (wall-clock contract)", mc.PublishedAt, beforeTime, afterTime)
	}
	// Decode the JSON PublishedLocation and assert canonical shape.
	var gotLoc artifact.PublishedLocation
	if err := json.Unmarshal([]byte(mc.LocationJSON), &gotLoc); err != nil {
		t.Fatalf("MarkPublished.LocationJSON is not valid JSON: %v (raw=%q)", err, mc.LocationJSON)
	}
	if gotLoc.ArtifactID != payload.StageID {
		t.Errorf("PublishedLocation.ArtifactID = %q, want %q", gotLoc.ArtifactID, payload.StageID)
	}
	if gotLoc.Kind != artifact.LocationKindDrive {
		t.Errorf("PublishedLocation.Kind = %q, want %q", gotLoc.Kind, artifact.LocationKindDrive)
	}
	if gotLoc.URI != "drive-file-1" {
		t.Errorf("PublishedLocation.URI = %q, want %q", gotLoc.URI, "drive-file-1")
	}
	if gotLoc.ExternalID != "drive-file-1" {
		t.Errorf("PublishedLocation.ExternalID = %q, want %q", gotLoc.ExternalID, "drive-file-1")
	}

	// 3. Structured log: exactly 1 Info entry with the
	//    canonical "artifact published" message + 9 fields.
	entries := logs.All()
	if num := len(entries); num != 1 {
		t.Fatalf("log entry count = %d, want 1", num)
	}
	if !strings.Contains(entries[0].Message, "artifact published") {
		t.Errorf("log message = %q, want contains %q", entries[0].Message, "artifact published")
	}
	fieldSet := map[string]bool{}
	for _, f := range entries[0].Context {
		fieldSet[f.Key] = true
	}
	for _, want := range []string{
		"event_type", "stage_id", "job_id", "destination",
		"drive_file_id", "drive_folder_id", "idempotency_key",
		"upload_action", "published_at",
	} {
		if !fieldSet[want] {
			t.Errorf("log fields missing canonical key: %q", want)
		}
	}
}

// ── Test 6: Re-delivery (ErrTerminalStateRejection → return nil) ─────

func TestHandler_Handle_TerminalStateRejection_ReturnsNil(t *testing.T) {
	h, repo, _, logs := newHandler(t)
	// Inject: the row is already in PUBLISHED state, so the
	// fenced-CAS rejects with the canonical terminal-state
	// sentinel. The handler MUST return nil to break the
	// outbox redelivery loop.
	repo.markErr = detail.ErrTerminalStateRejection

	if err := h.Handle(context.Background(), makeEvent(validPayload())); err != nil {
		t.Errorf("Handle terminal-state fence: err = %v, want nil (idempotent re-delivery = no-op)", err)
	}

	// Structured log: a separate Info entry confirms the fence observation.
	entries := logs.All()
	hasFenceLog := false
	for _, e := range entries {
		if strings.Contains(e.Message, "terminal-state fence observed") {
			hasFenceLog = true
		}
	}
	if !hasFenceLog {
		t.Errorf("expected log entry containing 'terminal-state fence observed'; got messages=%v", entries)
	}
}

// ── Test 7: malformed JSON → ErrInvalidPayload (no Publish call) ─────

func TestHandler_Handle_RejectsMalformedJSON(t *testing.T) {
	h, repo, pub, _ := newHandler(t)
	err := h.Handle(context.Background(), outboxevents.Event{
		EventType:   EventTypeArtifactStaged,
		PayloadJSON: "[not-json",
	})
	if !errors.Is(err, ErrInvalidPayload) {
		t.Errorf("Handle malformed JSON: err = %v, want ErrInvalidPayload", err)
	}
	if got := len(pub.pubCalls); got != 0 {
		t.Errorf("Publisher.Publish calls = %d, want 0 (validate gate fires before publish)", got)
	}
	if got := len(repo.markCalls); got != 0 {
		t.Errorf("MarkPublished calls = %d, want 0 (no Publish happened)", got)
	}
}

// ── Test 8: empty StageID → ErrEmptyStageID ───────────────────────────

func TestHandler_Handle_RejectsEmptyStageID(t *testing.T) {
	h, _, pub, _ := newHandler(t)
	p := validPayload()
	p.StageID = ""
	if err := h.Handle(context.Background(), makeEvent(p)); !errors.Is(err, ErrEmptyStageID) {
		t.Errorf("Handle empty StageID: err = %v, want ErrEmptyStageID", err)
	}
	if got := len(pub.pubCalls); got != 0 {
		t.Errorf("Publisher.Publish calls = %d, want 0", got)
	}
}

// ── Test 9: Destination without "drive:" prefix → ErrDestinationFormat

func TestHandler_Handle_RejectsDestinationWithoutPrefix(t *testing.T) {
	h, _, pub, _ := newHandler(t)
	p := validPayload()
	p.Destination = "voiceover/test" // missing "drive:" prefix
	err := h.Handle(context.Background(), makeEvent(p))
	if !errors.Is(err, ErrDestinationFormat) {
		t.Errorf("Handle bad destination: err = %v, want ErrDestinationFormat", err)
	}
	if got := len(pub.pubCalls); got != 0 {
		t.Errorf("Publisher.Publish calls = %d, want 0 (validate gate fires before publish)", got)
	}
}

// ── Test 10: Destination with empty key after "drive:" → ErrDestinationFormat

func TestHandler_Handle_RejectsDestinationEmptyKey(t *testing.T) {
	h, _, _, _ := newHandler(t)
	p := validPayload()
	p.Destination = "drive:/test" // empty key after "drive:"
	err := h.Handle(context.Background(), makeEvent(p))
	if !errors.Is(err, ErrDestinationFormat) {
		t.Errorf("Handle empty-key destination: err = %v, want ErrDestinationFormat", err)
	}
}

// ── Test 11: Publisher.Publish failure → wrapped upstream error ──────

func TestHandler_Handle_PublisherError_ReturnsWrappedAndSkipsMark(t *testing.T) {
	h, repo, pub, _ := newHandler(t)
	pubErr := errors.New("simulated upload failure (Drive 503)")
	pub.pubErr = pubErr

	err := h.Handle(context.Background(), makeEvent(validPayload()))
	if err == nil {
		t.Fatalf("Handle publisher error: expected non-nil error")
	}
	if !errors.Is(err, pubErr) {
		t.Errorf("err chain does not include injected sentinel: %v", err)
	}
	if !strings.Contains(err.Error(), "Publisher.Publish") {
		t.Errorf("err = %v, want contains 'Publisher.Publish'", err)
	}
	// MarkPublished MUST NOT be called when the upload failed.
	if got := len(repo.markCalls); got != 0 {
		t.Errorf("MarkPublished calls = %d, want 0 (Publish failed)", got)
	}
}

// ── Test 12: MarkPublished non-terminal error → wrapped upstream err ─

func TestHandler_Handle_MarkPublishedDBError_ReturnsWrapped(t *testing.T) {
	h, repo, _, _ := newHandler(t)
	repo.markErr = errors.New("simulated DB write failure")

	err := h.Handle(context.Background(), makeEvent(validPayload()))
	if err == nil {
		t.Fatalf("Handle MarkPublished error: expected non-nil error")
	}
	if !errors.Is(err, repo.markErr) {
		t.Errorf("err chain does not include injected sentinel: %v", err)
	}
	if !strings.Contains(err.Error(), "MarkPublished") {
		t.Errorf("err = %v, want contains 'MarkPublished'", err)
	}
}

// ── Forward-pointer: add a WithClock option on publish_drive.NewHandler ──
//
// The current Handler's nowFn field is package-private and
// unconfigurable from tests. The happy-path test asserts the
// wall-clock contract (non-zero, within before/after bounds)
// rather than a fixed stamp. When a future test needs a
// deterministic clock for stage-managed telemetry assertions,
// publish_drive.NewHandler should grow a `WithClock(fn)` option
// (or a `WithNowFn` field setter exported only to test builds).
// Today the contract: PublishedAt is non-zero UTC at the call
// instant — verified above.
