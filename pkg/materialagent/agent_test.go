package materialagent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/pkg/veloxclient"
)

func newAgent(t *testing.T, handler http.HandlerFunc) *Agent {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return NewDefaultAgent(veloxclient.New(srv.URL, "tok"))
}

// TestResolve_CatalogFirstStopsThere pins the platform rule the autonomy
// depends on: if the Master already owns good material, nothing is acquired.
func TestResolve_CatalogFirstStopsThere(t *testing.T) {
	var youtubeCalls int32
	agent := newAgent(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/media/search":
			_, _ = w.Write([]byte(`{"items":[{"asset_id":"a1","source":"youtube","title":"Tesla factory","duration_ms":8000,"width":1920,"height":1080,"score":0.9}],"next_cursor":""}`))
		case "/api/clips/search":
			atomic.AddInt32(&youtubeCalls, 1)
			_, _ = w.Write([]byte(`{"results":[]}`))
		default:
			t.Errorf("unexpected call: %s", r.URL.Path)
		}
	})

	got, err := agent.Resolve(context.Background(), MaterialRequest{
		ProjectID: "p1", SceneID: "s1", Description: "Tesla factory", Count: 1,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(got) != 1 || got[0].AssetID != "a1" || !got[0].Registered {
		t.Fatalf("unexpected material: %+v", got)
	}
	if n := atomic.LoadInt32(&youtubeCalls); n != 0 {
		t.Fatalf("catalog hit must not fall through to youtube (calls=%d)", n)
	}
}

// TestResolve_FallsBackToYouTubeAndExtracts pins the catalog-miss path: live
// discovery → info → process(selection=important) → job poll → registered.
func TestResolve_FallsBackToYouTubeAndExtracts(t *testing.T) {
	var sawSelection, sawDestination bool
	agent := newAgent(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/media/search":
			_, _ = w.Write([]byte(`{"items":[]}`))
		case "/api/clips/search":
			_, _ = w.Write([]byte(`{"ok":true,"results":[{"video_id":"vid1","title":"Factory tour","direct_link":"https://youtu.be/vid1","duration":600,"similarity_score":80}]}`))
		case "/api/clips/exists":
			_, _ = w.Write([]byte(`{"ok":true,"exists":false}`))
		case "/api/clips/info":
			_, _ = w.Write([]byte(`{"id":"vid1","title":"Factory tour","duration":600}`))
		case "/api/clips/process":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode process body: %v", err)
			}
			if sel, ok := body["selection"].(map[string]any); ok && sel["mode"] == "important" {
				sawSelection = true
			}
			if _, ok := body["destination"].(map[string]any); ok {
				sawDestination = true
			}
			if _, bad := body["group"]; bad {
				t.Error("destination fields must be nested, not top-level")
			}
			_, _ = w.Write([]byte(`{"job_id":"job_yt1","status":"QUEUED"}`))
		case "/api/jobs/job_yt1/full":
			_, _ = w.Write([]byte(`{"id":"job_yt1","status":"SUCCEEDED","result":{"asset_id":"asset_yt1"}}`))
		default:
			t.Errorf("unexpected call: %s", r.URL.Path)
		}
	})

	got, err := agent.Resolve(context.Background(), MaterialRequest{
		ProjectID: "p1", SceneID: "s2", Description: "factory tour", Count: 1,
		Constraints: Constraints{DurationMinSeconds: 5, DurationMaxSeconds: 12},
		Destination: &Destination{Group: "Clips", FolderID: "folder-1", SubfolderName: "Tesla"},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 material, got %d", len(got))
	}
	m := got[0]
	if m.JobID != "job_yt1" || m.AssetID != "asset_yt1" || !m.Registered {
		t.Fatalf("unexpected material: %+v", m)
	}
	if !sawSelection {
		t.Error("clips/process must request selection.mode=important when no explicit segments are given")
	}
	if !sawDestination {
		t.Error("clips/process must carry the nested destination")
	}
}

// TestResolve_FallsBackToStock pins the last-tier fallback for generic b-roll.
func TestResolve_FallsBackToStock(t *testing.T) {
	var sawQueries bool
	agent := newAgent(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/media/search":
			_, _ = w.Write([]byte(`{"items":[]}`))
		case "/api/clips/search":
			_, _ = w.Write([]byte(`{"results":[]}`))
		case "/api/stock-pipeline/search-and-run":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode stock body: %v", err)
			}
			if _, ok := body["queries"]; ok {
				sawQueries = true
			}
			if _, bad := body["search_queries"]; bad {
				t.Error("search-and-run must not send the legacy search_queries shape")
			}
			_, _ = w.Write([]byte(`{"job_id":"job_st1","status":"QUEUED"}`))
		case "/api/jobs/job_st1/full":
			_, _ = w.Write([]byte(`{"id":"job_st1","status":"SUCCEEDED","result":{"asset_id":"asset_st1"}}`))
		default:
			t.Errorf("unexpected call: %s", r.URL.Path)
		}
	})

	got, err := agent.Resolve(context.Background(), MaterialRequest{
		ProjectID: "p1", SceneID: "s3", Description: "city skyline b-roll", Count: 1,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(got) != 1 || got[0].AssetID != "asset_st1" {
		t.Fatalf("unexpected material: %+v", got)
	}
	if !sawQueries {
		t.Error("stock search-and-run must use the queries shape")
	}
}

// TestResolve_NoMaterialIsHonest pins the fail-closed outcome: when every
// source is exhausted (catalog empty, discovery empty, stock unavailable) the
// agent returns ErrNoMaterial rather than a fabricated item. The stock resolver
// is a last-tier acquisition with no read-only candidate list, so it is always
// attempted; its failure is one of the joined errors.
func TestResolve_NoMaterialIsHonest(t *testing.T) {
	agent := newAgent(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/media/search":
			_, _ = w.Write([]byte(`{"items":[]}`))
		case "/api/clips/search":
			_, _ = w.Write([]byte(`{"results":[]}`))
		case "/api/stock-pipeline/search-and-run":
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"stock unavailable"}`))
		default:
			t.Errorf("unexpected call: %s", r.URL.Path)
		}
	})

	got, err := agent.Resolve(context.Background(), MaterialRequest{Description: "nothing here", Count: 1})
	if !errors.Is(err, ErrNoMaterial) {
		t.Fatalf("err = %v, want ErrNoMaterial", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no material, got %+v", got)
	}
	// The per-resolver failures must be preserved, not swallowed.
	if !strings.Contains(err.Error(), "stock") {
		t.Fatalf("joined error should name the failed resolvers, got %v", err)
	}
}

// TestResolve_CallerSourceOverride pins that a caller can force a single
// source, which is how an operator reproduces or constrains a scene.
func TestResolve_CallerSourceOverride(t *testing.T) {
	var catalogCalls int32
	agent := newAgent(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/media/search":
			atomic.AddInt32(&catalogCalls, 1)
			_, _ = w.Write([]byte(`{"items":[{"asset_id":"cat","score":1}]}`))
		case "/api/clips/search":
			_, _ = w.Write([]byte(`{"results":[{"video_id":"v","direct_link":"https://youtu.be/v","similarity_score":10}]}`))
		case "/api/clips/exists":
			_, _ = w.Write([]byte(`{"ok":true,"exists":false}`))
		case "/api/clips/info":
			_, _ = w.Write([]byte(`{}`))
		case "/api/clips/process":
			_, _ = w.Write([]byte(`{"job_id":"job_o"}`))
		case "/api/jobs/job_o/full":
			_, _ = w.Write([]byte(`{"id":"job_o","status":"COMPLETED","result":{"asset_id":"yt"}}`))
		default:
			t.Errorf("unexpected call: %s", r.URL.Path)
		}
	})

	got, err := agent.Resolve(context.Background(), MaterialRequest{
		Description: "x", Count: 1, Sources: []string{ResolverYouTube},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got[0].AssetID != "yt" {
		t.Fatalf("expected the youtube material, got %+v", got[0])
	}
	if atomic.LoadInt32(&catalogCalls) != 0 {
		t.Fatal("an explicit Sources override must not consult the catalog")
	}
}

func TestImportFile_UploadsAndRegisters(t *testing.T) {
	var sawFile bool
	agent := newAgent(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/media/clips/upload-video" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("parse multipart: %v", err)
		}
		if _, _, err := r.FormFile("file"); err == nil {
			sawFile = true
		}
		if got := r.FormValue("description"); got != "a remote clip" {
			t.Errorf("description = %q", got)
		}
		_, _ = w.Write([]byte(`{"ok":true,"clip_id":"c9","name":"clip","drive_file_id":"d9","source":"material-agent"}`))
	})

	m, err := agent.ImportFile(context.Background(),
		MaterialRequest{Description: "a remote clip", MaterialType: "video"},
		veloxclient.VideoUploadMeta{Filename: "clip.mp4"},
		strings.NewReader("BYTES"))
	if err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	if !sawFile {
		t.Fatal("the file part was not received")
	}
	if m.AssetID != "c9" || !m.Registered || !m.HasDrive {
		t.Fatalf("unexpected material: %+v", m)
	}
}

func TestScorer_PrefersFitAndDurableBytes(t *testing.T) {
	s := DefaultScorer()
	req := MaterialRequest{Constraints: Constraints{DurationMinSeconds: 5, DurationMaxSeconds: 12}}

	fits := Candidate{DurationSeconds: 8, HasDrive: true, Width: 1920, Relevance: 0.6}
	misses := Candidate{DurationSeconds: 60, HasDrive: true, Width: 1920, Relevance: 0.6}
	if s.Score(fits, req) <= s.Score(misses, req) {
		t.Fatalf("a duration that fits must outrank one that does not (fit=%v miss=%v)", s.Score(fits, req), s.Score(misses, req))
	}

	ranked := s.Rank([]Candidate{misses, fits}, req)
	if ranked[0].DurationSeconds != 8 {
		t.Fatalf("Rank must put the fitting candidate first, got %+v", ranked[0])
	}
}

func TestPolicy_PlanDefaultsAndOverride(t *testing.T) {
	p := DefaultPolicy()
	if got := p.Plan(MaterialRequest{}); strings.Join(got, ",") != "catalog,youtube,stock" {
		t.Fatalf("default plan = %v", got)
	}
	if got := p.Plan(MaterialRequest{Sources: []string{"stock", " "}}); strings.Join(got, ",") != "stock" {
		t.Fatalf("override plan = %v", got)
	}
}
