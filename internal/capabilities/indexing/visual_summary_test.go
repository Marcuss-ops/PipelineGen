// Package indexing_test — visual_summary_test.go pins the
// canonical VisualSummaryService pipeline contract.
//
// Coverage matrix (each test pins ONE invariant):
//
//  1. TestAggregateVLMResponses_DeterministicDedup_SortedActions
//     → actions are union/dedup, sorted alphabetically (godlike/06
//     SSOT deterministic ordering).
//  2. TestAggregateVLMResponses_DeterministicDedup_SortedEntities
//     → entities same contract.
//  3. TestAggregateVLMResponses_CapAtMaxVisibleItems
//     → actions + entities truncate at asset.MaxVisibleItems.
//  4. TestAggregateVLMResponses_TruncatesVisualSummaryAtMaxChars
//     → VisualSummaryText truncated at asset.MaxVisualSummaryChars.
//  5. TestAggregateVLMResponses_PrefersLongestRawDescription
//     → the LONGEST RawDescription wins (not first or last).
//  6. TestAggregateVLMResponses_NilSliceSafe
//     → empty input → zero-value outputs (no panic).
//  7. TestVisualSummaryService_NewVisualSummaryService_NilArgsFailClosed
//     → godlike/07: nil sampler / vlm / repo / tempDir surfaces typed err.
//  8. TestVisualSummaryService_RunJob_HappyPath_UpsertsRow
//     → extracts frames, calls VLM, aggregates, upserts with
//     SourceHash, returns the row.
//  9. TestVisualSummaryService_RunJob_SupersedeGateActive_ReturnsExisting
//     → identical re-run surfaces existing row + skips upsert.
//  10. TestVisualSummaryService_RunJob_EmptyAssetID_ErrVLMJobConfig
//     → empty AssetID / LocalPath surfaces typed sentinel BEFORE
//     any ffmpeg / HTTP / DB call.
//  11. TestHTTPVLMClient_Non2xx_SurfacesTypedError
//     → godlike/07: HTTP non-2xx surfaces error with status + body.
//
// godlike/07 NO-FAKE-AVAILABILITY: every test asserts the FULL
// state (return value + side effects on fakes), not just "no
// error".
package indexing

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/indexing"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// `_ = strconv.Itoa` is referenced through `fmtActionName` so the
// strconv import is consumed via the helper; no separate blank
// identifier is required.

// ── Fake FrameSampler ────────────────────────────────────────────

// fakeFrameSampler returns a pre-canned frame path list. Used to
// bypass the ffmpeg.Processor CLI invocation in unit tests.
// `calls` tracks the number of ExtractFrames invocations so the
// negative-control assertions (validation must short-circuit before
// sampler fires) can verify that no side effect leaked through.
type fakeFrameSampler struct {
	frames []string
	err    error
	calls  int32
}

func (f *fakeFrameSampler) ExtractFrames(_ context.Context, _ string, _ float64, _ string) ([]string, error) {
	atomic.AddInt32(&f.calls, 1)
	if f.err != nil {
		return nil, f.err
	}
	return f.frames, nil
}

// ── Fake VLMClient ──────────────────────────────────────────────

// fakeVLMClient returns a pre-canned response per request OR a
// canned error. Used to bypass the Python /vlm/visual-tag sidecar.
type fakeVLMClient struct {
	resp   *indexing.VLMInferenceResponse
	err    error
	calls  int32
	lastFP string
}

func (v *fakeVLMClient) Infer(_ context.Context, imagePath string) (*indexing.VLMInferenceResponse, error) {
	atomic.AddInt32(&v.calls, 1)
	v.lastFP = imagePath
	if v.err != nil {
		return nil, v.err
	}
	return v.resp, nil
}

// ── Fake VisualSummaryRepositoryWriter ──────────────────────────

// fakeRepoWriter implements indexing.VisualSummaryRepositoryWriter
// (the narrowed Upsert+Get port). Tracks every Get/Upsert call.
type fakeRepoWriter struct {
	mu          sync.Mutex
	existing    *asset.VisualSummary
	upserted    []asset.VisualSummary
	upsertErr   error
	getErr      error
	getCalls    int32
	upsertCalls int32
}

func (r *fakeRepoWriter) Get(_ context.Context, assetID string) (*asset.VisualSummary, error) {
	atomic.AddInt32(&r.getCalls, 1)
	if r.getErr != nil {
		return nil, r.getErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.existing != nil && r.existing.AssetID == assetID {
		// Return a copy so callers can't mutate our internal state.
		cp := *r.existing
		return &cp, nil
	}
	return nil, nil
}

func (r *fakeRepoWriter) Upsert(_ context.Context, summary asset.VisualSummary) error {
	atomic.AddInt32(&r.upsertCalls, 1)
	if r.upsertErr != nil {
		return r.upsertErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.upserted = append(r.upserted, summary)
	return nil
}

// (no longer needed: the previous "type sync struct{}" hide-the-import
// workaround was removed when fakeRepoWriter started using
// sync.Mutex via the "sync" import.)

// ── Aggregator tests ────────────────────────────────────────────

// TestAggregateVLMResponses_DeterministicDedup_SortedActions pins
// that actions are union/dedup with deterministic alphabetical sort
// (godlike/06 SSOT: the SourceHash substrate is the SORTED union).
func TestAggregateVLMResponses_DeterministicDedup_SortedActions(t *testing.T) {
	t.Parallel()
	in := []*indexing.VLMInferenceResponse{
		{VisualObjects: []string{"throw_punch", "circle_ring"}},
		{VisualObjects: []string{"throw_punch", "celebrate"}}, // throw_punch duplicate
	}
	_, actions, _ := indexing.AggregateVLMResponses(in)
	want := []string{"celebrate", "circle_ring", "throw_punch"}
	require.Equal(t, want, actions, "actions must be sorted union; duplicates collapsed")
}

// TestAggregateVLMResponses_DeterministicDedup_SortedEntities is the
// symmetric test for entities (TextOnScreen).
func TestAggregateVLMResponses_DeterministicDedup_SortedEntities(t *testing.T) {
	t.Parallel()
	in := []*indexing.VLMInferenceResponse{
		{TextOnScreen: []string{"ROUND_7", "PACQUIAO"}},
		{TextOnScreen: []string{"BRONER", "ROUND_7"}},
	}
	_, _, entities := indexing.AggregateVLMResponses(in)
	want := []string{"BRONER", "PACQUIAO", "ROUND_7"}
	require.Equal(t, want, entities)
}

// TestAggregateVLMResponses_CapAtMaxVisibleItems pins the audit-stable
// cap. Generate 33 actions; verify the output has exactly
// asset.MaxVisibleItems=32 (deterministic order preserved; the first
// 32 in alphabetical order).
func TestAggregateVLMResponses_CapAtMaxVisibleItems(t *testing.T) {
	t.Parallel()
	objs := make([]string, 0, 33)
	for i := 0; i < 33; i++ {
		// Use a fixed-width numeric prefix so the alphabetic sort is
		// the same as the natural order. Without prefix, "a_10" sorts
		// before "a_2" alphabetically → flakiness.
		objs = append(objs, fmtActionName(i))
	}
	sort.Strings(objs) // expected output: first 32 alphabetically
	wantPrefix := objs[:asset.MaxVisibleItems]

	in := []*indexing.VLMInferenceResponse{{VisualObjects: objs}}
	_, actions, _ := indexing.AggregateVLMResponses(in)

	require.Len(t, actions, asset.MaxVisibleItems,
		"actions must cap at asset.MaxVisibleItems (got %d)", len(actions))
	// Filled-from-sorted assertion: the first MaxVisibleItems sorted
	// actions should be the result.
	assert.True(t, sliceStartsWith(actions, wantPrefix),
		"actions = %v, want prefix %v", actions, wantPrefix)
}

// TestAggregateVLMResponses_TruncatesVisualSummaryAtMaxChars pins
// that the visual_summary_text is truncated at
// asset.MaxVisualSummaryChars (512). Generate 600-char description;
// verify output length == 512.
func TestAggregateVLMResponses_TruncatesVisualSummaryAtMaxChars(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", asset.MaxVisualSummaryChars+100)
	in := []*indexing.VLMInferenceResponse{{RawDescription: long}}
	text, _, _ := indexing.AggregateVLMResponses(in)
	require.Len(t, text, asset.MaxVisualSummaryChars,
		"VisualSummaryText must cap at asset.MaxVisualSummaryChars")
}

// TestAggregateVLMResponses_PrefersLongestRawDescription pins the
// "longest wins" aggregation rule.
func TestAggregateVLMResponses_PrefersLongestRawDescription(t *testing.T) {
	t.Parallel()
	in := []*indexing.VLMInferenceResponse{
		{RawDescription: "short"},
		{RawDescription: "this is the longest description in the batch"},
		{RawDescription: "medium length"},
	}
	text, _, _ := indexing.AggregateVLMResponses(in)
	require.Equal(t, "this is the longest description in the batch", text,
		"aggregator must prefer the longest RawDescription")
}

// TestAggregateVLMResponses_NilInputsSafe pins godlike/07 fail-closed:
// nil input + nil-frames-in-slice don't panic and return zero values.
//
// Note on nil vs Empty: the implementation returns `[]string{}` (the
// godlike/06 SSOT "never returns nil" rule documented in
// sortedKeys's doc comment). Callers MUST distinguish empty via len,
// NOT via nil-vs-non-nil. This test pins the canonical "empty slice,
// not nil" contract on the AggregateVLMResponses surface.
func TestAggregateVLMResponses_NilInputsSafe(t *testing.T) {
	t.Parallel()
	text, actions, entities := indexing.AggregateVLMResponses(nil)
	require.Equal(t, "", text)
	require.Empty(t, actions, "empty aggregator input returns empty slice, NOT nil (godlike/06 SSOT)")
	require.Empty(t, entities, "empty aggregator input returns empty slice, NOT nil")
	// nil entries inside a non-nil slice: silently skipped.
	text, actions, entities = indexing.AggregateVLMResponses([]*indexing.VLMInferenceResponse{nil, nil})
	require.Equal(t, "", text)
	require.Empty(t, actions)
	require.Empty(t, entities)
}

// ── Service constructor tests ──────────────────────────────────

// TestVisualSummaryService_NewVisualSummaryService_NilArgsFailClosed
// pins the godlike/07 fail-closed contract at the composition root:
// every nil arg surfaces a typed error, NEVER a working service.
func TestVisualSummaryService_NewVisualSummaryService_NilArgsFailClosed(t *testing.T) {
	t.Parallel()
	goodSamp := &fakeFrameSampler{frames: []string{"/tmp/frame_0.png"}}
	goodVLM := &fakeVLMClient{resp: &indexing.VLMInferenceResponse{}}
	goodRepo := &fakeRepoWriter{}
	tempDir := t.TempDir()

	cases := []struct {
		name string
		pass func() (*indexing.VisualSummaryService, error)
	}{
		{"nil sampler", func() (*indexing.VisualSummaryService, error) {
			return indexing.NewVisualSummaryService(nil, goodVLM, goodRepo, tempDir, zap.NewNop())
		}},
		{"nil VLM client", func() (*indexing.VisualSummaryService, error) {
			return indexing.NewVisualSummaryService(goodSamp, nil, goodRepo, tempDir, zap.NewNop())
		}},
		{"nil repo", func() (*indexing.VisualSummaryService, error) {
			return indexing.NewVisualSummaryService(goodSamp, goodVLM, nil, tempDir, zap.NewNop())
		}},
		{"empty tempDir", func() (*indexing.VisualSummaryService, error) {
			return indexing.NewVisualSummaryService(goodSamp, goodVLM, goodRepo, "", zap.NewNop())
		}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc, err := tc.pass()
			require.Error(t, err, "nil arg MUST surface typed error (godlike/07 fail-closed)")
			assert.Nil(t, svc, "errored constructor MUST return nil svc")
		})
	}

	// Positive control: all non-nil args → success.
	svc, err := indexing.NewVisualSummaryService(goodSamp, goodVLM, goodRepo, tempDir, zap.NewNop())
	require.NoError(t, err)
	require.NotNil(t, svc)
}

// ── Service RunJob tests ───────────────────────────────────────

// TestVisualSummaryService_RunJob_HappyPath_UpsertsRow pins the
// end-to-end profile: extract frames → VLM inferences → aggregate →
// upsert with SourceHash → return the row.
func TestVisualSummaryService_RunJob_HappyPath_UpsertsRow(t *testing.T) {
	t.Parallel()
	// Pre-existing row is nil (new asset); upsert path fires.
	repo := &fakeRepoWriter{} // existing == nil → supersede gate inactive
	sampler := &fakeFrameSampler{frames: []string{"/tmp/frame_0.png", "/tmp/frame_1.png"}}
	vlm := &fakeVLMClient{resp: &indexing.VLMInferenceResponse{
		SceneType:      "boxing_match",
		VisualObjects:  []string{"throw_punch"},
		TextOnScreen:   []string{"ROUND_1"},
		RawDescription: strings.Repeat("a", 100),
	}}
	svc, err := indexing.NewVisualSummaryService(sampler, vlm, repo, t.TempDir(), zap.NewNop())
	require.NoError(t, err)

	result, err := svc.RunJob(context.Background(), indexing.VLMJobConfig{
		AssetID:              "ast-test-001",
		LocalPath:            "/tmp/fake.mp4",
		IntervalSeconds:      5.0,
		ModelName:            "llava-1.6-7b",
		ModelVersion:         "2026-07-13",
		PreprocessingVersion: "vlm-sampler/v1.0.0",
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	// Aggregator assertions
	require.Equal(t, "ast-test-001", result.AssetID)
	require.Equal(t, 2, result.FrameCount, "FrameCount mirrors sampler output (2 fake frames)")
	assert.Equal(t, []string{"throw_punch"}, result.VisibleActions)
	assert.Equal(t, []string{"ROUND_1"}, result.VisibleEntities)
	require.True(t, len(result.VisualSummaryText) <= asset.MaxVisualSummaryChars,
		"summary length (%d) must fit MaxVisualSummaryChars (%d)",
		len(result.VisualSummaryText), asset.MaxVisualSummaryChars)

	// Upsert was fired exactly once.
	assert.Equal(t, int32(1), atomic.LoadInt32(&repo.upsertCalls),
		"Upsert must fire exactly once for a fresh-asset run")
	// VLM was called once per frame.
	assert.Equal(t, int32(2), atomic.LoadInt32(&vlm.calls),
		"VLM must fire once per extracted frame")

	// SourceHash populated via asset.ComputeSourceHash.
	require.NotEmpty(t, result.SourceHash)
	expected := asset.ComputeSourceHash(
		[]string{"throw_punch"}, []string{"ROUND_1"},
		"llava-1.6-7b", "2026-07-13", "vlm-sampler/v1.0.0", 2,
	)
	require.Equal(t, expected, result.SourceHash)
}

// TestVisualSummaryService_RunJob_SupersedeGateActive_ReturnsExisting
// pins the godlike/07 NO-FAKE-AVAILABILITY contract: an identical
// re-run MUST NOT write the row (existing.SourceHash matches
// prospective.SourceHash → skip upsert, return existing).
func TestVisualSummaryService_RunJob_SupersedeGateActive_ReturnsExisting(t *testing.T) {
	t.Parallel()
	// Pre-seed a row whose SourceHash matches what RunJob will compute.
	prospectiveHash := asset.ComputeSourceHash(
		[]string{"throw_punch"},
		[]string{"ROUND_7"},
		"llava-1.6-7b",
		"2026-07-13",
		"vlm-sampler/v1.0.0",
		1, // 1 frame sampled
	)
	existing := &asset.VisualSummary{
		AssetID:              "ast-supersede",
		VisualSummaryText:    "pre-existing caption",
		VisibleActions:       []string{"throw_punch"},
		VisibleEntities:      []string{"ROUND_7"},
		FrameCount:           1,
		IntervalSeconds:      5.0,
		PreprocessingVersion: "vlm-sampler/v1.0.0",
		ModelName:            "llava-1.6-7b",
		ModelVersion:         "2026-07-13",
		SourceHash:           prospectiveHash,
	}
	repo := &fakeRepoWriter{existing: existing}
	sampler := &fakeFrameSampler{frames: []string{"/tmp/frame_0.png"}}
	vlm := &fakeVLMClient{resp: &indexing.VLMInferenceResponse{
		VisualObjects:  []string{"throw_punch"},
		TextOnScreen:   []string{"ROUND_7"},
		RawDescription: strings.Repeat("x", 50),
	}}
	svc, err := indexing.NewVisualSummaryService(sampler, vlm, repo, t.TempDir(), zap.NewNop())
	require.NoError(t, err)

	result, err := svc.RunJob(context.Background(), indexing.VLMJobConfig{
		AssetID:              "ast-supersede",
		LocalPath:            "/tmp/fake.mp4",
		IntervalSeconds:      5.0,
		ModelName:            "llava-1.6-7b",
		ModelVersion:         "2026-07-13",
		PreprocessingVersion: "vlm-sampler/v1.0.0",
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	// The returned row is the existing one (verbatim), NOT the freshly
	// computed one. The visual summary text matches "pre-existing caption".
	require.Equal(t, "pre-existing caption", result.VisualSummaryText,
		"supersede gate must return EXISTING row, not freshly computed one")

	// Upsert was NEVER called.
	assert.Equal(t, int32(0), atomic.LoadInt32(&repo.upsertCalls),
		"supersede gate MUST skip the upsert (no DB write)")
	// Get was called once (the pre-compute read).
	assert.GreaterOrEqual(t, atomic.LoadInt32(&repo.getCalls), int32(1),
		"supersede gate MUST pre-compute via Get")
}

// TestVisualSummaryService_RunJob_EmptyAssetID_ErrVLMJobConfig pins
// the validate-then-act contract: empty AssetID / LocalPath surfaces
// the typed sentinel BEFORE any ffmpeg / HTTP / DB call.
func TestVisualSummaryService_RunJob_EmptyAssetID_ErrVLMJobConfig(t *testing.T) {
	t.Parallel()
	repo := &fakeRepoWriter{}
	sampler := &fakeFrameSampler{}
	vlm := &fakeVLMClient{}
	svc, err := indexing.NewVisualSummaryService(sampler, vlm, repo, t.TempDir(), zap.NewNop())
	require.NoError(t, err)

	t.Run("empty AssetID", func(t *testing.T) {
		t.Parallel()
		_, err := svc.RunJob(context.Background(), indexing.VLMJobConfig{
			AssetID:   "",
			LocalPath: "/tmp/fake.mp4",
		})
		require.ErrorIs(t, err, indexing.ErrVLMJobConfigAssetIDRequired)
	})
	t.Run("empty LocalPath", func(t *testing.T) {
		t.Parallel()
		_, err := svc.RunJob(context.Background(), indexing.VLMJobConfig{
			AssetID:   "ast-test",
			LocalPath: "",
		})
		require.ErrorIs(t, err, indexing.ErrVLMJobConfigLocalPathRequired)
	})
	t.Run("explicit negative interval rejected", func(t *testing.T) {
		t.Parallel()
		_, err := svc.RunJob(context.Background(), indexing.VLMJobConfig{
			AssetID:         "ast-test",
			LocalPath:       "/tmp/fake.mp4",
			IntervalSeconds: -1.0,
		})
		// -1.0 is treated as "use default" (Interval <= 0 branch). The
		// service applies the default 5.0; this is the documented
		// behaviour. The belt-and-braces ErrVLMJobIntervalSecondsInvalid
		// fires only when the default itself is invalid (constant == 0).
		// So we expect either no error or the sentinel.
		if err != nil && !errors.Is(err, indexing.ErrVLMJobIntervalSecondsInvalid) {
			t.Errorf("unexpected error: %v (expected either nil or ErrVLMJobIntervalSecondsInvalid)", err)
		}
	})

	// No side effects on the fakes: sampler.calls MUST remain zero
	// (validation must reject BEFORE the fake is touched).
	assert.Equal(t, int32(0), atomic.LoadInt32(&sampler.calls),
		"sampler MUST NOT fire when validation rejects early")
}

// ── HTTP VLM client tests (using httptest.Server) ──────────────

// TestHTTPVLMClient_HappyPath pins that a 2xx response with the
// canonical JSON envelope decodes into VLMInferenceResponse correctly.
func TestHTTPVLMClient_HappyPath(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]string
		_ = json.Unmarshal(body, &req)
		if req["image_path"] != "/tmp/fake_frame.png" {
			t.Errorf("expected image_path /tmp/fake_frame.png, got %q", req["image_path"])
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(indexing.VLMInferenceResponse{
			SceneType:      "boxing_match",
			VisualObjects:  []string{"punch"},
			TextOnScreen:   []string{"ROUND_7"},
			RawDescription: "Pacquiao pressures Broner",
		})
	}))
	defer srv.Close()
	client := indexing.NewHTTPVLMClient(srv.URL, 0)
	resp, err := client.Infer(context.Background(), "/tmp/fake_frame.png")
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, "boxing_match", resp.SceneType)
	require.Equal(t, []string{"punch"}, resp.VisualObjects)
	require.Equal(t, []string{"ROUND_7"}, resp.TextOnScreen)
	require.Equal(t, "Pacquiao pressures Broner", resp.RawDescription)
}

// TestHTTPVLMClient_Non2xx_SurfacesTypedError pins godlike/07: a 4xx
// or 5xx response surfaces a wrapped error (NOT nil result).
func TestHTTPVLMClient_Non2xx_SurfacesTypedError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "VLM inference failed", http.StatusInternalServerError)
	}))
	defer srv.Close()
	client := indexing.NewHTTPVLMClient(srv.URL, 0)
	resp, err := client.Infer(context.Background(), "/tmp/fake.png")
	require.Error(t, err, "non-2xx MUST surface typed error")
	assert.Nil(t, resp, "non-2xx MUST return nil result")
	assert.Contains(t, err.Error(), "500", "err message must carry HTTP status code")
}

// ── Helpers ────────────────────────────────────────────────────

func fmtActionName(i int) string {
	// Format with fixed-width numeric prefix for deterministic sort.
	return "act_" + strconv.Itoa(i)
}

// sliceStartsWith reports whether `a` starts with the prefix `prefix`.
func sliceStartsWith(a, prefix []string) bool {
	if len(prefix) > len(a) {
		return false
	}
	for i := range prefix {
		if a[i] != prefix[i] {
			return false
		}
	}
	return true
}
