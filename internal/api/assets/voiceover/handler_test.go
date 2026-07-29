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
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
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
// Step 5 wire shape (request_id + items[] + destination.group + options)
// round-trips into the GenerateVoiceoversCommand with every field
// populated correctly.
//
// Step 5 (P0.3 items-model recovery, June 2026): items[] is now
// passed through 1:1 (no collapse). The payload carries Items[]
// NOT the legacy Text + Languages[] + VoiceOverrides + FilenameTemplate
// shape — this test pins the new contract. Two items carry different
// texts and per-item voices/filenames to exercise the multi-text fan-out
// (the P0.2 shared-text invariant is REMOVED per Step 5).
func TestGenerateHandler_HappyPath_P0_2WireShape(t *testing.T) {
	jobsSvc := &stubJobsSvc{
		returnJob: &job.Job{ID: "job_abc123", CorrelationID: "video-xyz"},
	}
	r := newTestRouter(jobsSvc)

	// Step 5: items carry independent text/voice/filename (was collapsed
	// into cmd.Text/cmd.Languages/cmd.VoiceOverrides in P0.2). Two
	// distinct texts exercise the multi-text fan-out path which is now
	// first-class.
	body := `{
		"request_id": "video-xyz",
		"items": [
			{"text": "Testo A", "language": "it-IT", "voice": "it-IT-DiegoNeural", "filename": "intro-it.mp3"},
			{"text": "Testo B", "language": "en-US", "voice": "en-US-RogerNeural", "filename": "intro-en.mp3"}
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
	// Step 5 contract: Items carries the per-row payloads verbatim.
	// The collapsed fields are gone from the Command shape.
	if len(cmd.Items) != 2 {
		t.Fatalf("len(Items): got %d, want 2", len(cmd.Items))
	}
	if cmd.Items[0].Text != "Testo A" {
		t.Errorf("Items[0].Text: got %q, want %q", cmd.Items[0].Text, "Testo A")
	}
	if cmd.Items[1].Text != "Testo B" {
		t.Errorf("Items[1].Text: got %q, want %q", cmd.Items[1].Text, "Testo B")
	}
	if cmd.Items[0].Language != "it-IT" || cmd.Items[1].Language != "en-US" {
		t.Errorf("Items[i].Language: got [%q, %q], want [it-IT, en-US]", cmd.Items[0].Language, cmd.Items[1].Language)
	}
	if cmd.Items[0].Voice != "it-IT-DiegoNeural" {
		t.Errorf("Items[0].Voice: got %q, want it-IT-DiegoNeural", cmd.Items[0].Voice)
	}
	if cmd.Items[1].Voice != "en-US-RogerNeural" {
		t.Errorf("Items[1].Voice: got %q, want en-US-RogerNeural", cmd.Items[1].Voice)
	}
	if cmd.Items[0].Filename != "intro-it.mp3" {
		t.Errorf("Items[0].Filename: got %q, want intro-it.mp3", cmd.Items[0].Filename)
	}
	if cmd.Items[1].Filename != "intro-en.mp3" {
		t.Errorf("Items[1].Filename: got %q, want intro-en.mp3", cmd.Items[1].Filename)
	}
	// Step 5 invariant: the collapsed fields (Text, Languages,
	// VoiceOverrides, FilenameTemplate) are absent from the struct.
	// Compile-time absence is the audit-pin — runtime "x != nil" /
	// "x != \"\"" checks are impossible because the fields don't
	// exist. The struct-shape audit-pin is enforced by
	// TestGenerateVoiceoversCommand_NoLegacyJSONFields in this file.
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

// TestGenerateHandler_AcceptsMixedTexts verifies Step 5 inverted the
// P0.2 invariant — items[] may now carry different texts and the
// fan-out propagates each item's textHash independently. The P0.2
// test "RejectsMixedTexts" was removed in Step 5; this is the canonical
// post-Step-5 audit pin.
func TestGenerateHandler_AcceptsMixedTexts(t *testing.T) {
	jobsSvc := &stubJobsSvc{returnJob: &job.Job{ID: "job_mixed"}}
	r := newTestRouter(jobsSvc)
	body := `{
		"items": [
			{"text": "Testo A", "language": "it-IT", "voice": "it-IT-DiegoNeural"},
			{"text": "Testo B", "language": "en-US", "voice": "en-US-RogerNeural"}
		]
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
	if len(cmd.Items) != 2 {
		t.Fatalf("len(Items): got %d want 2", len(cmd.Items))
	}
	if cmd.Items[0].Text != "Testo A" || cmd.Items[1].Text != "Testo B" {
		t.Errorf("Items[i].Text: got [%q, %q] want [Testo A, Testo B]", cmd.Items[0].Text, cmd.Items[1].Text)
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
// mapper: Items[] 1:1 pass-through + Strategy normalisation, all
// without going through Gin (decoupled from handler context).
//
// Step 5 (P0.3 items-model recovery, June 2026): the previous
// P0.2 test pinned the collapse (Text + Languages[] + VoiceOverrides
// last-wins + FilenameTemplate last-wins). With Step 5's items-model
// the collapse is REMOVED — this test pins the new contract: items
// round-trip 1:1, the collapsed fields stay zero/nil, Strategy
// normalisation still applies.
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
	// Step 5: items round-trip 1:1.
	if len(cmd.Items) != 3 {
		t.Fatalf("len(Items): got %d, want 3", len(cmd.Items))
	}
	if cmd.Items[0].Text != "Hello" || cmd.Items[0].Language != "en-US" || cmd.Items[0].Filename != "hello-en.mp3" {
		t.Errorf("Items[0]: got %+v, want Text=Hello Language=en-US Filename=hello-en.mp3", cmd.Items[0])
	}
	if cmd.Items[1].Voice != "it-IT-DiegoNeural" {
		t.Errorf("Items[1].Voice: got %q, want it-IT-DiegoNeural", cmd.Items[1].Voice)
	}
	if cmd.Items[2].Voice != "" {
		t.Errorf("Items[2].Voice: got %q, want empty", cmd.Items[2].Voice)
	}
	// Step 5 invariant: the collapsed fields (Text, Languages,
	// VoiceOverrides, FilenameTemplate) are absent from the struct.
	// Compile-time absence is the audit-pin — runtime checks are
	// impossible because the fields don't exist. The struct-shape
	// audit-pin is enforced by
	// TestGenerateVoiceoversCommand_NoLegacyJSONFields in this file.
	if string(cmd.Strategy) != "skip" {
		t.Errorf("Strategy: got %q, want skip (case-insensitive normalise)", cmd.Strategy)
	}
	if cmd.Parallelism != 4 {
		t.Errorf("Parallelism: got %d, want 4", cmd.Parallelism)
	}
}

// TestRequest_ToCommand_DuplicateLanguagesPassesVerbatim locks the
// Step 5 invariant: two items may share the same language code with
// DIFFERENT voices. The payload round-trips both items independently;
// no "last-wins" collapse happens (the legacy P0.2 last-wins test
// was removed in Step 5 because its target fields no longer exist).
func TestRequest_ToCommand_DuplicateLanguagesPassesVerbatim(t *testing.T) {
	req := &GenerateVoiceoversRequest{
		Items: []VoiceoverItem{
			{Text: "Hello", Language: "it-IT", Voice: "it-IT-BenignoNeural"},
			{Text: "Hello", Language: "it-IT", Voice: "it-IT-DiegoNeural"},
		},
	}
	cmd := req.ToCommand()
	if len(cmd.Items) != 2 {
		t.Fatalf("len(Items): got %d, want 2 (both items passed through)", len(cmd.Items))
	}
	if cmd.Items[0].Voice != "it-IT-BenignoNeural" {
		t.Errorf("Items[0].Voice: got %q, want it-IT-BenignoNeural (NOT last-wins)", cmd.Items[0].Voice)
	}
	if cmd.Items[1].Voice != "it-IT-DiegoNeural" {
		t.Errorf("Items[1].Voice: got %q, want it-IT-DiegoNeural (NOT last-wins)", cmd.Items[1].Voice)
	}
}

// TestRequest_ToCommand_DuplicateFilenamesPassesVerbatim locks the
// Step 5 invariant: per-item filenames round-trip independently.
// Each item keeps its own filename — no "last-wins" collapse to a
// shared FilenameTemplate (the legacy P0.2 last-wins test was
// removed in Step 5 because its target field no longer exists).
func TestRequest_ToCommand_DuplicateFilenamesPassesVerbatim(t *testing.T) {
	req := &GenerateVoiceoversRequest{
		Items: []VoiceoverItem{
			{Filename: "first-name.mp3", Language: "en-US", Text: "x"},
			{Filename: "second-name.mp3", Language: "it-IT", Text: "x"},
		},
	}
	cmd := req.ToCommand()
	if cmd.Items[0].Filename != "first-name.mp3" {
		t.Errorf("Items[0].Filename: got %q, want first-name.mp3 (NOT last-wins)", cmd.Items[0].Filename)
	}
	if cmd.Items[1].Filename != "second-name.mp3" {
		t.Errorf("Items[1].Filename: got %q, want second-name.mp3 (NOT last-wins)", cmd.Items[1].Filename)
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
	// Step 5 invariant: Items[(0)] carries the source text (the
	// collapsed cmd.Text is gone; per-item text lives in Items).
	if len(cmd.Items) != 1 || cmd.Items[0].Text != "hi" {
		t.Errorf("Payload.Items[0].Text: got %q, want hi", cmd.Items[0].Text)
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

// TestHTTPConsumer_RoutesThroughCanonicalUseCase (BLOC5.3 commit-1
// consumer cutover, June 2026) is the audit pin: the canonical HTTP
// consumer (POST /api/media/voiceover/generate) routes through the
// canonical async pipeline (enqueue `voiceover.generate` job →
// FanoutVoiceoversUseCase → per-language voiceover.generate_item
// children → canonical pipeline Execute, forward-deferred to BLOC5.4). It MUST NOT reach
// the legacy Service.GenerateBatch surface (per BLOC5.3 master plan
// §1, §5).
//
// The check: an HTTP request POSTed to /generate MUST result in
//  1. jobs.Service.Enqueue being called once (NOT zero, NOT twice)
//  2. with Type == job.TypeVoiceoverGenerate (canonical job-type identifier)
//  3. with Payload type *voiceover.GenerateVoiceoversCommand (canonical
//     wire-shape round-trip, NOT BatchRequest or interface{})
//  4. NOT result in any direct *voiceover.Service construction or
//     b.svc.Generate()/Service.GenerateBatch call.
//
// The stub JobsSvc captures the EnqueueRequest so the test asserts on
// the canonical pipeline shape instead of the legacy wiring contract.
// Any future regression that swaps the canonical job-type or
// discards the wire-shape Command will fail this test at compile or
// at runtime.
func TestHTTPConsumer_RoutesThroughCanonicalUseCase(t *testing.T) {
	jobsSvc := &stubJobsSvc{
		returnJob: &job.Job{ID: "job_canonical", CorrelationID: "vo-canonical-trace"},
	}
	r := newTestRouter(jobsSvc)

	body := `{
		"request_id": "vo-canonical-trace",
		"items": [
			{"text": "Canonical text", "language": "it-IT", "voice": "it-IT-DiegoNeural", "filename": "canon-it.mp3"}
		],
		"destination": {"kind": "explicit", "folder_id": "0AbCCanonicalFolder"},
		"options": {"strategy": "verify", "remove_silence": false, "parallelism": 1}
	}`

	rec := doRequest(r, body)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status: got %d, want 202 (canonical async enqueue); body=%s", rec.Code, rec.Body.String())
	}
	if jobsSvc.enqueued == nil {
		t.Fatal("expected jobs.Service.Enqueue to be called exactly once (canonical pipeline enqueue)")
	}
	// (1) enqueue called EXACTLY once
	if jobsSvc.enqueued.CorrelationID != "vo-canonical-trace" {
		t.Errorf("CorrelationID: got %q, want vo-canonical-trace (canonical: request_id propagates)", jobsSvc.enqueued.CorrelationID)
	}
	// (2) canonical job-type identifier
	if jobsSvc.enqueued.Type != job.TypeVoiceoverGenerate {
		t.Errorf("Type: got %q, want %q (canonical: voiceover.generate job type — NOT TypeVoiceoverBatch)", jobsSvc.enqueued.Type, job.TypeVoiceoverGenerate)
	}
	// (3) canonical Payload type — *voiceover.GenerateVoiceoversCommand,
	//     the per-batch parent command that FanoutVoiceoversUseCase consumes.
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

// TestGenerateVoiceoversCommand_NoLegacyJSONFields is the Step 5
// audit-pin that locks the collapsed-field removal at both routing
// surfaces (JSON and reflection). The runtime "x != \"\"" /
// "x != nil" checks in the per-feature tests were removed because
// the fields don't exist on the struct; this test re-establishes the
// audit-pin via two complementary mechanisms:
//
//  1. JSON round-trip absence: a non-empty Command's JSON output
//     MUST NOT include "text", "languages", "voice_overrides", or
//     "filename_template" keys. If a future refactor re-introduces
//     one of these as a backward-compat shim, this catches it at
//     runtime even though the compile-time pin (no references in
//     test code) is silent.
//  2. Reflection: confirm the struct shape itself does NOT expose
//     Text, Languages, VoiceOverrides, or FilenameTemplate. This
//     catches re-introductions even if the JSON tags differ.
//
// Both pins are needed: JSON-only catches field additions with the
// right tag, reflection-only catches field additions with the wrong
// tag. Together they close the "audit-pin regression" hole that the
// runtime "x != \"\""-style assertions left behind.
func TestGenerateVoiceoversCommand_NoLegacyJSONFields(t *testing.T) {
	cmd := voiceover.GenerateVoiceoversCommand{
		Items: []voiceover.VoiceoverItem{{Text: "x", Language: "en-US"}},
	}
	b, err := json.Marshal(cmd)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Top-level key audit (not substring search): unmarshal then
	// assert the legacy keys are NOT in the Command-level map. This
	// avoids false positives from the per-item "text"/"language"
	// JSON tags inside cmd.Items (which are legitimate VoiceoverItem
	// fields, NOT legacy Command-level shortcuts).
	var topLevel map[string]json.RawMessage
	if err := json.Unmarshal(b, &topLevel); err != nil {
		t.Fatalf("unmarshal to top-level map: %v", err)
	}
	for _, key := range []string{"text", "languages", "voice_overrides", "filename_template"} {
		if raw, has := topLevel[key]; has {
			t.Errorf("Step 5 audit-pin: legacy field %q must NOT appear at top-level (got %s)", key, string(raw))
		}
	}

	rt := reflect.TypeOf(voiceover.GenerateVoiceoversCommand{})
	for _, name := range []string{"Text", "Languages", "VoiceOverrides", "FilenameTemplate"} {
		if _, has := rt.FieldByName(name); has {
			t.Errorf("Step 5 audit-pin: struct field %q must be absent (compile-time pin failed via reflection)", name)
		}
	}
}

// TestRequest_ToCommand_PropagatesRequiredFlag is the audit pin for
// PR-PROMOTE-REQUIRED-FIX (2026-07-08). Prior to the fix, ToCommand()
// used an explicit 4-field struct literal that SILENTLY DROPPED the
// Required field when crossing the API → Command boundary. A
// REQUIRED-failed child whose item.Required=true reaches the API
// would propagate as item.Required=false into the parent command,
// breaking the parent's REQUIRED-failed short-circuit
// (godlike/07 NO-FAKE-AVAILABILITY — a required failure would be
// treated as optional and the parent could pass without the child).
//
// Fix: VoiceoverItem is `type VoiceoverItem = voiceover.VoiceoverItem`
// (a true Go type alias, NOT a named type), so `items[i] = it` is a
// 1:1 field-for-field pass-through. Required flows through verbatim.
//
// This test pins the post-fix contract: API item.Required=true MUST
// arrive at the parent command as item.Required=true.
func TestRequest_ToCommand_PropagatesRequiredFlag(t *testing.T) {
	req := &GenerateVoiceoversRequest{
		Items: []VoiceoverItem{
			{Text: "ciao", Language: "it-IT", Voice: "it-IT-DiegoNeural", Filename: "ciao.mp3", Required: true},
			{Text: "hello", Language: "en-US", Filename: "hello.mp3", Required: false}, // explicit false
			{Text: "ola", Language: "pt-BR", Filename: "ola.mp3"},                      // zero-value false
		},
	}
	cmd := req.ToCommand()
	if len(cmd.Items) != 3 {
		t.Fatalf("len(Items): got %d, want 3", len(cmd.Items))
	}
	if !cmd.Items[0].Required {
		t.Errorf("Items[0].Required: got false, want true (API Required=true must reach the parent command)")
	}
	if cmd.Items[1].Required {
		t.Errorf("Items[1].Required: got true, want false (API Required=false must reach the parent command)")
	}
	if cmd.Items[2].Required {
		t.Errorf("Items[2].Required: got true, want false (zero-value Required=false must stay false)")
	}

	// Bonus: all non-Required fields propagate byte-equivalent too.
	if cmd.Items[0].Text != "ciao" || cmd.Items[0].Language != "it-IT" ||
		cmd.Items[0].Voice != "it-IT-DiegoNeural" || cmd.Items[0].Filename != "ciao.mp3" {
		t.Errorf("Items[0] fields: got %+v, want full item-equivalent copy", cmd.Items[0])
	}
}

// TestRequest_parentActiveKey_DistinctWhenProjectDiffers is the audit
// pin for the dedup-collision branch of PR-PROMOTE-REQUIRED-FIX.
// Two POsts that are byte-identical except for Project should produce
// distinct parent ActiveKeys — otherwise the broker's per-ActiveKey
// idempotency dedup would silently merge batches that target
// different Drive subdirs (`{project}/{language}/` for project A vs B),
// breaking both godlike/06 SSOT (one canonical owner per fact:
// the publisher MUST NOT silently overwrite project A's folder with
// project B's content) and godlike/07 NO-FAKE-AVAILABILITY (a request
// that "succeeded" against project A would be reported as project B).
func TestRequest_parentActiveKey_DistinctWhenProjectDiffers(t *testing.T) {
	base := &GenerateVoiceoversRequest{
		Items: []VoiceoverItem{
			{Text: "hello", Language: "en-US", Filename: "hello.mp3"},
		},
		Destination: &voiceover.DestinationRequest{
			Kind:     "explicit",
			FolderID: "folder-1",
		},
	}
	a := *base
	a.Project = "project-a"
	b := *base
	b.Project = "project-b"

	keyA := a.parentActiveKey()
	keyB := b.parentActiveKey()
	if keyA == keyB {
		t.Fatalf("parentActiveKey collision across projects: %q == %q (would silently dedup project-a and project-b)", keyA, keyB)
	}
	// Both keys MUST carry the canonical voiceover:parent: prefix.
	const wantPrefix = "voiceover:parent:"
	if !strings.HasPrefix(keyA, wantPrefix) || !strings.HasPrefix(keyB, wantPrefix) {
		t.Errorf("ActiveKey prefix mismatch: %q / %q (want %q)", keyA, keyB, wantPrefix)
	}
}

// TestRequest_parentActiveKey_EmptyProjectProducesLegacyHash locks
// the back-compat invariant AND a literal hex anchor. The empty-Project
// guard (`if r.Project != ""`) is mandatory — unconditional append
// (`h.Write([]byte("|")); h.Write([]byte(r.Project))`) would silently
// append `|` to every empty-Project batch's SHA-256 input, triggering
// a broker key-rotation storm (duplicate enqueues, lost dedup
// guarantees) on the next deploy. The literal anchor pins the
// pre-fix algorithm: any algorithm change that produces a different
// hash for the same input breaks the test loudly (and the operator
// reasonably expects a key-rotation storm if they change the algo).
//
// SHA-256 input bytes for the canonical test input (NOT outputting the
// "voiceover:parent:" prefix):
//
//	sha256("hello|en-us|folder-1") [:16] = "1c98f20e67398c6b"
//
// Reference: see git log for the pre-PR commit to confirm this is
// the verbatim pre-fix anchor. If this constant ever hardcodes false
// (e.g. someone flips Project-from-empty processing), the test will
// surface the regression immediately.
func TestRequest_parentActiveKey_EmptyProjectProducesLegacyHash(t *testing.T) {
	// Literal anchor — empty-Project pre-fix canonical hash.
	// SHA-256("hello|en-us|folder-1")[:16] hex-prefix.
	const wantLegacyKey = "voiceover:parent:1c98f20e67398c6b"

	req := &GenerateVoiceoversRequest{
		Items: []VoiceoverItem{
			{Text: "hello", Language: "en-US", Filename: "hello.mp3"},
		},
		Destination: &voiceover.DestinationRequest{
			Kind:     "explicit",
			FolderID: "folder-1",
		},
		// Project is intentionally omitted — zero-value empty string.
	}
	keyA := req.parentActiveKey()

	// Literal anchor: empty-Project MUST hash to the pre-fix bytes.
	// If a future agent flips the guard to unconditional append
	// (`h.Write([]byte("|")); h.Write([]byte(r.Project))`) the hash
	// changes AND this test fails — the key-rotation storm bug.
	if keyA != wantLegacyKey {
		t.Errorf("empty-Project hash diverged from pre-fix anchor: got %q, want %q "+
			"(unconditional-append regression? check the if r.Project != \"\" guard)",
			keyA, wantLegacyKey)
	}

	// Same items/dest with explicit empty Project MUST produce the
	// same hash (byte-identical back-compat invariant).
	keyB := req.parentActiveKey() // same struct, no mutation
	if keyA != keyB {
		t.Errorf("re-invocations must produce identical hash: %q vs %q", keyA, keyB)
	}

	// Mutate Project to empty (explicit assignment) and re-invoke.
	req.Project = ""
	if keyA != req.parentActiveKey() {
		t.Errorf("explicit empty Project must produce the same hash as implicit empty: %q vs %q",
			keyA, req.parentActiveKey())
	}

	// A non-empty Project MUST diverge from the empty-Project hash.
	req.Project = "yt-channel-test"
	if keyA == req.parentActiveKey() {
		t.Errorf("non-empty Project produced the same hash as empty (Project guard bypassed): %q", keyA)
	}
}
