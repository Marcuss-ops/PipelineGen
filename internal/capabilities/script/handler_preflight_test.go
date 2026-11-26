package script

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// fakeAssetReader is the narrow catalog port with per-clip answers.
type fakeAssetReader struct {
	facts map[string]AssetPreflightFact
	errs  map[string]error
}

func (f fakeAssetReader) PreflightAssetFacts(_ context.Context, clipID string) (AssetPreflightFact, bool, error) {
	if err := f.errs[clipID]; err != nil {
		return AssetPreflightFact{}, false, err
	}
	fact, ok := f.facts[clipID]
	return fact, ok, nil
}

const preflightRoutePayload = `{
  "version": 2,
  "items": [{
    "id": "item-1",
    "language": "it",
    "title": "Preflight",
    "script_params": {
      "target_words": 150,
      "segments": [
        {"id": "scene-1", "kind": "clip", "topic": "preflight", "clip_ids": ["clip-ok"]},
        {"id": "scene-2", "kind": "clip", "topic": "preflight", "clip_ids": ["clip-mute"], "no_voiceover": true}
      ]
    },
    "source": {"type": "text", "topic": "preflight", "source_text": "testo di prova"}
  }]
}`

func newPreflightRouter(t *testing.T, reader AssetPreflightReader) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := NewHandlerGenerate(nil, nil, nil, nil, nil)
	h.SetAssetPreflight(reader)
	r := gin.New()
	r.POST("/api/script/preflight", h.Preflight)
	return r
}

func postPreflight(t *testing.T, reader AssetPreflightReader, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/script/preflight", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	newPreflightRouter(t, reader).ServeHTTP(rec, req)
	return rec
}

// TestScriptPreflightAcceptsHealthyCatalog pins the green path: every bound
// clip is ACTIVE/READY with a duration, and the mute cutaway clip needs no
// text track because its segment is no_voiceover.
func TestScriptPreflightAcceptsHealthyCatalog(t *testing.T) {
	reader := fakeAssetReader{facts: map[string]AssetPreflightFact{
		"clip-ok": {ClipID: "clip-ok", LifecycleState: "ACTIVE", AssetState: "READY", DurationMS: 12000, TextTracks: 1,
			LocalPath: "data/media/clips/general/clip-ok.mp4", HasLocalFile: true},
		"clip-mute": {ClipID: "clip-mute", LifecycleState: "ACTIVE", AssetState: "READY", DurationMS: 8000,
			LocalPath: "data/media/clips/general/clip-mute.mp4", HasLocalFile: true},
	}}
	rec := postPreflight(t, reader, preflightRoutePayload)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

// TestScriptPreflightReports422WithPerClipProblems pins the reason the route
// exists: a formally valid body whose assets cannot render answers 422 and
// names each clip and the scene that binds it, before any TTS or GPU work.
func TestScriptPreflightReports422WithPerClipProblems(t *testing.T) {
	reader := fakeAssetReader{
		facts: map[string]AssetPreflightFact{
			// READY but not ACTIVE and without a text track.
			"clip-ok": {ClipID: "clip-ok", LifecycleState: "STAGING", AssetState: "READY", DurationMS: 12000,
				LocalPath: "data/media/clips/general/clip-ok.mp4", HasLocalFile: true},
			// Present but not READY and with no duration.
			"clip-mute": {ClipID: "clip-mute", LifecycleState: "ACTIVE", AssetState: "PROCESSING",
				LocalPath: "data/media/clips/general/clip-mute.mp4", HasLocalFile: true},
		},
	}
	rec := postPreflight(t, reader, preflightRoutePayload)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"asset clip-ok (scene-1): ",
		"lifecycle_state=STAGING (serve ACTIVE)",
		"NO_SPEECH: nessuna traccia testo",
		"asset_state=PROCESSING (serve READY)",
		"durata sconosciuta",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q: %s", want, body)
		}
	}
	// The no_voiceover-only clip must not be blamed for missing speech: it has
	// no text track in this fixture and that is legitimate.
	if strings.Contains(body, "clip-mute (scene-2): NO_SPEECH") {
		t.Fatalf("mute cutaway clip was blamed for missing speech: %s", body)
	}
}

// TestScriptPreflightUnknownClipIsAFailure pins that an id absent from the
// catalog is a failure, never an empty pass.
func TestScriptPreflightUnknownClipIsAFailure(t *testing.T) {
	reader := fakeAssetReader{facts: map[string]AssetPreflightFact{
		"clip-mute": {ClipID: "clip-mute", LifecycleState: "ACTIVE", AssetState: "READY", DurationMS: 8000,
			LocalPath: "data/media/clips/general/clip-mute.mp4", HasLocalFile: true},
	}}
	rec := postPreflight(t, reader, preflightRoutePayload)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "asset clip-ok (scene-1): asset non trovato nel catalogo") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// TestScriptPreflightUnwiredCatalogIsNotGreen pins the false-green guard: with
// no catalog reader the contract is checked but nothing is verified, and the
// response says so instead of answering 200 ok=true.
func TestScriptPreflightUnwiredCatalogIsNotGreen(t *testing.T) {
	rec := postPreflight(t, nil, preflightRoutePayload)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"unverified":true`) || strings.Contains(body, `"ok":true`) {
		t.Fatalf("body = %s", body)
	}
	if !strings.Contains(body, "catalogo non collegato") {
		t.Fatalf("body = %s", body)
	}
}

// TestScriptPreflightRejectsUnknownField pins parity with POST /generate: the
// preflight must fail on exactly the same bodies the submit would 400, or it
// would hand out greens the server then refuses.
func TestScriptPreflightRejectsUnknownField(t *testing.T) {
	body := `{
	  "version": 2,
	  "assemble_final": true,
	  "items": [{"id": "i", "language": "it", "source": {"type": "text", "source_text": "x"}}]
	}`
	rec := postPreflight(t, nil, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// TestScriptPreflightMissingMediaWithoutDriveIsFatal pins the OVL-06 verdict:
// a clip whose local binary is gone AND whose catalog row carries no Drive copy
// can never render, so the preflight fails and names the path the resolver
// expected instead of the old "has no Drive link" shrug.
func TestScriptPreflightMissingMediaWithoutDriveIsFatal(t *testing.T) {
	reader := fakeAssetReader{facts: map[string]AssetPreflightFact{
		"clip-ok": {ClipID: "clip-ok", LifecycleState: "ACTIVE", AssetState: "READY", DurationMS: 12000, TextTracks: 1,
			LocalPath: "data/media/clips/general/yt_video_0_58_v1.mp4"},
		"clip-mute": {ClipID: "clip-mute", LifecycleState: "ACTIVE", AssetState: "READY", DurationMS: 8000,
			LocalPath: "data/media/clips/general/clip-mute.mp4", HasLocalFile: true},
	}}
	rec := postPreflight(t, reader, preflightRoutePayload)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"media locale non risolvibile",
		"data/media/clips/general/yt_video_0_58_v1.mp4",
		"il clip non è reidratabile",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q: %s", want, body)
		}
	}
}

// TestScriptPreflightDriveOnlyClipIsAWarning pins the other half of the
// distinction: local media missing but a Drive copy registered is a degraded
// path, reported as a warning that never turns the verdict green and never
// blocks the submit.
func TestScriptPreflightDriveOnlyClipIsAWarning(t *testing.T) {
	reader := fakeAssetReader{facts: map[string]AssetPreflightFact{
		"clip-ok": {ClipID: "clip-ok", LifecycleState: "ACTIVE", AssetState: "READY", DurationMS: 12000, TextTracks: 1,
			LocalPath: "data/media/clips/general/yt_video_0_58_v1.mp4",
			DriveLink: "https://drive.google.com/file/d/abc/view", HasDriveFile: true},
		"clip-mute": {ClipID: "clip-mute", LifecycleState: "ACTIVE", AssetState: "READY", DurationMS: 8000,
			LocalPath: "data/media/clips/general/clip-mute.mp4", HasLocalFile: true},
	}}
	rec := postPreflight(t, reader, preflightRoutePayload)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`"ok":true`,
		`"warnings":`,
		"copia Drive disponibile: https://drive.google.com/file/d/abc/view",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q: %s", want, body)
		}
	}
}

// TestPreflightAssetCheckCatalogErrorIsNotAVerdict pins that a reader error is
// reported as "not verified", never as a clean asset.
func TestPreflightAssetCheckCatalogErrorIsNotAVerdict(t *testing.T) {
	reader := fakeAssetReader{errs: map[string]error{"clip-ok": errors.New("db closed")}}
	check := preflightAssetCheck(context.Background(), reader,
		scriptpkg.ClipRequirement{ClipID: "clip-ok", NeedSpeech: true})
	if check.OK {
		t.Fatalf("check = %+v, want a failure", check)
	}
	if len(check.Problems) != 1 || !strings.Contains(check.Problems[0], "catalogo non consultabile") {
		t.Fatalf("problems = %v", check.Problems)
	}
}
