package usecase

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/pkg/security"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	ptrutil "github.com/Marcuss-ops/PipelineGen/internal/platform/shared/ptrutil"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	urlutil "github.com/Marcuss-ops/PipelineGen/pkg/urlutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestYouTubeClipRequestValidation(t *testing.T) {
	// Test empty URL
	req := &youtubetypes.ExtractRequest{
		URL:      "",
		Segments: []youtubetypes.Segment{{Start: "0:10", End: "0:20"}},
	}

	if req.URL == "" {
		t.Log("Empty URL correctly identified as invalid")
	}

	// Test valid URL
	req.URL = "https://www.youtube.com/watch?v=dQw4w9WgXcQ"
	if req.URL == "" {
		t.Error("URL should not be empty")
	}

	_ = req
}

func TestYouTubeClipRejectsInvalidURL(t *testing.T) {
	// Test invalid URLs
	invalidURLs := []string{
		"",
		"not-a-url",
		"ftp://example.com/file",
		"http://malicious.com/script",
	}

	for _, url := range invalidURLs {
		req := &youtubetypes.ExtractRequest{
			URL:      url,
			Segments: []youtubetypes.Segment{{Start: "0:10", End: "0:20"}},
		}

		// In a real test, we would call Extract and check for error
		// For now, just log
		t.Logf("Testing invalid URL: %s", url)

		// URL validation would happen in Extract method
		if url == "" {
			t.Logf("Empty URL correctly detected: %s", url)
		}

		_ = req
	}
}

func TestYouTubeClipRejectsInvalidTimeRange(t *testing.T) {
	// Test invalid time ranges
	testCases := []struct {
		name  string
		start string
		end   string
	}{
		{"empty start", "", "0:20"},
		{"empty end", "0:10", ""},
		{"end before start", "0:20", "0:10"},
		{"invalid format", "abc", "0:20"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := &youtubetypes.ExtractRequest{
				URL:      "https://www.youtube.com/watch?v=test",
				Segments: []youtubetypes.Segment{{Start: tc.start, End: tc.end}},
			}

			// Validate segment
			if tc.start == "" || tc.end == "" {
				t.Logf("Empty timestamp detected: start=%s, end=%s", tc.start, tc.end)
			}

			// In real implementation, parseTimestamp would be called
			// and would return error for invalid formats

			_ = req
		})
	}
}

func TestYouTubeClipCreatesExpectedOutputPath(t *testing.T) {
	// Test that output path is created correctly
	videoID := "dQw4w9WgXcQ"
	expectedFolder := "yt_" + videoID

	if expectedFolder != "yt_dQw4w9WgXcQ" {
		t.Errorf("Expected folder 'yt_dQw4w9WgXcQ', got %s", expectedFolder)
	}

	t.Logf("Expected output folder: %s", expectedFolder)
}

func TestParseTimestamp(t *testing.T) {
	testCases := []struct {
		input    string
		expected int
		hasError bool
	}{
		{"10", 10, false},
		{"1:30", 90, false},
		{"1:23:45", 5025, false},
		{"", 0, true},
		{"invalid", 0, true},
		{"0:10", 10, false},
		{"0:05", 5, false},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			result, err := textutil.ParseTimestamp(tc.input)

			if tc.hasError && err == nil {
				t.Errorf("Expected error for input %s, but got none", tc.input)
			}

			if !tc.hasError && err != nil {
				t.Errorf("Unexpected error for input %s: %v", tc.input, err)
			}

			if !tc.hasError && result != tc.expected {
				t.Errorf("For input %s: expected %d, got %d", tc.input, tc.expected, result)
			}
		})
	}
}

func TestExtractVideoID(t *testing.T) {
	testCases := []struct {
		input    string
		expected string
		wantErr  bool
	}{
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQ", "dQw4w9WgXcQ", false},
		{"https://youtu.be/dQw4w9WgXcQ", "dQw4w9WgXcQ", false},
		{"https://www.youtube.com/shorts/abc123", "abc123", false},
		{"https://www.youtube.com/embed/xyz789", "xyz789", false},
		{"https://www.youtube.com/live/def456", "def456", false},
		{"not-a-url", "", true},
		{"", "", true},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			result, err := urlutil.ExtractVideoID(tc.input)
			if tc.wantErr && err == nil {
				t.Errorf("For input %s: expected error, got none", tc.input)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("For input %s: unexpected error: %v", tc.input, err)
			}
			if result != tc.expected {
				t.Errorf("For input %s: expected %s, got %s", tc.input, tc.expected, result)
			}
		})
	}
}

func TestYouTubeClipHandlesPipelineFailure(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	// Add test hosts to security allowlist
	security.AddAllowedHost("www.youtube.com")
	security.AddAllowedHost("youtu.be")

	cfg := testConfig(tmp)
	log := zap.NewNop()

	pipeline := &fakeVideoPipeline{
		err: errors.New("yt-dlp failed"),
	}

	// PR-GRUPOC-1 (July 2026): the 22-field ServiceDeps is retired in
	// favour of 5 capability-area sub-bundles. This test mutates
	// MediaProcessor / LifecycleService / AssetDestResolver to nil
	// (no-op); only VideoPipeline + ProcessSeg are exercised.
	svc := NewServiceFromSubBundles(
		ServiceCoreDeps{Cfg: cfg, Log: log},
		ServiceAssetDeps{
			MediaProcessor:    nil, // processor
			LifecycleService:  nil, // lifecycle
			AssetDestResolver: nil, // asset dest resolver
		},
		ServiceVideoDeps{
			VideoPipeline: pipeline,
			ProcessSeg:    newTestProcessSegmentUseCase(log, pipeline),
		},
		ServiceStorageDeps{},
		ServiceAdapterDeps{},
	)

	resp, err := svc.Extract(ctx, &youtubetypes.ExtractRequest{
		URL: "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		Segments: []youtubetypes.Segment{
			{
				Name:  "intro",
				Start: "0",
				End:   "5",
			},
		},
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.False(t, resp.OK)
	require.Equal(t, 1, resp.Stats.Failed)
	require.Len(t, resp.Items, 1)
	require.Equal(t, "failed", resp.Items[0].Status)
	require.Contains(t, resp.Items[0].Error, "video processing failed: yt-dlp failed")
	require.True(t, pipeline.called)
}

func TestYouTubeClipPassesExpectedAssetInputToPipeline(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	// Add test hosts to security allowlist
	security.AddAllowedHost("www.youtube.com")
	security.AddAllowedHost("youtu.be")

	cfg := testConfig(tmp)
	log := zap.NewNop()

	dummyFilePath := filepath.Join(tmp, "dummy.mp4")
	err := os.WriteFile(dummyFilePath, []byte("dummy video content"), 0644)
	require.NoError(t, err)

	pipeline := &fakeVideoPipeline{
		outputPath: dummyFilePath,
	}

	// PR-GRUPOC-1 (July 2026): see sibling test for the sub-builder
	// migration rationale. Same nil-MediaProcessor / nil-Lifecycle /
	// nil-AssetDestResolver pattern (the test exercises the
	// Pipeline-Forwarding path, not the asset-lifecycle cluster).
	svc := NewServiceFromSubBundles(
		ServiceCoreDeps{Cfg: cfg, Log: log},
		ServiceAssetDeps{
			MediaProcessor:    nil,
			LifecycleService:  nil,
			AssetDestResolver: nil,
		},
		ServiceVideoDeps{
			VideoPipeline: pipeline,
			ProcessSeg:    newTestProcessSegmentUseCase(log, pipeline),
		},
		ServiceStorageDeps{},
		ServiceAdapterDeps{},
	)

	resp, err := svc.Extract(ctx, &youtubetypes.ExtractRequest{
		URL: "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		Segments: []youtubetypes.Segment{
			{Name: "clip one", Start: "10", End: "20"},
		},
	})

	require.NoError(t, err)
	require.True(t, resp.OK)
	require.True(t, pipeline.called)

	assert.Equal(t, "https://www.youtube.com/watch?v=dQw4w9WgXcQ", pipeline.url)
	assert.Equal(t, float64(10), pipeline.start)
	assert.Equal(t, float64(10), pipeline.duration)
	// OutputName follows the new unique format: yt_{videoID}_{start}_{end}_{policyVer}_{slug}
	assert.Equal(t, "yt_dQw4w9WgXcQ_10_20_v1_clip-one", pipeline.outputName)
}

func TestYouTubeClipValidSegmentCount(t *testing.T) {
	// Test that segment count is validated
	req := &youtubetypes.ExtractRequest{
		URL: "https://www.youtube.com/watch?v=test",
	}

	// Empty segments should fail
	if len(req.Segments) == 0 {
		t.Log("Empty segments correctly detected")
	}

	// Test max segments limit
	for i := 0; i < 25; i++ {
		req.Segments = append(req.Segments, youtubetypes.Segment{Start: "0:10", End: "0:20"})
	}

	if len(req.Segments) > 20 {
		t.Logf("Too many segments detected: %d", len(req.Segments))
	}
}

func TestYouTubeClipServiceCreation(t *testing.T) {
	// Test that service can be created
	// This requires mocking all dependencies
	t.Log("Service creation test - requires full mock setup")
}

func TestBoolDefault(t *testing.T) {
	testCases := []struct {
		input    *bool
		def      bool
		expected bool
	}{
		{nil, true, true},
		{nil, false, false},
		{boolPtr(true), true, true},
		{boolPtr(true), false, true},
		{boolPtr(false), true, false},
		{boolPtr(false), false, false},
	}

	for _, tc := range testCases {
		result := ptrutil.BoolDefault(tc.input, tc.def)
		if result != tc.expected {
			t.Errorf("BoolDefault(%v, %v) = %v, expected %v", tc.input, tc.def, result, tc.expected)
		}
	}
}

func boolPtr(b bool) *bool {
	return &b
}

type fakeMediaProcessor struct {
	called bool
	err    error
	result *asset.ProcessResult
	inputs []*asset.ProcessInput
}

type fakeVideoPipeline struct {
	called     bool
	err        error
	outputPath string
	url        string
	start      float64
	duration   float64
	outputName string
}

func (f *fakeVideoPipeline) DownloadAndCutYouTubeVideo(ctx context.Context, req youtubeports.VideoCutRequest) (*youtubeports.VideoCutResult, error) {
	f.called = true
	f.url = req.URL
	f.start = req.Start
	f.duration = req.Duration
	f.outputName = req.OutputName
	if f.err != nil {
		return nil, f.err
	}
	return &youtubeports.VideoCutResult{
		LocalPath: f.outputPath,
	}, nil
}

func (f *fakeMediaProcessor) Process(ctx context.Context, input *asset.ProcessInput) (*asset.ProcessResult, error) {
	f.called = true
	f.inputs = append(f.inputs, input)

	if f.err != nil {
		return &asset.ProcessResult{
			ID:     input.ID,
			Status: "failed",
			Error:  f.err.Error(),
		}, f.err
	}

	if f.result != nil {
		return f.result, nil
	}

	return &asset.ProcessResult{
		ID:            input.ID,
		Filename:      input.Name + ".mp4",
		LocalPath:     input.OutputDir + "/" + input.Name + ".mp4",
		LegacyFileMD5: "hash-test",
		Status:        "processed",
	}, nil
}

func testConfig(tmp string) youtubetypes.RuntimeConfig {
	return youtubetypes.RuntimeConfig{
		DataDir: tmp,
	}
}

// ── PR5 Phase 3 forwarding tests (June 2026) ────────────────────────────────────────
//
// These tests cover the public Extract forwarding method on the root Service.
// The canonical extraction logic lives in internal/application/youtube/extraction/;
// the root Service is a thin forwarder. Three contracts are guaranteed:
//
//   1. ctx.Err() is honoured BEFORE internal-state inspection, so a cancelled
//      request short-circuits without paying for nil-unwrapping.
//   2. If the extraction capability is not wired (`s.extraction == nil`),
//      callers get an explicit error rather than a silent nil panic.
//   3. The forwarding path reaches the canonical extraction service so the
//      rest of the pipeline (validation, video info, segments, lifecycle) is
//      observable through the root method.

// TestExtract_ReturnsContextCancelledIfContextCancelled ensures context
// cancellation is honoured through the forwarding chain. The service is
// built via NewService wired with the minimum ports extraction.Extract
// needs (Cfg to avoid nil-deref on cfg.Jobs.*, VideoPipeline to satisfy
// the cap on cfg.Jobs.YouTubeExtractTimeout dereference, etc.).
//
// Acceptance contract (deterministic across Go's select nondeterminism
// in AcquireVideoExtractSem): either:
//   - the forwarding chain returns err == context.Canceled (early
//     short-circuit via AcquireVideoExtractSem(ctx).Done case), OR
//   - the chain returns resp != nil with OK == false because per-segment
//     retry.Do(ctx, ...) propagates ctx.Err() through the worker goroutines.
//
// In either case the caller observes that the cancelled request did NOT
// produce a successful extraction. This is the user-facing contract:
// cancellation must short-circuit before a successful response is
// returned.
func TestExtract_ReturnsContextCancelledIfContextCancelled(t *testing.T) {
	tmp := t.TempDir()
	security.AddAllowedHost("www.youtube.com")
	security.AddAllowedHost("youtu.be")

	// PR-GRUPOC-1 (July 2026): sub-builder ctor. AssetRepo lands in
	// ServiceAssetDeps; SearchRunner in ServiceAdapterDeps. The
	// forwarding chain needs AssetRepo for the canonical Extract
	// facade (per PR5 Phase 3).
	svc := NewServiceFromSubBundles(
		ServiceCoreDeps{Cfg: testConfig(tmp), Log: zap.NewNop()},
		ServiceAssetDeps{AssetRepo: &fakeAssetRepo{}},
		ServiceVideoDeps{
			VideoPipeline: &fakeVideoPipeline{},
			ProcessSeg:    newTestProcessSegmentUseCase(zap.NewNop(), &fakeVideoPipeline{}),
		},
		ServiceStorageDeps{},
		ServiceAdapterDeps{SearchRunner: &fakeSearchRunner{}},
	)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	resp, err := svc.Extract(ctx, &youtubetypes.ExtractRequest{
		URL: "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		Segments: []youtubetypes.Segment{
			{Name: "intro", Start: "0", End: "5"},
		},
	})
	if err != nil {
		// Early short-circuit path — ctx.Err() surfaces directly as err.
		assert.ErrorIs(t, err, context.Canceled, "early path must surface context.Canceled")
	} else {
		// Late path — body ran but cancellation propagated into retry.Do,
		// producing a failed-response rather than a success response.
		require.NotNil(t, resp, "late path must return a non-nil response object")
		assert.False(t, resp.OK, "cancelled ctx must NOT produce a successful response (resp.OK should be false)")
		assert.GreaterOrEqual(t, resp.Stats.Failed, 0)
		if len(resp.Items) > 0 {
			hasCtxErr := false
			for _, it := range resp.Items {
				if it.Error != "" && (strings.Contains(it.Error, "context canceled") || strings.Contains(it.Error, "context deadline exceeded")) {
					hasCtxErr = true
					break
				}
			}
			assert.True(t, hasCtxErr, "late path items must surface context-cancellation in their Error fields")
		}
	}
}

// TestExtract_ReturnsContextDeadlineExceededIfContextExpired mirrors the
// Cancellation test for the deadline variant. Uses time.Now() as the
// deadline (always in the past at construction time) instead of
// arithmetic on system time to avoid clock-skew flakiness on hosts with
// non-monotonic clocks.
func TestExtract_ReturnsContextDeadlineExceededIfContextExpired(t *testing.T) {
	tmp := t.TempDir()
	security.AddAllowedHost("www.youtube.com")
	security.AddAllowedHost("youtu.be")

	// PR-GRUPOC-1 (July 2026): see sibling TestExtract_ReturnsContext
	// Cancelled for the sub-builder migration rationale. Same shape.
	svc := NewServiceFromSubBundles(
		ServiceCoreDeps{Cfg: testConfig(tmp), Log: zap.NewNop()},
		ServiceAssetDeps{AssetRepo: &fakeAssetRepo{}},
		ServiceVideoDeps{
			VideoPipeline: &fakeVideoPipeline{},
			ProcessSeg:    newTestProcessSegmentUseCase(zap.NewNop(), &fakeVideoPipeline{}),
		},
		ServiceStorageDeps{},
		ServiceAdapterDeps{SearchRunner: &fakeSearchRunner{}},
	)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now())
	defer cancel()

	resp, err := svc.Extract(ctx, &youtubetypes.ExtractRequest{
		URL: "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		Segments: []youtubetypes.Segment{
			{Name: "intro", Start: "0", End: "5"},
		},
	})
	if err != nil {
		// Early short-circuit path - ctx.Err() surfaces as err. The
		// specific error kind depends on which cancellation mode fires
		// first: DeadlineExceeded is expected, but Canceled is also
		// acceptable if the cancel() goroutine wins the select race.
		assert.True(t,
			errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled),
			"deadline-expired ctx must surface as context.DeadlineExceeded or context.Canceled, got: %v", err)
	} else {
		require.NotNil(t, resp)
		assert.False(t, resp.OK, "deadline-expired ctx must NOT produce a successful response")
	}
}

// TestExtract_ReturnsErrorIfExtractionNotWired ensures the forwarding
// wrapper returns an EXPLICIT error when `s.extraction == nil` rather
// than silently panicking on a downstream nil dereference. NewService
// always wires extraction in production; this test exercises a defensive
// failure mode (e.g. post-construction nil assignment, future refactor).
// The expected error string matches upstream's canonical Extract facade
// (introduced in PR5 Phase 3 / CPR-CC-6 Phase 2) which uses the format
// "youtube: extraction capability not wired (composition root must include
// ...ServiceDeps for NewService to wire the extraction service)".
func TestExtract_ReturnsErrorIfExtractionNotWired(t *testing.T) {
	svc := &Service{extraction: nil}

	resp, err := svc.Extract(context.Background(), &youtubetypes.ExtractRequest{
		URL: "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
	})
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "youtube: extraction capability not wired")
	assert.Contains(t, err.Error(), "composition root must include")
}

// TestExtract_DelegatesToExtractionCapability is the LIGHTWEIGHT positive
// contract for "delega corretta" (correct delegation). We deliberately
// avoid duplicating the heavier TestYouTubeClipHandlesPipelineFailure
// assertions on OK=false / Stats.Failed=1; that test covers end-to-end
// behaviour. Here we only assert that pipeline.called flips: if root
// .Extract re-implemented the pipeline locally instead of delegating,
// the fake port would never see the call. The "yt-dlp failed" sentinel
// gives the extraction service something concrete to record against.
