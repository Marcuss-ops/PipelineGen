// Package scriptgeneration — final_job_demolition_benchmark_test.go
//
// BenchmarkFinalJobDemolition quantifies the final-job work removed by the
// 2026-11-16 demolition. It is an operation ledger as well as a latency
// benchmark: every sub-benchmark reports the operations it performs, so the
// saved counts are visible next to the measured cost.
//
// The pre-demolition code paths no longer exist (that is the point of the
// demolition), so the benchmark prices each removed operation with the
// surviving primitive that owned it. The saved totals are therefore measured:
//
//   - retry: a SUCCEEDED receipt skips the whole payload build;
//   - stock: the Producer no longer lists the folder or resolves the selected
//     stock files before the handoff;
//   - editorial: one v2 request replaces the retired duplicate Go decision;
//   - intro: only the bound 5s/120-frame window is planned, not the 60s file.
//
// The overlay-compile measurement is a LOWER BOUND for the retired decision:
// it prices the v2 request projection and JSON envelope, which the duplicate
// decision paid in addition to its own selection work. The intro measurement
// counts remote encoder frames; it intentionally claims no local latency for
// GPU work the benchmark cannot execute.
//
// Run:
//
//	go test ./internal/capabilities/scripts -run '^$' \
//	  -bench BenchmarkFinalJobDemolition -benchtime=10x -count=1
package scriptgeneration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

const (
	demolitionStockScenes       = 9
	demolitionIntroClips        = 3
	demolitionIntroSeconds      = 5
	demolitionIntroSourceSecs   = 60
	demolitionFPS               = 24
	demolitionSceneSeconds      = 5
	demolitionIntroSourceFrames = demolitionIntroSourceSecs * demolitionFPS
	demolitionIntroBoundFrames  = demolitionIntroSeconds * demolitionFPS
)

// demolitionScenario is one representative final job: a 15s three-clip intro
// followed by nine 5s stock scenes, with one certified overlay item per stock
// scene and a 60s certified final-audio master.
type demolitionScenario struct {
	req            GenerateRequest
	result         *GenerateResult
	timelineBounds map[string][2]int64
}

func newDemolitionScenario() demolitionScenario {
	introDurationUS := int64(demolitionIntroClips * demolitionIntroSeconds * 1_000_000)
	sceneDurationUS := int64(demolitionSceneSeconds * 1_000_000)

	introClips := make([]*ClipReference, 0, demolitionIntroClips)
	for i := 0; i < demolitionIntroClips; i++ {
		id := fmt.Sprintf("intro-source-clip-%d", i+1)
		introClips = append(introClips, &ClipReference{ID: id, SourceInMS: 0, SourceOutMS: demolitionIntroSeconds * 1000})
	}

	scenes := []Scene{{
		ID: "scene-intro", Index: 0, ExecutionMode: scriptpkg.SceneExecutionFixedMedia,
		Clips: introClips, Text: map[Language]string{"en": "INTRO"},
	}}
	segments := []capabilityaudio.TimelineSegment{{
		ID: "scene-intro", Index: 0, TimelineStartUS: 0, DurationUS: introDurationUS, FixedMedia: true,
	}}
	localized := make([]LocalizedRenderResult, 0, demolitionIntroClips)
	for i, clip := range introClips {
		localized = append(localized, LocalizedRenderResult{
			SceneID: "scene-intro", Language: "en", ClipID: clip.ID, Status: "UPLOADED",
			AssetID: fmt.Sprintf("intro-render-%d", i+1), DriveFileID: fmt.Sprintf("intro-render-drive-%d", i+1),
			SHA256: strings.Repeat(fmt.Sprintf("%x", i+1), 64), DurationMS: demolitionIntroSeconds * 1000,
		})
	}

	items := make([]capmodel.OverlayItem, 0, demolitionStockScenes)
	offsetUS := introDurationUS
	for i := 0; i < demolitionStockScenes; i++ {
		id := fmt.Sprintf("scene-stock-%d", i)
		scenes = append(scenes, Scene{
			ID: id, Index: i + 1,
			Text:  map[Language]string{"en": fmt.Sprintf("Stock scene %d narration.", i)},
			Stock: concreteStockBinding("drive-asset-5000", 0, demolitionSceneSeconds*1000),
		})
		segments = append(segments, capabilityaudio.TimelineSegment{
			ID: id, Index: i + 1, TimelineStartUS: offsetUS, DurationUS: sceneDurationUS,
		})
		offsetUS += sceneDurationUS
		items = append(items, capmodel.OverlayItem{
			ID: fmt.Sprintf("overlay-%d", i), SceneID: id, Kind: "text_phrase", TemplateID: "phrase",
			Text: fmt.Sprintf("Stock scene %d", i), StartUS: offsetUS - sceneDurationUS, DurationUS: sceneDurationUS,
		})
	}

	result := &GenerateResult{
		SourceLanguage: "en",

		Scenes:           scenes,
		LocalizedRenders: localized,
		OverlayPlan: &capmodel.OverlayPlan{
			SchemaVersion: capmodel.SchemaVersionPlan, PlanID: "bench-plan", VideoID: "bench-video",
			Width: 1920, Height: 1080, FPSNum: demolitionFPS, FPSDen: 1,
			Items: items,
		},
		OverlayRender: manifestOverlayRender(items, &RenderArtifact{
			ID: "bench-timeline", DriveFileID: "bench-timeline-drive", SHA256: strings.Repeat("d", 64),
			SizeBytes: 100, DurationUS: 60_000_000, FrameCount: 60_000_000 * demolitionFPS / 1_000_000, CopyEligible: true,
		}), AudioOutput: AudioOutput{CanonicalTimeline: &capabilityaudio.CanonicalTimeline{
			DurationUS: introDurationUS + int64(demolitionStockScenes)*sceneDurationUS,
			Segments:   segments,
		},
			FinalAudio: certifiedFinalAudio(60_000)},
	}
	bounds := make(map[string][2]int64, demolitionStockScenes)
	for _, segment := range segments {
		bounds[segment.ID] = [2]int64{segment.TimelineStartUS, segment.TimelineStartUS + segment.DurationUS}
	}
	return demolitionScenario{
		req:            GenerateRequest{Title: "Demolition benchmark", SourceLanguage: "en"},
		result:         result,
		timelineBounds: bounds,
	}
}

// demolitionCountingResolver wraps the certified fixture resolver and counts
// the operations the final-job handoff actually performs.
type demolitionCountingResolver struct {
	inner          FinalJobAssetResolver
	folderListings int
	assetResolves  int
	stockResolves  int
	sizeLookups    int
}

func (r *demolitionCountingResolver) ResolveFinalJobAsset(ctx context.Context, id string) (FinalJobRuntimeAssetV1, error) {
	r.assetResolves++
	return r.inner.ResolveFinalJobAsset(ctx, id)
}

func (r *demolitionCountingResolver) ResolveFinalJobStockAsset(ctx context.Context, id string) (FinalJobRuntimeAssetV1, error) {
	r.stockResolves++
	if stock, ok := r.inner.(finalJobStockAssetResolver); ok {
		return stock.ResolveFinalJobStockAsset(ctx, id)
	}
	return r.inner.ResolveFinalJobAsset(ctx, id)
}

func (r *demolitionCountingResolver) FinalJobPublishedFileSize(ctx context.Context, id string) (int64, error) {
	r.sizeLookups++
	return r.inner.FinalJobPublishedFileSize(ctx, id)
}

func (r *demolitionCountingResolver) ListFinalJobStockFolder(ctx context.Context, folderID string) ([]FinalJobStockFile, error) {
	r.folderListings++
	if lister, ok := r.inner.(interface {
		ListFinalJobStockFolder(context.Context, string) ([]FinalJobStockFile, error)
	}); ok {
		return lister.ListFinalJobStockFolder(ctx, folderID)
	}
	return nil, fmt.Errorf("fixture resolver cannot list stock folder %q", folderID)
}

func (r *demolitionCountingResolver) operations() int {
	return r.folderListings + r.assetResolves + r.stockResolves + r.sizeLookups
}

func runDemolitionBuild(b *testing.B, scenario demolitionScenario) (nsPerOp float64, opsPerBuild int) {
	b.Helper()
	resolver := &demolitionCountingResolver{inner: fixedMediaSourcePayloadResolver{}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := BuildFinalJobPayloads(context.Background(), "bench-run", scenario.req, scenario.result, resolver); err != nil {
			b.Fatalf("BuildFinalJobPayloads: %v", err)
		}
	}
	b.StopTimer()
	if resolver.folderListings > 0 {
		b.Fatalf("final-job payload build listed the stock folder %d times; stock planning must stay outside the Producer", resolver.folderListings)
	}
	nsPerOp = float64(b.Elapsed().Nanoseconds()) / float64(b.N)
	opsPerBuild = resolver.operations() / b.N
	b.ReportMetric(float64(opsPerBuild), "resolver_ops/op")
	b.ReportMetric(float64(resolver.stockResolves)/float64(b.N), "stock_resolves/op")
	b.ReportMetric(float64(resolver.folderListings)/float64(b.N), "stock_listings/op")
	b.ReportMetric(nsPerOp/1e6, "build_ms/op")
	return nsPerOp, opsPerBuild
}

// BenchmarkFinalJobDemolition reports the operations and measured latency the
// demolished final-job paths no longer pay. Parent-level metrics aggregate the
// per-operation measurements into a per-job ledger.
func BenchmarkFinalJobDemolition(b *testing.B) {
	scenario := newDemolitionScenario()
	var payloadBuildNS, stockPrefetchNS, overlayCompileNS float64
	var payloadOps, stockOps, compileOps, introFrames int

	// Current handoff: builds the complete final-job envelope and counts the
	// resolver operations it actually performs. The Producer performs no stock
	// folder listing and no stock prefetch, which the two stock_* metrics pin.
	b.Run("PayloadBuild", func(b *testing.B) {
		var ops int
		payloadBuildNS, ops = runDemolitionBuild(b, scenario)
		payloadOps = ops
	})

	// Retry reuse: on a SUCCEEDED receipt submitFinalJob returns before the
	// submitter, so an entire payload build is skipped. Its measured cost is
	// the latency saved per retry.
	b.Run("RetrySavedPayloadRebuild", func(b *testing.B) {
		ns, _ := runDemolitionBuild(b, scenario)
		b.ReportMetric(1, "removed_builds/op")
		b.ReportMetric(ns/1e6, "saved_ms/op")
		payloadBuildNS = ns
	})

	// Stock planning: the demolition moved selection to the stockpipeline, so
	// the Producer no longer lists the folder or resolves each selected file.
	// The benchmark prices exactly one listing plus one resolution per stock
	// scene — the removed pre-demolition work.
	b.Run("StockPrefetchRemoved", func(b *testing.B) {
		resolver := &demolitionCountingResolver{inner: fixedMediaSourcePayloadResolver{}}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := resolver.ListFinalJobStockFolder(context.Background(), "folder-1"); err != nil {
				b.Fatalf("list stock folder: %v", err)
			}
			for sceneIndex := 0; sceneIndex < demolitionStockScenes; sceneIndex++ {
				if _, err := resolver.ResolveFinalJobStockAsset(context.Background(), "drive-asset-5000"); err != nil {
					b.Fatalf("resolve stock asset: %v", err)
				}
			}
		}
		b.StopTimer()
		stockPrefetchNS = float64(b.Elapsed().Nanoseconds()) / float64(b.N)
		stockOps = demolitionStockScenes + 1
		b.ReportMetric(float64(stockOps), "removed_ops/op")
		b.ReportMetric(stockPrefetchNS/1e6, "saved_ms/op")
	})

	// Editorial authority: production now sends exactly one editorial_plan.v2
	// request. The retired duplicate Go decision paid at least this request
	// projection and JSON envelope before the Rust authority ran.
	b.Run("DuplicateEditorialCompileRemoved", func(b *testing.B) {
		scenes := scenario.result.Scenes[1:]
		canvas := OverlayCanvasSpec{Width: 1920, Height: 1080, FPSNum: demolitionFPS, FPSDen: 1}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			request, err := buildOverlayPlanV2Request(
				scenario.result, "en", canvas, "bench-plan", "bench-video", "bench-project", "bench-seed",
				scenes, nil, nil, nil, scenario.timelineBounds,
			)
			if err != nil {
				b.Fatalf("build v2 request: %v", err)
			}
			payload, err := json.Marshal(request)
			if err != nil {
				b.Fatalf("marshal v2 request: %v", err)
			}
			b.SetBytes(int64(len(payload)))
		}
		b.StopTimer()
		overlayCompileNS = float64(b.Elapsed().Nanoseconds()) / float64(b.N)
		compileOps = 1
		b.ReportMetric(float64(compileOps), "removed_compiles/op")
		b.ReportMetric(overlayCompileNS/1e6, "saved_ms_lower_bound/op")
	})

	// Intro window: the certified 60s source is planned for its bound 5s
	// window. This is a remote encoder-frame count, not a local latency claim.
	b.Run("IntroWindowFramesRemoved", func(b *testing.B) {
		framesSaved := demolitionIntroClips * (demolitionIntroSourceFrames - demolitionIntroBoundFrames)
		secondsSaved := demolitionIntroClips * (demolitionIntroSourceSecs - demolitionIntroSeconds)
		introFrames = framesSaved
		b.ReportMetric(float64(framesSaved), "frames_saved/job")
		b.ReportMetric(float64(secondsSaved), "source_seconds_saved/job")
		// The benchmark body has no local work: it reports the deterministic
		// frame ledger of the fixture, not a synthetic sleep.
		for i := 0; i < b.N; i++ {
			_ = framesSaved
		}
	})

	// Ledger row. A parent benchmark that only contains sub-benchmarks does
	// not print its own metrics, so the aggregate is reported as an explicit
	// leaf: the current build's operation count and the per-job latency the
	// removed local operations would have cost. It performs no work of its
	// own; the numbers come from the measured sub-benchmarks above.
	b.Run("SavedLedger", func(b *testing.B) {
		b.ReportMetric(float64(payloadOps), "current_resolver_ops/job")
		b.ReportMetric(float64(stockOps+compileOps), "removed_local_ops/job")
		b.ReportMetric(float64(introFrames), "removed_intro_frames/job")
		b.ReportMetric((stockPrefetchNS+overlayCompileNS)/1e6, "saved_ms_lower_bound/job")
		b.ReportMetric(payloadBuildNS/1e6, "saved_ms_per_retry/job")
	})
}
