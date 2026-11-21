package scriptgeneration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/overlayjoin"
)

// OverlayClipFetcher materializes one certified per-item overlay render into a
// local file. It is the only way the join may obtain those pixels: the rendered
// artifact is the certified source, so a re-encode from anything else would
// ship different bytes than the renderer certified.
type OverlayClipFetcher interface {
	FetchOverlayClip(ctx context.Context, artifact RenderArtifact, destPath string) error
}

// LocalFinalVideoArtifact is the job-owned final MP4 assembled from the
// certified per-item overlay renders. It is distinct from RemoteFinalVideo:
// that one is a Master render receipt, this one was joined on the job's own
// timeline from the clips this run rendered.
type LocalFinalVideoArtifact struct {
	Path       string
	SizeBytes  int64
	SHA256     string
	DurationUS int64
	Width      int
	Height     int
	FPS        int
	// JoinedItems is how many certified item renders were placed on the
	// timeline; PlanItems is how many the plan carried. They differ when an
	// item produced no render (cache miss, unsupported item), and the pair is
	// the observable difference between "joined everything" and "joined what
	// exists".
	JoinedItems int
	PlanItems   int
}

// SetOverlayClipFetcher wires the artifact fetcher used by the local final
// video join. Nil disables the join: a composition that has no way to fetch the
// certified clips keeps the historical audio-only behaviour instead of failing
// every run.
func (r *Runner) SetOverlayClipFetcher(fetcher OverlayClipFetcher) {
	if r == nil {
		return
	}
	r.overlayClipFetcher = fetcher
}

// SetOverlayJoinEncoder wires the encoder policy resolved by the application
// composition root. The local final-video join must not choose an encoder.
func (r *Runner) SetOverlayJoinEncoder(policy mediaexec.EncoderPolicy) {
	if r == nil {
		return
	}
	r.overlayJoinEncoder = policy
}

// assembleFinalVideo joins the certified per-item overlay renders into the
// job's own MP4: background over the plan's full duration, every clip on its
// plan window, the certified final audio muxed in.
//
// It is a no-op when the run did not request a video, when a final video
// already exists (remote master or a previous attempt), when nothing was
// rendered, or when no fetcher is wired. When the join IS possible and fails,
// the caller fails the run: shipping audio plus a script for a render-enabled
// job while claiming success is the exact silent gap this join closes.
func (r *Runner) assembleFinalVideo(ctx context.Context, runID string, req GenerateRequest, result *GenerateResult) error {
	if r == nil || result == nil {
		return nil
	}
	if !req.Render.Enabled && !req.Render.OverlaysOnly {
		return nil
	}
	// A final job hands delivery to the Master render; only the local lane
	// owns its own join.
	if req.FinalJob || result.LocalFinalVideo != nil {
		return nil
	}
	if result.OverlayRender == nil || len(result.OverlayRender.Items) == 0 {
		return nil
	}
	if r.overlayClipFetcher == nil {
		if r.log != nil {
			r.log.Warn("final video join skipped: certified overlay clip fetcher is not wired",
				zap.String("run_id", runID))
		}
		return nil
	}
	if result.OverlayPlan == nil {
		return fmt.Errorf("final video join: run has overlay renders but no overlay plan")
	}
	if result.FinalAudio == nil || strings.TrimSpace(result.FinalAudio.Path) == "" {
		return fmt.Errorf("final video join: certified final audio is missing")
	}

	width, height, fps := overlayJoinCanvas(result.OverlayPlan)
	durationUS := result.OverlayPlan.DurationMS * 1000
	if durationUS <= 0 && result.FinalAudio.DurationUS > 0 {
		durationUS = result.FinalAudio.DurationUS
	}

	backgroundPath, err := r.overlayJoinBackground(ctx, result.OverlayPlan)
	if err != nil {
		return err
	}

	joinDir := filepath.Join(os.TempDir(), "pipelinegen-job-final", runID)
	if err := os.MkdirAll(joinDir, 0o755); err != nil {
		return fmt.Errorf("final video join: mkdir %s: %w", joinDir, err)
	}
	fetchStarted := time.Now()
	clips := make([]overlayjoin.Clip, 0, len(result.OverlayPlan.Items))
	for _, item := range result.OverlayPlan.Items {
		artifact, _, ok := overlayArtifactForItem(result.OverlayRender, item.ID)
		if !ok || artifact == nil {
			continue
		}
		localPath := filepath.Join(joinDir, sanitizeJoinItemID(item.ID)+".mp4")
		if err := r.overlayClipFetcher.FetchOverlayClip(ctx, *artifact, localPath); err != nil {
			return fmt.Errorf("final video join: fetch clip %s: %w", item.ID, err)
		}
		durationUS := overlayItemDurationUS(item)
		if durationUS <= 0 {
			// The plan window carries no length: bound the overlay by the clip's
			// own duration instead of leaving it open (an open overlay freezes
			// its last frame over the rest of the timeline).
			probed, probeErr := overlayjoin.ProbeDurationUS(ctx, localPath, "")
			if probeErr != nil {
				return fmt.Errorf("final video join: %w", probeErr)
			}
			durationUS = probed
		}
		clips = append(clips, overlayjoin.Clip{
			Path:       localPath,
			StartUS:    overlayItemStartUS(item),
			DurationUS: durationUS,
		})
	}
	fetchMS := time.Since(fetchStarted).Milliseconds()
	if len(clips) == 0 {
		return fmt.Errorf("final video join: no certified overlay clip could be materialized")
	}
	sort.SliceStable(clips, func(i, j int) bool { return clips[i].StartUS < clips[j].StartUS })

	outputPath := filepath.Join(joinDir, "final_video.mp4")
	joinStarted := time.Now()
	joined, err := overlayjoin.Run(ctx, overlayjoin.Spec{
		BackgroundPath: backgroundPath,
		AudioPath:      result.FinalAudio.Path,
		OutputPath:     outputPath,
		Width:          width,
		Height:         height,
		FPS:            fps,
		Encoder:        r.overlayJoinEncoder,
		DurationUS:     durationUS,
		Clips:          clips,
	}, "")
	joinMS := time.Since(joinStarted).Milliseconds()
	if err != nil {
		// A failed assembly still consumed its fetch and encode wall; attribute
		// it before returning so the failure is not another silent stage wall.
		recordFinalVideoJoin(ctx, finalVideoJoinTiming{clips: len(clips), fetchMS: fetchMS, joinMS: joinMS})
		return fmt.Errorf("final video join: %w", err)
	}
	hashStarted := time.Now()
	digest, err := fileSHA256(joined.Path)
	hashMS := time.Since(hashStarted).Milliseconds()
	if err != nil {
		return fmt.Errorf("final video join: hash output: %w", err)
	}
	recordFinalVideoJoin(ctx, finalVideoJoinTiming{
		clips:   len(clips),
		bytes:   joined.SizeBytes,
		fetchMS: fetchMS,
		joinMS:  joinMS,
		hashMS:  hashMS,
	})
	result.LocalFinalVideo = &LocalFinalVideoArtifact{
		Path:        joined.Path,
		SizeBytes:   joined.SizeBytes,
		SHA256:      digest,
		DurationUS:  durationUS,
		Width:       width,
		Height:      height,
		FPS:         fps,
		JoinedItems: len(clips),
		PlanItems:   len(result.OverlayPlan.Items),
	}
	if r.log != nil {
		r.log.Info("final video joined from certified overlay renders",
			zap.String("run_id", runID),
			zap.String("path", joined.Path),
			zap.Int("joined_items", len(clips)),
			zap.Int("plan_items", len(result.OverlayPlan.Items)),
			zap.Int("width", width),
			zap.Int("height", height),
			zap.Int64("duration_us", durationUS),
			zap.Int64("fetch_ms", fetchMS),
			zap.Int64("join_ms", joinMS),
			zap.Int64("hash_ms", hashMS))
	}
	return nil
}

// overlayJoinBackground resolves the plan's background to a local path. A color
// background (including the deliberate black canvas) returns an empty path: the
// joiner then renders that color itself instead of pretending a file exists.
func (r *Runner) overlayJoinBackground(ctx context.Context, plan *capmodel.OverlayPlan) (string, error) {
	if plan == nil || plan.Background == nil {
		return "", nil
	}
	kind := strings.ToLower(strings.TrimSpace(plan.Background.Kind))
	if kind == "" || kind == "color" {
		return "", nil
	}
	assetID := ""
	if refs := plan.Background.AssetRefs; len(refs) > 0 {
		if path := strings.TrimSpace(refs[0].LocalPath); path != "" {
			return path, nil
		}
		assetID = strings.TrimSpace(refs[0].AssetID)
	}
	if assetID == "" {
		return "", fmt.Errorf("final video join: background %q has no asset identity", kind)
	}
	if r.overlayBackgroundSource == nil {
		return "", fmt.Errorf("final video join: background %q cannot be resolved (no resolver wired)", kind)
	}
	asset, err := r.overlayBackgroundSource.ResolveOverlayBackground(ctx, assetID)
	if err != nil {
		return "", fmt.Errorf("final video join: resolve background %q: %w", assetID, err)
	}
	if strings.TrimSpace(asset.LocalPath) == "" {
		return "", fmt.Errorf("final video join: background %q resolved without a local path", assetID)
	}
	return asset.LocalPath, nil
}

func overlayJoinCanvas(plan *capmodel.OverlayPlan) (int, int, int) {
	width, height, fps := 1920, 1080, 24
	if plan == nil {
		return width, height, fps
	}
	if plan.Width > 0 && plan.Height > 0 {
		width, height = plan.Width, plan.Height
	}
	if plan.FPSNum > 0 && plan.FPSDen > 0 {
		fps = plan.FPSNum / plan.FPSDen
		if fps <= 0 {
			fps = 24
		}
	}
	return width, height, fps
}

func overlayItemStartUS(item capmodel.OverlayItem) int64 {
	if item.StartUS > 0 {
		return item.StartUS
	}
	if item.StartMs > 0 {
		return item.StartMs * 1000
	}
	return 0
}

func overlayItemDurationUS(item capmodel.OverlayItem) int64 {
	if item.DurationUS > 0 {
		return item.DurationUS
	}
	if item.EndMs > item.StartMs {
		return (item.EndMs - item.StartMs) * 1000
	}
	return 0
}

func sanitizeJoinItemID(id string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, id)
	if safe == "" {
		return "item"
	}
	return safe
}

func fileSHA256(path string) (string, error) {
	hash, _, err := digest.SHA256File(path)
	if err != nil {
		return "", err
	}
	return hash, nil
}

// finalVideoJoinTiming is the wall clock of the local assembly, split by the
// three boundaries the join actually crosses.
type finalVideoJoinTiming struct {
	clips   int
	bytes   int64
	fetchMS int64
	joinMS  int64
	hashMS  int64
}

// recordFinalVideoJoin attributes the local assembly on the run bound to ctx.
//
// The assembly used to be charged to the persistence stage with no operation
// of its own: on the r8 biography run 455,555 ms of a 648,922 ms wall (70.2%)
// reported as `persistence` while the only operation recorded there was
// persistence.sqlite at 1 ms. The stage was right — the join really is part
// of persistence — but the report could not say which boundary owned the
// time, so no speed lever could be aimed at it. The three operations are the
// certified-clip fetch (object store), the ffmpeg overlay graph + canonical
// encode, and the artifact hash.
func recordFinalVideoJoin(ctx context.Context, t finalVideoJoinTiming) {
	stage := kernobs.StageRunPersistence
	component := kernobs.ComponentFFmpeg
	items := int64(t.clips)
	// A boundary that did not run (a failed join never hashes, for example) is
	// not recorded as a zero-length operation: no fake measurements.
	record := func(op kernobs.OperationName, ms int64, bytes int64) {
		if ms <= 0 {
			return
		}
		kernobs.RecordOperation(ctx, kernobs.OperationInfo{
			Stage: stage, Component: component, Operation: op, Items: items, Bytes: bytes,
		}, ms)
	}
	record(kernobs.OperationFetch, t.fetchMS, 0)
	record(kernobs.OperationJoin, t.joinMS, t.bytes)
	record(kernobs.OperationHash, t.hashMS, t.bytes)
}
