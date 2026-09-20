package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// ── pure helpers ──────────────────────────────────────────────────────

func TestIdempotencyKeyIsDeterministicAndPayloadSensitive(t *testing.T) {
	payload := []byte(`{"url":"https://youtu.be/abc"}`)
	k1 := idempotencyKey("Milton Leite", payload)
	k2 := idempotencyKey("Milton Leite", payload)
	if k1 != k2 {
		t.Fatalf("key is not deterministic: %q != %q", k1, k2)
	}
	if strings.Contains(k1, " ") {
		t.Fatalf("key %q must not contain spaces (header-safe)", k1)
	}
	if !strings.HasPrefix(k1, "milton-leite-") {
		t.Fatalf("key %q must be namespaced by the project", k1)
	}
	if other := idempotencyKey("Milton Leite", []byte(`{"url":"https://youtu.be/xyz"}`)); other == k1 {
		t.Fatal("different payloads must produce different keys")
	}
}

func TestCleanPayloadStripsANSIAndCR(t *testing.T) {
	raw := []byte("{\r\n  \"a\": \"\x1b[31mred\x1b[0m\"\r\n}\r\n")
	got, err := cleanPayload(raw)
	if err != nil {
		t.Fatalf("cleanPayload: %v", err)
	}
	if strings.ContainsAny(string(got), "\r\x1b") {
		t.Fatalf("cleaned payload still contains control bytes: %q", got)
	}
	if !strings.Contains(string(got), "red") {
		t.Fatalf("cleaning dropped content: %q", got)
	}
}

func TestCleanPayloadRejectsInvalidJSON(t *testing.T) {
	if _, err := cleanPayload([]byte("{not json")); err == nil {
		t.Fatal("want an error for a non-JSON payload")
	}
}

func TestDefaultSourceFor(t *testing.T) {
	cases := map[string]string{"yt_abc_1_2_v1": "youtube", "vo_123": "voiceover", "other": "local"}
	for in, want := range cases {
		if got := defaultSourceFor(in); got != want {
			t.Errorf("defaultSourceFor(%q)=%q, want %q", in, got, want)
		}
	}
}

func TestStoreRoundTripWritesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.json")
	st, err := OpenStore(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := st.Put(jobRecord{JobID: "job_1", Endpoint: "/api/clips/process", IdempotencyKey: "k"}); err != nil {
		t.Fatalf("put: %v", err)
	}
	reopened, err := OpenStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	rec, ok := reopened.Get("job_1")
	if !ok || rec.Endpoint != "/api/clips/process" {
		t.Fatalf("record not persisted: %#v ok=%v", rec, ok)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("staging file left behind: %s", e.Name())
		}
	}
}

// ── command-level tests ───────────────────────────────────────────────

type fakeServer struct {
	mu          sync.Mutex
	idemKeys    []string
	statusCalls int
	submitCount int
	downloadGET bool
}

func (f *fakeServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/clips/process", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.idemKeys = append(f.idemKeys, r.Header.Get("Idempotency-Key"))
		f.submitCount++
		f.mu.Unlock()
		_, _ = w.Write([]byte(`{"ok":true,"job_id":"job_1","status":"queued"}`))
	})
	mux.HandleFunc("/api/jobs/job_1/full", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		f.statusCalls++
		n := f.statusCalls
		f.mu.Unlock()
		if n < 2 {
			_, _ = w.Write([]byte(`{"id":"job_1","status":"RUNNING","progress":10}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"job_1","status":"SUCCEEDED","progress":100}`))
	})
	mux.HandleFunc("/api/media/clips/youtube/clips/yt_x/download", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			f.mu.Lock()
			f.downloadGET = true
			f.mu.Unlock()
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte("mp4-bytes"))
	})
	mux.HandleFunc("/api/media/search", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"sources":["youtube"]`) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"sources not forwarded"}`))
			return
		}
		_, _ = w.Write([]byte(`{"items":[{"asset_id":"yt_1","source":"youtube","score":0.9,"title":"x"}],"partial":false}`))
	})
	return mux
}

// capture runs fn with stdout/stderr redirected and returns the codes + text.
func capture(t *testing.T, fn func() int) (int, string, string) {
	t.Helper()
	origOut, origErr := os.Stdout, os.Stderr
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	os.Stdout, os.Stderr = wOut, wErr
	code := fn()
	_ = wOut.Close()
	_ = wErr.Close()
	os.Stdout, os.Stderr = origOut, origErr
	outBytes, _ := io.ReadAll(rOut)
	errBytes, _ := io.ReadAll(rErr)
	return code, string(outBytes), string(errBytes)
}

func TestSubmitThenPollAgainstFakeServer(t *testing.T) {
	fake := &fakeServer{}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	home := t.TempDir()
	t.Setenv("VELOX_BASE_URL", srv.URL)
	t.Setenv("VELOX_ADMIN_TOKEN", "test-token")
	t.Setenv("VELOX_HOME", home)

	payloadPath := filepath.Join(home, "payload.json")
	if err := os.WriteFile(payloadPath, []byte("{ \"url\": \"https://youtu.be/abc\" }\r\n"), 0o644); err != nil {
		t.Fatalf("write payload: %v", err)
	}

	code, out, errOut := capture(t, func() int {
		return run([]string{"submit", "clips-process", "--key", "milton-leite", "--payload", payloadPath})
	})
	if code != exitOK {
		t.Fatalf("submit exit=%d out=%q err=%q", code, out, errOut)
	}
	if !strings.Contains(out, "job_id=job_1") {
		t.Fatalf("submit output lacks job id: %q", out)
	}
	if len(fake.idemKeys) != 1 || !strings.HasPrefix(fake.idemKeys[0], "milton-leite-") {
		t.Fatalf("server saw idempotency keys %v", fake.idemKeys)
	}

	code, out, errOut = capture(t, func() int {
		return run([]string{"poll", "job_1", "--interval", "1ms"})
	})
	if code != exitOK {
		t.Fatalf("poll exit=%d out=%q err=%q", code, out, errOut)
	}
	if !strings.Contains(out, "SUCCEEDED") {
		t.Fatalf("poll did not report SUCCEEDED: %q", out)
	}
}

func TestReplayReusesStoredIdempotencyKey(t *testing.T) {
	fake := &fakeServer{}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	home := t.TempDir()
	t.Setenv("VELOX_BASE_URL", srv.URL)
	t.Setenv("VELOX_HOME", home)

	payloadPath := filepath.Join(home, "payload.json")
	if err := os.WriteFile(payloadPath, []byte(`{"url":"https://youtu.be/abc"}`), 0o644); err != nil {
		t.Fatalf("write payload: %v", err)
	}

	if code, _, e := capture(t, func() int {
		return run([]string{"submit", "clips-process", "--key", "proj", "--payload", payloadPath})
	}); code != exitOK {
		t.Fatalf("submit failed: %s", e)
	}
	firstKey := fake.idemKeys[0]

	if code, _, e := capture(t, func() int { return run([]string{"replay", "job_1"}) }); code != exitOK {
		t.Fatalf("replay failed: %s", e)
	}
	if len(fake.idemKeys) != 2 {
		t.Fatalf("expected 2 submissions, got %d", len(fake.idemKeys))
	}
	if fake.idemKeys[1] != firstKey {
		t.Fatalf("replay used a NEW idempotency key %q, want the original %q", fake.idemKeys[1], firstKey)
	}
}

func TestDownloadUsesPOSTAndWritesFile(t *testing.T) {
	fake := &fakeServer{}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	home := t.TempDir()
	t.Setenv("VELOX_BASE_URL", srv.URL)
	t.Setenv("VELOX_HOME", home)

	out := filepath.Join(home, "clip.mp4")
	code, _, errOut := capture(t, func() int {
		return run([]string{"download", "yt_x", "--source", "youtube", "-o", out})
	})
	if code != exitOK {
		t.Fatalf("download exit=%d err=%q", code, errOut)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read download: %v", err)
	}
	if string(got) != "mp4-bytes" {
		t.Fatalf("download content=%q", got)
	}
	if fake.downloadGET {
		t.Fatal("download used GET; the endpoint is POST-only")
	}
}

func TestSearchForwardsSourceFilter(t *testing.T) {
	fake := &fakeServer{}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	t.Setenv("VELOX_BASE_URL", srv.URL)
	t.Setenv("VELOX_HOME", t.TempDir())

	code, out, errOut := capture(t, func() int {
		return run([]string{"search", "Milton Leite", "--source", "youtube"})
	})
	if code != exitOK {
		t.Fatalf("search exit=%d out=%q err=%q", code, out, errOut)
	}
	if !strings.Contains(out, "yt_1") {
		t.Fatalf("search output lacks the item: %q", out)
	}
}

func TestSubmitRejectsUnknownAlias(t *testing.T) {
	t.Setenv("VELOX_HOME", t.TempDir())
	if code, _, _ := capture(t, func() int {
		return run([]string{"submit", "nope", "--key", "k", "--payload", "/dev/null"})
	}); code != exitUsage {
		t.Fatalf("unknown alias exit=%d, want %d", code, exitUsage)
	}
}
