package materialagent

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/pkg/veloxclient"
)

// captionFixture is a scripted Master for the caption-gate / dedup tests. It
// records every /api/clips/info url it is asked about so a test can prove a
// candidate was NOT probed (budget spent) and every /api/clips/process call so
// it can prove extraction was skipped (dedup hit).
type captionFixture struct {
	search   string
	infoCaps func(url string) (bool, bool)
	exists   string

	mu        sync.Mutex
	probed    []string
	processed []string
}

func (f *captionFixture) urls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.probed...)
}

func (f *captionFixture) processCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.processed...)
}

func (f *captionFixture) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/media/search":
			_, _ = w.Write([]byte(`{"items":[]}`))
		case "/api/clips/search":
			_, _ = w.Write([]byte(f.search))
		case "/api/clips/exists":
			_, _ = w.Write([]byte(f.exists))
		case "/api/clips/info":
			u := r.URL.Query().Get("url")
			f.mu.Lock()
			f.probed = append(f.probed, u)
			f.mu.Unlock()
			has, ok := f.infoCaps(u)
			if !ok {
				_, _ = w.Write([]byte(`{}`)) // field absent: unknown
				return
			}
			if has {
				_, _ = w.Write([]byte(`{"id":"x","has_captions":true}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":"x","has_captions":false}`))
		case "/api/clips/process":
			f.mu.Lock()
			f.processed = append(f.processed, r.URL.Query().Get("url"))
			f.mu.Unlock()
			_, _ = w.Write([]byte(`{"job_id":"job_cap","status":"QUEUED"}`))
		case "/api/jobs/job_cap/full":
			_, _ = w.Write([]byte(`{"id":"job_cap","status":"SUCCEEDED","result":{"asset_id":"asset_cap"}}`))
		default:
			t.Errorf("unexpected call: %s", r.URL.Path)
		}
	}
}

func newCaptionAgent(t *testing.T, f *captionFixture, opts ...Option) *Agent {
	t.Helper()
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	return NewDefaultAgent(veloxclient.New(srv.URL, "tok"), opts...)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func twoResults(aID string, aScore int, bID string, bScore int) string {
	ra := `{"video_id":"` + aID + `","title":"` + aID + `","direct_link":"https://youtu.be/` + aID + `","duration":600,"similarity_score":` + itoa(aScore) + `}`
	rb := `{"video_id":"` + bID + `","title":"` + bID + `","direct_link":"https://youtu.be/` + bID + `","duration":600,"similarity_score":` + itoa(bScore) + `}`
	return `{"results":[` + ra + `,` + rb + `]}`
}

// TestResolve_RequireCaptionsDropsCaptionlessAndKeepsTheRest pins the headline
// gate: with RequireCaptions set, a candidate the metadata probe reports as
// caption-less is dropped, while one reported as having captions is kept and
// materialized.
func TestResolve_RequireCaptionsDropsCaptionlessAndKeepsTheRest(t *testing.T) {
	f := &captionFixture{
		search: twoResults("bad", 90, "good", 10),
		infoCaps: func(url string) (bool, bool) {
			return strings.HasSuffix(url, "good"), true
		},
		exists: `{"ok":true,"exists":false}`,
	}
	agent := newCaptionAgent(t, f)

	got, err := agent.Resolve(context.Background(), MaterialRequest{
		ProjectID: "p", SceneID: "s", Description: "interview", Count: 1,
		Sources:     []string{ResolverYouTube},
		Constraints: Constraints{RequireCaptions: true},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(got) != 1 || got[0].SourceRef != "good" || got[0].AssetID != "asset_cap" {
		t.Fatalf("expected the caption-bearing candidate, got %+v", got)
	}
	for _, u := range f.processCalls() {
		if strings.HasSuffix(u, "bad") {
			t.Fatalf("a caption-less candidate must never be extracted, processed=%v", f.processCalls())
		}
	}
}

// TestResolve_RequireCaptionsFailsClosed pins the honest outcome: when every
// candidate is caption-less the agent returns ErrNoMaterial joined with
// ErrNoCaptions (a classified outcome, not a silent fallback).
func TestResolve_RequireCaptionsFailsClosed(t *testing.T) {
	f := &captionFixture{
		search:   twoResults("a", 90, "b", 80),
		infoCaps: func(string) (bool, bool) { return false, true },
		exists:   `{"ok":true,"exists":false}`,
	}
	agent := newCaptionAgent(t, f)

	got, err := agent.Resolve(context.Background(), MaterialRequest{
		Description: "interview", Count: 1,
		Sources:     []string{ResolverYouTube},
		Constraints: Constraints{RequireCaptions: true},
	})
	if !errors.Is(err, ErrNoMaterial) || !errors.Is(err, ErrNoCaptions) {
		t.Fatalf("err = %v, want ErrNoMaterial joined with ErrNoCaptions", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no material, got %+v", got)
	}
	if len(f.processCalls()) != 0 {
		t.Fatalf("nothing may be extracted when the gate rejects everything: %v", f.processCalls())
	}
}

// TestResolve_CaptionProbeBudgetBounded pins the policy guard: probes stop at
// MaxMetadataProbes, and a candidate the budget could not reach fails closed
// rather than passing through unverified.
func TestResolve_CaptionProbeBudgetBounded(t *testing.T) {
	f := &captionFixture{
		search:   twoResults("first", 90, "second", 10),
		infoCaps: func(string) (bool, bool) { return true, true },
		exists:   `{"ok":true,"exists":false}`,
	}
	policy := DefaultPolicy()
	policy.MaxMetadataProbes = 1
	agent := newCaptionAgent(t, f, WithPolicy(policy))

	got, err := agent.Resolve(context.Background(), MaterialRequest{
		Description: "interview", Count: 1,
		Sources:     []string{ResolverYouTube},
		Constraints: Constraints{RequireCaptions: true},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(got) != 1 || got[0].SourceRef != "first" {
		t.Fatalf("expected the first (probed) candidate, got %+v", got)
	}
	for _, u := range f.urls() {
		if strings.HasSuffix(u, "second") {
			t.Fatalf("the probe budget must stop after MaxMetadataProbes; probed=%v", f.urls())
		}
	}
}

// TestResolve_RequireCaptionsExemptsNonProberSources pins that the gate is
// scoped to sources that can answer the probe: a catalog hit satisfies a
// caption-required request without any /api/clips/info probe (the fixture's
// default case fails the test if one is made).
func TestResolve_RequireCaptionsExemptsNonProberSources(t *testing.T) {
	agent := newAgent(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/media/search" {
			t.Errorf("unexpected call: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"items":[{"asset_id":"cat1","source":"youtube","title":"interview","duration_ms":8000, "score":0.9}]}`))
	})

	got, err := agent.Resolve(context.Background(), MaterialRequest{
		Description: "interview", Count: 1,
		Sources:     []string{ResolverCatalog},
		Constraints: Constraints{RequireCaptions: true},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(got) != 1 || got[0].AssetID != "cat1" {
		t.Fatalf("expected the catalog hit, got %+v", got)
	}
}

// TestResolve_ClipExistsHitSkipsExtraction pins T1.3: when the pre-extraction
// dedup probe reports the video is already owned, the resolver returns the
// registered material with zero process calls (0 jobs, 0 downloads).
func TestResolve_ClipExistsHitSkipsExtraction(t *testing.T) {
	var processCalls int
	agent := newAgent(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/media/search":
			_, _ = w.Write([]byte(`{"items":[]}`))
		case "/api/clips/search":
			_, _ = w.Write([]byte(`{"results":[{"video_id":"vid1","title":"Interview","direct_link":"https://youtu.be/vid1","duration":600,"similarity_score":80}]}`))
		case "/api/clips/exists":
			_, _ = w.Write([]byte(`{"ok":true,"exists":true,"clip_id":"yt_vid1"}`))
		case "/api/clips/process":
			processCalls++
			_, _ = w.Write([]byte(`{"job_id":"job_should_not_exist"}`))
		default:
			t.Errorf("unexpected call: %s", r.URL.Path)
		}
	})

	got, err := agent.Resolve(context.Background(), MaterialRequest{
		Description: "interview", Count: 1, Sources: []string{ResolverYouTube},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(got) != 1 || !got[0].Registered || got[0].AssetID != "yt_vid1" {
		t.Fatalf("expected a registered dedup hit, got %+v", got)
	}
	if processCalls != 0 {
		t.Fatalf("a dedup hit must skip extraction, process calls=%d", processCalls)
	}
	if got[0].JobID != "" {
		t.Fatalf("a dedup hit has no job, got %q", got[0].JobID)
	}
}

// TestResolve_ClipExistsProbeFailureIsNonFatal pins the fail-open boundary of
// the dedup probe: when the probe itself fails (server error) extraction still
// proceeds — a dedup optimization must never block acquisition.
func TestResolve_ClipExistsProbeFailureIsNonFatal(t *testing.T) {
	var processCalls int
	agent := newAgent(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/media/search":
			_, _ = w.Write([]byte(`{"items":[]}`))
		case "/api/clips/search":
			_, _ = w.Write([]byte(`{"results":[{"video_id":"vid1","title":"Interview","direct_link":"https://youtu.be/vid1","duration":600,"similarity_score":80}]}`))
		case "/api/clips/exists":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"probe not wired"}`))
		case "/api/clips/info":
			_, _ = w.Write([]byte(`{"id":"vid1","title":"Interview","duration":600}`))
		case "/api/clips/process":
			processCalls++
			_, _ = w.Write([]byte(`{"job_id":"job_yt1","status":"QUEUED"}`))
		case "/api/jobs/job_yt1/full":
			_, _ = w.Write([]byte(`{"id":"job_yt1","status":"SUCCEEDED","result":{"asset_id":"asset_yt1"}}`))
		default:
			t.Errorf("unexpected call: %s", r.URL.Path)
		}
	})

	got, err := agent.Resolve(context.Background(), MaterialRequest{
		Description: "interview", Count: 1, Sources: []string{ResolverYouTube},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(got) != 1 || got[0].AssetID != "asset_yt1" {
		t.Fatalf("a failed probe must not block extraction, got %+v", got)
	}
	if processCalls != 1 {
		t.Fatalf("process calls = %d, want 1", processCalls)
	}
}
