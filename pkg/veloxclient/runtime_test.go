package veloxclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newTestClient points a Client at a test server and asserts the Bearer header
// on every request (the whole SDK must stay authenticated by construction).
func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("%s %s: Authorization = %q, want Bearer test-token", r.Method, r.URL.Path, got)
		}
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL, "test-token")
}

func TestSearchClipsByTopic_BuildsQueryAndDecodes(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != RouteClipsTopicSearch {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("q"); got != "tesla factory" {
			t.Errorf("q = %q", got)
		}
		if got := r.URL.Query().Get("limit"); got != "5" {
			t.Errorf("limit = %q", got)
		}
		if got := r.URL.Query().Get("published_after"); got != "2025-01-01T00:00:00Z" {
			t.Errorf("published_after = %q", got)
		}
		_ = json.NewEncoder(w).Encode(TopicSearchResponse{
			OK: true, Query: "tesla factory", Count: 1,
			Results: []TopicSearchResult{{VideoID: "abc", DirectLink: "https://youtu.be/abc", Duration: 42}},
		})
	})

	resp, err := c.SearchClipsByTopic(context.Background(), TopicSearchQuery{
		Q: "tesla factory", Limit: 5, PublishedAfter: "2025-01-01T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("SearchClipsByTopic: %v", err)
	}
	if len(resp.Results) != 1 || resp.Results[0].VideoID != "abc" {
		t.Fatalf("unexpected results: %+v", resp.Results)
	}
}

func TestSearchClipsByTopic_RequiresQuery(t *testing.T) {
	c := New("http://127.0.0.1:1", "t")
	if _, err := c.SearchClipsByTopic(context.Background(), TopicSearchQuery{}); err == nil {
		t.Fatal("expected an error for an empty query")
	}
}

func TestClipInfo_DecodesAndKeepsRaw(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != RouteClipsInfo {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("url"); got != "https://youtu.be/abc" {
			t.Errorf("url = %q", got)
		}
		// Include a field the typed struct does not model to prove Raw keeps it.
		_, _ = w.Write([]byte(`{"id":"abc","title":"T","duration":42.5,"chapters":[{"t":1}]}`))
	})

	meta, err := c.ClipInfo(context.Background(), "https://youtu.be/abc")
	if err != nil {
		t.Fatalf("ClipInfo: %v", err)
	}
	if meta.ID != "abc" || meta.Duration != 42.5 {
		t.Fatalf("unexpected metadata: %+v", meta)
	}
	if !strings.Contains(string(meta.Raw), "chapters") {
		t.Fatalf("Raw must preserve unmodelled fields, got %s", meta.Raw)
	}
}

func TestStockPipelineRun_PostsRunShape(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != RouteStockPipelineRun {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if _, ok := body["search_queries"]; !ok {
			t.Errorf("run body must carry search_queries, got %v", body)
		}
		_ = json.NewEncoder(w).Encode(StockRunResponse{JobID: "job_1", Status: "QUEUED"})
	})

	resp, err := c.StockPipelineRun(context.Background(), StockRunRequest{
		SearchQueries: []string{"city b-roll"}, Async: true,
	})
	if err != nil {
		t.Fatalf("StockPipelineRun: %v", err)
	}
	if resp.JobID != "job_1" || resp.Status != "QUEUED" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestStockPipelineSearchAndRun_PostsQueriesShape(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != RouteStockPipelineSearchAndRun {
			t.Errorf("path = %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		// search_queries on search-and-run is the legacy shape and fails
		// closed server-side; the client must never send it here.
		if _, bad := body["search_queries"]; bad {
			t.Errorf("search-and-run must not send search_queries: %v", body)
		}
		if _, ok := body["queries"]; !ok {
			t.Errorf("search-and-run must send queries: %v", body)
		}
		_ = json.NewEncoder(w).Encode(StockRunResponse{JobID: "job_2", Status: "QUEUED"})
	})

	resp, err := c.StockPipelineSearchAndRun(context.Background(), StockSearchAndRunRequest{
		Queries: []StockQuery{{Q: "city b-roll", Limit: 5}}, MaxVideos: 3, Async: true,
	})
	if err != nil {
		t.Fatalf("StockPipelineSearchAndRun: %v", err)
	}
	if resp.JobID != "job_2" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestStockPipelineSearchAndRun_RequiresQuery(t *testing.T) {
	c := New("http://127.0.0.1:1", "t")
	if _, err := c.StockPipelineSearchAndRun(context.Background(), StockSearchAndRunRequest{}); err == nil {
		t.Fatal("expected an error for an empty queries array")
	}
}

func TestRegisterBatch_ReturnsRawEnvelope(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != RouteMediaRegisterBatch {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"ok":true,"registered":2}`))
	})
	raw, err := c.RegisterBatch(context.Background(), map[string]any{"clips": []any{}})
	if err != nil {
		t.Fatalf("RegisterBatch: %v", err)
	}
	if !strings.Contains(string(raw), `"registered":2`) {
		t.Fatalf("unexpected raw: %s", raw)
	}
}

func TestUploadVideoClip_SendsMultipart(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != RouteMediaUploadVideo {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "multipart/form-data" {
			t.Fatalf("content-type = %q (%v)", r.Header.Get("Content-Type"), err)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("parse multipart: %v", err)
		}
		if got := r.FormValue("description"); got != "a remote clip" {
			t.Errorf("description = %q", got)
		}
		if got := r.FormValue("tags"); got != `["interview"]` {
			t.Errorf("tags = %q", got)
		}
		file, hdr, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("FormFile: %v", err)
		}
		defer file.Close()
		if hdr.Filename != "clip.mp4" {
			t.Errorf("filename = %q", hdr.Filename)
		}
		data, _ := io.ReadAll(file)
		if string(data) != "BYTES" {
			t.Errorf("file content = %q", data)
		}
		if params["boundary"] == "" {
			t.Error("missing multipart boundary")
		}
		_, _ = w.Write([]byte(`{"ok":true,"clip_id":"c1"}`))
	})

	raw, err := c.UploadVideoClip(context.Background(), VideoUploadMeta{
		Filename:    "clip.mp4",
		Description: "a remote clip",
		Tags:        []string{"interview"},
	}, strings.NewReader("BYTES"))
	if err != nil {
		t.Fatalf("UploadVideoClip: %v", err)
	}
	if !strings.Contains(string(raw), `"clip_id":"c1"`) {
		t.Fatalf("unexpected raw: %s", raw)
	}
}

func TestDownloadClip_PostsToCanonicalPath(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/media/clips/youtube/clips/yt_1/download" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte("VIDEO"))
	})

	var buf strings.Builder
	contentType, err := c.DownloadClip(context.Background(), "youtube", "yt_1", &buf)
	if err != nil {
		t.Fatalf("DownloadClip: %v", err)
	}
	if contentType != "video/mp4" || buf.String() != "VIDEO" {
		t.Fatalf("contentType=%q body=%q", contentType, buf.String())
	}
}

func TestM2MClient_ListsRunnableJobTypes(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != RouteM2MJobTypes {
			t.Fatalf("unexpected catalog request: %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(M2MJobTypesResponse{Types: []string{"clip.render", "script.generate", "voiceover.generate"}})
	})
	got, err := New(c.baseURL, "test-token", WithM2M).ListM2MJobTypes(context.Background())
	if err != nil {
		t.Fatalf("ListM2MJobTypes: %v", err)
	}
	if len(got.Types) != 3 || got.Types[1] != "script.generate" {
		t.Fatalf("unexpected runnable types: %+v", got.Types)
	}
}

func TestM2MClient_UsesScopedSubmitAndPollRoutes(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == RouteM2MJobs:
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode M2M body: %v", err)
			}
			if body["type"] != "script.generate" || body["idempotency_key"] != "run-1" {
				t.Fatalf("unexpected M2M envelope: %#v", body)
			}
			_ = json.NewEncoder(w).Encode(AsyncResponse{JobID: "job_m2m", Status: "QUEUED"})
		case r.Method == http.MethodGet && r.URL.Path == RouteM2MJob("job_m2m"):
			_ = json.NewEncoder(w).Encode(JobStatusResponse{ID: "job_m2m", Type: "script.generate", Status: "SUCCEEDED"})
		default:
			t.Fatalf("unexpected M2M request: %s %s", r.Method, r.URL.Path)
		}
	})
	m2m := New(c.baseURL, "test-token", WithM2M)
	ack, err := m2m.SubmitBytes(context.Background(), RouteM2MJobs,
		[]byte(`{"type":"script.generate","idempotency_key":"run-1","payload":{}}`), "run-1")
	if err != nil || ack.JobID != "job_m2m" {
		t.Fatalf("SubmitBytes M2M: ack=%+v err=%v", ack, err)
	}
	status, err := m2m.WaitJob(context.Background(), ack.JobID, WithPollInterval(time.Millisecond), WithPollTimeout(time.Second))
	if err != nil || status.Status != "SUCCEEDED" {
		t.Fatalf("WaitJob M2M: status=%+v err=%v", status, err)
	}
}

func TestWaitJob_ReturnsOnSuccess(t *testing.T) {
	var calls int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		status := "queued"
		if n >= 2 {
			status = "SUCCEEDED"
		}
		_ = json.NewEncoder(w).Encode(JobStatusResponse{ID: "job_1", Status: status, Result: map[string]any{"asset_id": "a1"}})
	})

	var seen []string
	got, err := c.WaitJob(context.Background(), "job_1",
		WithPollInterval(time.Millisecond),
		WithPollTimeout(2*time.Second),
		WithPollObserver(func(r *JobStatusResponse) { seen = append(seen, r.Status) }),
	)
	if err != nil {
		t.Fatalf("WaitJob: %v", err)
	}
	if got.Status != "SUCCEEDED" {
		t.Fatalf("status = %q", got.Status)
	}
	if len(seen) < 2 || seen[0] != "queued" {
		t.Fatalf("observer saw %v", seen)
	}
}

func TestWaitJob_ReturnsResponseOnTerminalFailure(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(JobStatusResponse{ID: "job_x", Status: "FAILED", Error: "boom"})
	})

	got, err := c.WaitJob(context.Background(), "job_x",
		WithPollInterval(time.Millisecond),
		WithPollTimeout(time.Second),
	)
	if !errors.Is(err, ErrJobFailed) {
		t.Fatalf("err = %v, want ErrJobFailed", err)
	}
	if got == nil || got.Error != "boom" {
		t.Fatalf("terminal response must be returned with the error, got %+v", got)
	}
}

func TestWaitJob_TimesOutWhileRunning(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(JobStatusResponse{ID: "job_r", Status: "RUNNING"})
	})

	_, err := c.WaitJob(context.Background(), "job_r",
		WithPollInterval(time.Millisecond),
		WithPollMaxInterval(2*time.Millisecond),
		WithPollTimeout(20*time.Millisecond),
	)
	if !errors.Is(err, ErrPollTimeout) {
		t.Fatalf("err = %v, want ErrPollTimeout", err)
	}
}

func TestWaitJob_RespectsContextCancellation(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(JobStatusResponse{ID: "job_c", Status: "RUNNING"})
	})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(5 * time.Millisecond)
		cancel()
	}()
	_, err := c.WaitJob(ctx, "job_c",
		WithPollInterval(5*time.Millisecond),
		WithPollTimeout(time.Minute),
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestWaitJob_RetriesNotFoundWhilePropagating(t *testing.T) {
	var calls int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(JobStatusResponse{ID: "job_p", Status: "COMPLETED"})
	})

	got, err := c.WaitJob(context.Background(), "job_p",
		WithPollInterval(time.Millisecond),
		WithPollTimeout(time.Second),
	)
	if err != nil {
		t.Fatalf("WaitJob: %v", err)
	}
	if got.Status != "COMPLETED" || atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("status=%q calls=%d", got.Status, calls)
	}
}
