package scriptgeneration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
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
	if len(clips) == 0 {
		return fmt.Errorf("final video join: no certified overlay clip could be materialized")
	}
	sort.SliceStable(clips, func(i, j int) bool { return clips[i].StartUS < clips[j].StartUS })

	outputPath := filepath.Join(joinDir, "final_video.mp4")
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
	if err != nil {
		return fmt.Errorf("final video join: %w", err)
	}
	digest, err := fileSHA256(joined.Path)
	if err != nil {
		return fmt.Errorf("final video join: hash output: %w", err)
	}
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
			zap.Int64("duration_us", durationUS))
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
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}
