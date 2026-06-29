// Package voiceover — handler_test.go (P0.2 wire-shape round-trip tests, June 2026).
//
// Tests verify the canonical P0.2 wire shape (request_id + items[] +
// destination.group + options) round-trips into the canonical
// GenerateVoiceoversCommand without leaking the wire shape into
// the business side. The stub job.Service records the EnqueueRequest
// passed by the handler so each test can assert on:
//   - EnqueueRequest.Type (must equal job.TypeVoiceoverGenerate)
//   - EnqueueRequest.CorrelationID (must equal request_id)
//   - EnqueueRequest.Payload cast to *GenerateVoiceoversCommand
//     and field-by-field field assertions
//   - HTTP response status + body shape
package voiceover

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// stubJobsSvc captures the EnqueueRequest so tests can assert on the
// canonical wire-shape → Command translation. All unused Service methods
// return zero values — only Enqueue is exercised by the handler tests.
type stubJobsSvc struct {
	enqueued  *job.EnqueueRequest
	returnJob *job.Job
	returnErr error
}

func (s *stubJobsSvc) Enqueue(_ context.Context, req *job.EnqueueRequest) (*job.Job, error) {
	s.enqueued = req
	if s.returnJob == nil {
		s.returnJob = &job.Job{ID: "job_default"}
	}
	return s.returnJob, s.returnErr
}
func (s *stubJobsSvc) Get(_ context.Context, _ string) (*job.Job, error) { return nil, nil }
func (s *stubJobsSvc) Cancel(_ context.Context, _ string) error          { return nil }
func (s *stubJobsSvc) List(_ context.Context, _ job.Filter) ([]job.Job, error) {
	return nil, nil
}
func (s *stubJobsSvc) IsTerminal(status job.Status) bool {
	return status == job.StatusSucceeded || status == job.StatusFailed || status == job.StatusCancelled
}
func (s *stubJobsSvc) RegisterHandler(_ string, _ any) error { return nil }
func (s *stubJobsSvc) ListEvents(_ context.Context, _ string) ([]job.Event, error) {
	return nil, nil
}
func (s *stubJobsSvc) Retry(_ context.Context, _ string) (*job.Job, error) { return nil, nil }

// compile-time assertion: stubJobsSvc satisfies job.Service.
var _ job.Service = (*stubJobsSvc)(nil)

// newTestRouter wires the canonical Handler on a gin engine rooted at /generate,
// mirroring the production RegisterRoutes bound under /api/media/voiceover/*.
//
// httptest serves against the engine root, so we mount the handler on the
// engine's root RouterGroup (Engine embeds RouterGroup; addressable as
// &rg.RouterGroup). Tests POST /generate.
func newTestRouter(svc job.Service) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := NewHandler(svc, zap.NewNop())
	rg := gin.New()
	h.RegisterRoutes(&rg.RouterGroup)
	return rg
}

// doRequest performs a POST /generate with the given JSON body and
// returns the recorder so tests can assert status + body.
func doRequest(r *gin.Engine, rawBody string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/generate", bytes.NewReader([]byte(rawBody)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// TestGenerateHandler_HappyPath_P0_2WireShape verifies the canonical
// P0.2 wire shape (request_id + items[] + destination.group + options)
// round-trips into the GenerateVoiceoversCommand with every field
// populated correctly.
func TestGenerateHandler_HappyPath_P0_2WireShape(t *testing.T) {
	jobsSvc := &stubJobsSvc{
		returnJob: &job.Job{ID: "job_abc123", CorrelationID: "video-xyz"},
	}
	r := newTestRouter(jobsSvc)

	body := `{
		"request_id": "video-xyz",
		"items": [
			{"text": "Testo", "language": "it-IT", "voice": "it-IT-DiegoNeural", "filename": "intro-it.mp3"},
			{"text": "Testo", "language": "en-US", "voice": "en-US-RogerNeural", "filename": "intro-en.mp3"}
		],
		"destination": {"kind": "group", "group": "Promozionali"},
		"options": {"remove_silence": true, "strategy": "replace", "parallelism": 2, "metadata": {"trace": "video-xyz", "scene": "intro"}}
	}`

	rec := doRequest(r, body)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status: got %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	if jobsSvc.enqueued == nil {
		t.Fatal("expected Enqueue to be called, got no call")
	}
	if jobsSvc.enqueued.CorrelationID != "video-xyz" {
		t.Errorf("CorrelationID: got %q, want %q", jobsSvc.enqueued.CorrelationID, "video-xyz")
	}
	if jobsSvc.enqueued.Type != job.TypeVoiceoverGenerate {
		t.Errorf("Type: got %q, want %q", jobsSvc.enqueued.Type, job.TypeVoiceoverGenerate)
	}

	cmd, ok := jobsSvc.enqueued.Payload.(*voiceover.GenerateVoiceoversCommand)
	if !ok {
		t.Fatalf("Payload type: got %T, want *voiceover.GenerateVoiceoversCommand", jobsSvc.enqueued.Payload)
	}
	if cmd.Text != "Testo" {
		t.Errorf("Text: got %q, want %q", cmd.Text, "Testo")
	}
	if !reflect.DeepEqual(cmd.Languages, []string{"it-IT", "en-US"}) {
		t.Errorf("Languages: got %v, want %v", cmd.Languages, []string{"it-IT", "en-US"})
	}
	expectedOverrides := map[string]string{
		"it-IT": "it-IT-DiegoNeural",
		"en-US": "en-US-RogerNeural",
	}
	if !reflect.DeepEqual(cmd.VoiceOverrides, expectedOverrides) {
		t.Errorf("VoiceOverrides: got %v, want %v", cmd.VoiceOverrides, expectedOverrides)
	}
	if !cmd.RemoveSilence {
		t.Errorf("RemoveSilence: got false, want true")
	}
	if string(cmd.Strategy) != "replace" {
		t.Errorf("Strategy: got %q, want %q", cmd.Strategy, "replace")
	}
	if cmd.Parallelism != 2 {
		t.Errorf("Parallelism: got %d, want 2", cmd.Parallelism)
	}
	if cmd.Destination == nil || cmd.Destination.Group != "Promozionali" {
		t.Errorf("Destination.Group: got %v, want Promozionali", cmd.Destination)
	}
	// godlike/07: HTTP-level metadata round-trip pinned via the actual
	// JSON unmarshal pipeline (catches any future regression where the
	// wire binding silently drops options.metadata — the worst kind
	// of fake availability).
	if cmd.Metadata["trace"] != "video-xyz" || cmd.Metadata["scene"] != "intro" {
		t.Errorf("Metadata round-trip: got %v, want {trace:video-xyz, scene:intro}", cmd.Metadata)
	}

	// Verify 202 body contains the canonical fields.
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("resp unmarshal: %v body=%s", err, rec.Body.String())
	}
	if resp["ok"] != true {
		t.Errorf("resp.ok: got %v, want true", resp["ok"])
	}
	if resp["status"] != "queued" {
		t.Errorf("resp.status: got %v, want queued", resp["status"])
	}
	if resp["job_id"] != "job_abc123" {
		t.Errorf("resp.job_id: got %v, want job_abc123", resp["job_id"])
	}
	if resp["request_id"] != "video-xyz" {
		t.Errorf("resp.request_id: got %v, want video-xyz", resp["request_id"])
	}
	// JSON numbers decode as float64 in map[string]any.
	if v, _ := resp["total_outputs"].(float64); v != 2 {
		t.Errorf("resp.total_outputs: got %v, want 2", resp["total_outputs"])
	}
}

// TestGenerateHandler_RejectsEmptyItems verifies validation rejects
// items=[] with 400.
func TestGenerateHandler_RejectsEmptyItems(t *testing.T) {
	jobsSvc := &stubJobsSvc{returnJob: &job.Job{}}
	r := newTestRouter(jobsSvc)
	rec := doRequest(r, `{"items": []}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400; body=%s", rec.Code, rec.Body.String())
	}
	if jobsSvc.enqueued != nil {
		t.Errorf("enqueue should not be called; got %+v", jobsSvc.enqueued)
	}
}

// TestGenerateHandler_RejectsMixedTexts verifies the P0.2 invariant
// (all items must share the same text). Multi-text fan-out is P0.3 scope.
func TestGenerateHandler_RejectsMixedTexts(t *testing.T) {
	jobsSvc := &stubJobsSvc{returnJob: &job.Job{}}
	r := newTestRouter(jobsSvc)
	body := `{
		"items": [
			{"text": "Testo A", "language": "it-IT"},
			{"text": "Testo B", "language": "en-US"}
		]
	}`
	rec := doRequest(r, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "items[1].text") {
		t.Errorf("error should mention items[1].text; got body=%s", rec.Body.String())
	}
	if jobsSvc.enqueued != nil {
		t.Errorf("enqueue should not be called; got %+v", jobsSvc.enqueued)
	}
}

// TestGenerateHandler_RejectsEmptyTextOrLanguage verifies per-item
// validation rejects empty text or language with 400.
func TestGenerateHandler_RejectsEmptyTextOrLanguage(t *testing.T) {
	jobsSvc := &stubJobsSvc{returnJob: &job.Job{}}
	r := newTestRouter(jobsSvc)

	rec := doRequest(r, `{"items": [{"text": "", "language": "it-IT"}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("empty text: got %d want 400; body=%s", rec.Code, rec.Body.String())
	}
	rec = doRequest(r, `{"items": [{"text": "x", "language": ""}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("empty language: got %d want 400; body=%s", rec.Code, rec.Body.String())
	}
	if jobsSvc.enqueued != nil {
		t.Errorf("enqueue should not be called; got %+v", jobsSvc.enqueued)
	}
}

// TestGenerateHandler_RejectsGroupKindWithoutGroup verifies the
// PR-VO-C1 invariant: kind="group" + empty group → 400.
func TestGenerateHandler_RejectsGroupKindWithoutGroup(t *testing.T) {
	jobsSvc := &stubJobsSvc{returnJob: &job.Job{}}
	r := newTestRouter(jobsSvc)
	body := `{
		"items": [{"text": "x", "language": "it-IT"}],
		"destination": {"kind": "group", "group": ""}
	}`
	rec := doRequest(r, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "destination") {
		t.Errorf("error should mention destination; got body=%s", rec.Body.String())
	}
	if jobsSvc.enqueued != nil {
		t.Errorf("enqueue should not be called; got %+v", jobsSvc.enqueued)
	}
}

// TestGenerateHandler_NormalisesBogusStrategy verifies unknown
// strategy values are normalised to "verify" by asset.NormalizeStrategy
// before reaching the job payload (godlike/07 invariant: never
// surface raw user input through the canonical enum).
func TestGenerateHandler_NormalisesBogusStrategy(t *testing.T) {
	jobsSvc := &stubJobsSvc{returnJob: &job.Job{}}
	r := newTestRouter(jobsSvc)
	body := `{
		"items": [{"text": "x", "language": "it-IT"}],
		"options": {"strategy": "totally-bogus-value"}
	}`
	rec := doRequest(r, body)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status: got %d want 202; body=%s", rec.Code, rec.Body.String())
	}
	if jobsSvc.enqueued == nil {
		t.Fatal("expected Enqueue to be called")
	}
	cmd, ok := jobsSvc.enqueued.Payload.(*voiceover.GenerateVoiceoversCommand)
	if !ok {
		t.Fatalf("Payload type: got %T, want *voiceover.GenerateVoiceoversCommand", jobsSvc.enqueued.Payload)
	}
	if string(cmd.Strategy) != "verify" {
		t.Errorf("Strategy: got %q, want verify (NormalizeStrategy(unknown, force=false))", cmd.Strategy)
	}
}

// TestGenerateHandler_EnqueueErrorReturns500 verifies that a
// failure inside the jobs broker maps to 500 (error path not
// swallowed silently — godlike/07 invariant).
func TestGenerateHandler_EnqueueErrorReturns500(t *testing.T) {
	jobsSvc := &stubJobsSvc{
		returnErr: errors.New("sqlite: database is locked"),
	}
	r := newTestRouter(jobsSvc)
	body := `{"items": [{"text": "x", "language": "it-IT"}]}`
	rec := doRequest(r, body)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500; body=%s", rec.Code, rec.Body.String())
	}
}

// TestGenerateHandler_RegistersOnlyGenerateRoute verifies the slim
// surface: only POST /generate is exposed (Wave 21 retire
// /generate-with-group /batch /promo /sync /groups is closed).
func TestGenerateHandler_RegistersOnlyGenerateRoute(t *testing.T) {
	r := newTestRouter(&stubJobsSvc{})
	hasGenerate := false
	for _, route := range r.Routes() {
		if route.Path == "/generate" && route.Method == "POST" {
			hasGenerate = true
		}
		// Reject any legacy lane that Wave 21 retired.
		if strings.Contains(route.Path, "generate-with-group") ||
			strings.HasSuffix(route.Path, "/batch") ||
			strings.HasSuffix(route.Path, "/promo") ||
			strings.HasSuffix(route.Path, "/sync") ||
			strings.HasSuffix(route.Path, "/groups") {
			t.Errorf("legacy voiceover route leaked: %s %s", route.Method, route.Path)
		}
	}
	if !hasGenerate {
		t.Fatal("expected POST /generate to be registered")
	}
}

// TestRequest_ToCommand_NormalisesInputs verifies the types.go
// mapper: items[] collapse + VoiceOverrides map + FilenameTemplate
// last-non-empty-wins + Strategy normalisation, all without going
// through Gin (decoupled from handler context).
func TestRequest_ToCommand_NormalisesInputs(t *testing.T) {
	req := &GenerateVoiceoversRequest{
		Items: []VoiceoverItem{
			{Text: "Hello", Language: "en-US", Filename: "hello-en.mp3"},
			{Text: "Hello", Language: "it-IT", Voice: "it-IT-DiegoNeural"},
			{Text: "Hello", Language: "fr-FR"},
		},
		Options: VoiceoverOptions{
			Strategy:    "SKIP", // case-insensitive normalisation enters via ToLower
			Parallelism: 4,
		},
	}
	cmd := req.ToCommand()
	if cmd.Text != "Hello" {
		t.Errorf("Text: got %q, want Hello", cmd.Text)
	}
	if !reflect.DeepEqual(cmd.Languages, []string{"en-US", "it-IT", "fr-FR"}) {
		t.Errorf("Languages: got %v", cmd.Languages)
	}
	if cmd.VoiceOverrides["it-IT"] != "it-IT-DiegoNeural" {
		t.Errorf("VoiceOverrides[it-IT]: got %q", cmd.VoiceOverrides["it-IT"])
	}
	if _, ok := cmd.VoiceOverrides["en-US"]; ok {
		t.Errorf("VoiceOverrides[en-US] should NOT be set (empty voice)")
	}
	if cmd.FilenameTemplate != "hello-en.mp3" {
		t.Errorf("FilenameTemplate: got %q, want hello-en.mp3", cmd.FilenameTemplate)
	}
	if string(cmd.Strategy) != "skip" {
		t.Errorf("Strategy: got %q, want skip (case-insensitive normalise)", cmd.Strategy)
	}
	if cmd.Parallelism != 4 {
		t.Errorf("Parallelism: got %d, want 4", cmd.Parallelism)
	}
}

// TestRequest_ToCommand_DuplicateLanguagesLastWins locks the Edge
// case: two items share the same language code. The VoiceOverrides
// map (keyed by language) silently picks the LATER item's voice.
// This is the documented P0.2 behaviour; P0.3 multi-item fan-out
// will revisit this with worker-side per-item dispatch.
func TestRequest_ToCommand_DuplicateLanguagesLastWins(t *testing.T) {
	req := &GenerateVoiceoversRequest{
		Items: []VoiceoverItem{
			{Text: "Hello", Language: "it-IT", Voice: "it-IT-BenignoNeural"},
			{Text: "Hello", Language: "it-IT", Voice: "it-IT-DiegoNeural"},
		},
	}
	cmd := req.ToCommand()
	if cmd.VoiceOverrides["it-IT"] != "it-IT-DiegoNeural" {
		t.Errorf("VoiceOverrides[it-IT]: got %q, want it-IT-DiegoNeural (last-wins)", cmd.VoiceOverrides["it-IT"])
	}
	if !reflect.DeepEqual(cmd.Languages, []string{"it-IT", "it-IT"}) {
		t.Errorf("Languages: got %v, want [it-IT, it-IT] (duplicates preserved for worker-side fan-out)", cmd.Languages)
	}
}

// TestRequest_ToCommand_DuplicateFilenamesLastWins locks the
// FilenameTemplate last-non-empty-wins behaviour. P0.2 invariant
// (shared text) limits real-world occurrences to per-language
// override collisions; record the behaviour so P0.3 worker-side
// per-item dispatch can rely on it.
func TestRequest_ToCommand_DuplicateFilenamesLastWins(t *testing.T) {
	req := &GenerateVoiceoversRequest{
		Items: []VoiceoverItem{
			{Filename: "first-name.mp3", Language: "en-US", Text: "x"},
			{Filename: "second-name.mp3", Language: "it-IT", Text: "x"},
		},
	}
	cmd := req.ToCommand()
	if cmd.FilenameTemplate != "second-name.mp3" {
		t.Errorf("FilenameTemplate: got %q, want second-name.mp3", cmd.FilenameTemplate)
	}
}

// TestRequest_ToEnqueueRequest_CarriesCorrelationID ensures the
// request_id round-trips into the kernel EnqueueRequest.CorrelationID
// field so the worker-side log stream and dispatcher audit can
// trace the caller across the async boundary.
func TestRequest_ToEnqueueRequest_CarriesCorrelationID(t *testing.T) {
	req := &GenerateVoiceoversRequest{
		RequestID: "vo-trace-xyz",
		Items:     []VoiceoverItem{{Text: "hi", Language: "en-US"}},
	}
	enq := req.ToEnqueueRequest()
	if enq.CorrelationID != "vo-trace-xyz" {
		t.Errorf("CorrelationID: got %q, want vo-trace-xyz", enq.CorrelationID)
	}
	if enq.Type != job.TypeVoiceoverGenerate {
		t.Errorf("Type: got %q, want %q", enq.Type, job.TypeVoiceoverGenerate)
	}
	cmd, ok := enq.Payload.(*voiceover.GenerateVoiceoversCommand)
	if !ok {
		t.Fatalf("Payload type: got %T, want *voiceover.GenerateVoiceoversCommand", enq.Payload)
	}
	if cmd.Text != "hi" {
		t.Errorf("Payload.Text: got %q, want hi", cmd.Text)
	}
}

// TestRequest_ToEnqueueRequest_EmptyRequestID ensures absence stays
// a legitimate signal — empty CorrelationID, not "vo-trace-empty".
func TestRequest_ToEnqueueRequest_EmptyRequestID(t *testing.T) {
	req := &GenerateVoiceoversRequest{
		Items: []VoiceoverItem{{Text: "hi", Language: "en-US"}},
	}
	enq := req.ToEnqueueRequest()
	if enq.CorrelationID != "" {
		t.Errorf("CorrelationID: got %q, want empty (no request_id supplied)", enq.CorrelationID)
	}
}

// TestGenerateHandler_RejectsExplicitKindWithoutFolderID locks the
// PR-VO-C1 invariant for the explicit routing branch: kind="explicit"
// + empty folder_id MUST hard 400 (godlike/07 no-fake-availability —
// silent fallback to GroupsResolver is the worst kind of fake
// availability).
func TestGenerateHandler_RejectsExplicitKindWithoutFolderID(t *testing.T) {
	jobsSvc := &stubJobsSvc{returnJob: &job.Job{}}
	r := newTestRouter(jobsSvc)
	body := `{
		"items": [{"text": "x", "language": "it-IT"}],
		"destination": {"kind": "explicit", "folder_id": ""}
	}`
	rec := doRequest(r, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "explicit") || !strings.Contains(rec.Body.String(), "folder_id") {
		t.Errorf("error should mention explicit + folder_id; got body=%s", rec.Body.String())
	}
	if jobsSvc.enqueued != nil {
		t.Errorf("enqueue should not be called; got %+v", jobsSvc.enqueued)
	}
}

// TestGenerateHandler_AcceptsExplicitKindWithFolderID sanity-checks
// the happy path of the explicit branch: kind="explicit" +
// non-empty folder_id proceeds to enqueue normally (no validation
// regression introduced by the explicit check).
func TestGenerateHandler_AcceptsExplicitKindWithFolderID(t *testing.T) {
	jobsSvc := &stubJobsSvc{returnJob: &job.Job{}}
	r := newTestRouter(jobsSvc)
	body := `{
		"items": [{"text": "x", "language": "it-IT"}],
		"destination": {"kind": "explicit", "folder_id": "0AbC123XyZ"}
	}`
	rec := doRequest(r, body)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status: got %d want 202; body=%s", rec.Code, rec.Body.String())
	}
	if jobsSvc.enqueued == nil {
		t.Fatal("expected Enqueue to be called")
	}
	cmd := jobsSvc.enqueued.Payload.(*voiceover.GenerateVoiceoversCommand)
	if cmd.Destination == nil || cmd.Destination.Kind != "explicit" || cmd.Destination.FolderID != "0AbC123XyZ" {
		t.Errorf("Destination: got %+v, want kind=explicit folder_id=0AbC123XyZ", cmd.Destination)
	}
}

// TestRequest_ToCommand_ForwardsMetadata locks metadata forward
// through the canonical command (godlike/07 no-silent-drop
// invariant). The Command.Metadata field is the merge target for
// per-row collision resolution; losing it on the wire would silently
// corrupt downstream journal entries.
func TestRequest_ToCommand_ForwardsMetadata(t *testing.T) {
	meta := map[string]any{"trace": "video-xyz", "scene": "intro"}
	req := &GenerateVoiceoversRequest{
		Items: []VoiceoverItem{{Text: "x", Language: "en-US"}},
		Options: VoiceoverOptions{
			Metadata: meta,
		},
	}
	cmd := req.ToCommand()
	if !reflect.DeepEqual(cmd.Metadata, meta) {
		t.Errorf("Metadata: got %v, want %v", cmd.Metadata, meta)
	}
}
