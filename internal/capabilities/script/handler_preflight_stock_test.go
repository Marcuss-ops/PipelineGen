package script

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// CS27-06 regression suite. Each case reproduces a live failure that the route
// used to answer ok=true for:
//
//	r14  stock Drive file not ingested      → ok=false, file id named
//	r12  Drive folder of another project    → explicit warning
//	r-s  clip duration vs catalog mismatch  → ok=false, both values named
//
// None of these may ever be a green preflight again.

// fakeStockReader answers the registry port per reference key.
type fakeStockReader struct {
	facts map[string]StockPreflightFact
	errs  map[string]error
}

func (f fakeStockReader) PreflightStockFacts(_ context.Context, ref StockPreflightRef) (StockPreflightFact, bool, error) {
	key := ref.ID
	if err := f.errs[key]; err != nil {
		return StockPreflightFact{}, false, err
	}
	fact, ok := f.facts[key]
	return fact, ok, nil
}

// fakeFolderReader answers the Drive folder port per folder id.
type fakeFolderReader struct {
	names map[string]string
	errs  map[string]error
}

func (f fakeFolderReader) PreflightStockFolderName(_ context.Context, folderID string) (string, bool, error) {
	if err := f.errs[folderID]; err != nil {
		return "", false, err
	}
	name, ok := f.names[folderID]
	return name, ok, nil
}

func newPreflightRouterWithStock(t *testing.T, assets AssetPreflightReader, stock StockPreflightReader, folders StockFolderPreflightReader) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := NewHandlerGenerate(nil, nil, nil, nil, nil)
	h.SetAssetPreflight(assets)
	h.SetStockPreflight(stock, folders)
	r := gin.New()
	r.POST("/api/script/preflight", h.Preflight)
	return r
}

func postPreflightTo(t *testing.T, router *gin.Engine, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/script/preflight", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// preflightR14Payload is the r14 shape: one scene, one stock binding that
// references a Drive FILE directly (not a folder), and no clips at all.
const preflightR14Payload = `{
  "version": 2,
  "items": [{
    "id": "item-r14",
    "project": "de_gennaro_gratteri_compilation_20261127",
    "language": "it",
    "title": "De Gennaro Gratteri",
    "source": {"type": "text", "topic": "gratteri", "source_text": "testo di prova"},
    "script_params": {
      "target_words": 120,
      "segments": [{"id": "apertura-gratteri", "kind": "narration", "topic": "gratteri",
        "source_text": "testo di prova", "target_words": 120, "min_words": 120, "max_words": 120}]
    },
    "output": {
      "stock_enabled": "enabled",
      "stock_bindings": [{
        "index": 0, "scene_id": "scene-0", "segment_id": "apertura-gratteri",
        "drive_link": "https://drive.google.com/file/d/13Hsr2UnregisteredDriveFile/view",
        "source": "drive", "start_ms": 0, "end_ms": 5000, "fallback": false
      }]
    }
  }]
}`

// preflightR12Payload is the r12 shape: a stock_pool_slot bound to a Drive
// folder, while the item declares a completely different project.
const preflightR12Payload = `{
  "version": 2,
  "items": [{
    "id": "item-r12",
    "project": "de_gennaro_gratteri_compilation_20261127",
    "language": "it",
    "title": "De Gennaro Gratteri",
    "source": {"type": "text", "topic": "gratteri", "source_text": "testo di prova"},
    "script_params": {
      "target_words": 120,
      "segments": [{"id": "apertura-gratteri", "kind": "narration", "topic": "gratteri",
        "source_text": "testo di prova", "target_words": 120, "min_words": 120, "max_words": 120}]
    },
    "output": {
      "stock_enabled": "enabled",
      "stock_bindings": [{
        "index": 0, "scene_id": "scene-0", "segment_id": "apertura-gratteri",
        "folder_id": "1augiasOnlyFolderId001",
        "folder_link": "https://drive.google.com/drive/folders/1augiasOnlyFolderId001",
        "source": "drive", "start_ms": 0, "end_ms": 5000, "stock_pool_slot": true, "fallback": false
      }]
    }
  }]
}`

// preflightClipDurationPayload declares an editorial 53010 ms window for a clip
// whose catalog copy is 52010 ms: the one-second drift the ticket recorded.
const preflightClipDurationPayload = `{
  "version": 2,
  "items": [{
    "id": "item-duration",
    "language": "it",
    "source": {"type": "text", "topic": "gratteri", "source_text": "testo di prova"},
    "script_params": {
      "target_words": 120,
      "segments": [{"id": "scene-1", "kind": "clip", "topic": "gratteri",
        "clip_ids": ["clip-53"], "duration_ms": 53010}]
    }
  }]
}`

func TestScriptPreflightFailsWhenStockDriveFileIsNotRegistered(t *testing.T) {
	// r14: the Drive file was never ingested. The job used to pass preflight and
	// die at 85% in PUBLISHING_DOCUMENTS.
	stock := fakeStockReader{facts: map[string]StockPreflightFact{}}
	rec := postPreflightTo(t, newPreflightRouterWithStock(t, nil, stock, nil), preflightR14Payload)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`"ok":false`,
		`"stock_refs":1`,
		"13Hsr2UnregisteredDriveFile",
		"has no saved media registry entry",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q: %s", want, body)
		}
	}
}

func TestScriptPreflightAcceptsRegisteredStockDriveFile(t *testing.T) {
	stock := fakeStockReader{facts: map[string]StockPreflightFact{
		"13Hsr2UnregisteredDriveFile": {
			DriveFileID:      "13Hsr2UnregisteredDriveFile",
			Name:             "apertura.mp4",
			DurationUS:       5_000_000,
			SizeBytes:        1024,
			ContentSHA256Len: 64,
		},
	}}
	rec := postPreflightTo(t, newPreflightRouterWithStock(t, nil, stock, nil), preflightR14Payload)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{`"ok":true`, `"verified":true`, `"resolved":true`, `"duration_us":5000000`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q: %s", want, body)
		}
	}
}

func TestScriptPreflightWarnsWhenStockFolderBelongsToAnotherProject(t *testing.T) {
	// r12: the request pointed at the Augias folder while the project was
	// DeGennaro. The pool is Master-expanded, so the file identity is unknown:
	// the verdict is a warning, never a refusal.
	folders := fakeFolderReader{names: map[string]string{"1augiasOnlyFolderId001": "AUGIAS RAW 2026"}}
	stock := fakeStockReader{}
	rec := postPreflightTo(t, newPreflightRouterWithStock(t, nil, stock, folders), preflightR12Payload)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`"ok":true`,
		`"kind":"pool"`,
		"non appartiene al progetto dichiarato",
		"AUGIAS RAW 2026",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q: %s", want, body)
		}
	}
}

func TestScriptPreflightAcceptsMatchingStockFolder(t *testing.T) {
	folders := fakeFolderReader{names: map[string]string{"1augiasOnlyFolderId001": "De Gennaro - Gratteri"}}
	stock := fakeStockReader{}
	rec := postPreflightTo(t, newPreflightRouterWithStock(t, nil, stock, folders), preflightR12Payload)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "non appartiene al progetto dichiarato") {
		t.Fatalf("matching folder was reported as foreign: %s", rec.Body.String())
	}
}

func TestScriptPreflightFailsWhenClipDurationDiffersFromCatalog(t *testing.T) {
	assets := fakeAssetReader{facts: map[string]AssetPreflightFact{
		"clip-53": {
			ClipID: "clip-53", LifecycleState: "ACTIVE", AssetState: "READY",
			DurationMS: 52010, TextTracks: 1,
			LocalPath: "data/media/clips/general/clip-53.mp4", HasLocalFile: true,
		},
	}}
	rec := postPreflightTo(t, newPreflightRouterWithStock(t, assets, nil, nil), preflightClipDurationPayload)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"durata richiesta 53010ms", "52010ms nel catalogo"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q: %s", want, body)
		}
	}
}

func TestScriptPreflightStockWithoutRegistryIsUnverified(t *testing.T) {
	// An unwired registry must never read as a clean stock inventory.
	rec := postPreflightTo(t, newPreflightRouterWithStock(t, nil, nil, nil), preflightR14Payload)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{`"ok":false`, `"unverified":true`, `"verified":false`, "registro stock non collegato"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q: %s", want, body)
		}
	}
}
