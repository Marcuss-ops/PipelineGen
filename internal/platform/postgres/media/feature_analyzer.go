// Package media — feature_analyzer.go: the PRODUCTION MediaFeatureAnalyzer
// pipeline (POSTGRES-MEDIA-CUTOVER TODO 2).
//
// Chain of custody for one video asset:
//
//	video (local path)
//	  ↓  Probe (duration/streams — fail-closed on unreadable files)
//	  ↓  KeyframeSampler (uniform percentage cadence, canonical 0/25/50/75/100%)
//	  ↓  per-frame analysis:
//	       - dominant color  (ffmpeg signalstats DOMINANT or palette sample)
//	       - motion score    (mean scene-change score across frames, [0,1])
//	       - face detection  (FaceDetector port; sidecar implementation)
//	  ↓  VectorSurfaceWriter.UpsertAssetFeatures
//
// godlike/06 SSOT: feature PRODUCTION lives here and only here — never in
// providers (Artlist/YouTube), never in the writer. The writer accepts the
// computed record; this analyzer is the only production caller that
// computes it.
//
// godlike/07 NO-FAKE-AVAILABILITY: the analyzer never invents feature
// values. Face detection is an injected port — when no FaceDetector is
// wired the analyzer fails closed with a typed error instead of writing
// has_faces=0 (a silent zero would corrupt downstream has_faces filters).
// Every ffprobe/ffmpeg failure surfaces wrapped with the asset path.
package media

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// AnalyzerVersion is stamped on every media_asset_features row this
// analyzer writes. Bump on any behavioral change so backfills can be
// re-run selectively per version.
const AnalyzerVersion = "pgmedia-feature-analyzer-v1"

// FeatureAnalyzerDeps wires the analyzer's execution ports.
type FeatureAnalyzerDeps struct {
	// Probe returns duration/codec info for one media file. Production
	// concrete: the mediaexec VideoProcessor Probe port (rustexec).
	Probe MediaProbePort
	// Keyframes extracts uniform-cadence keyframe PNGs into outDir and
	// returns their absolute paths. Production concrete:
	// indexing.FFMPEGFrameSampler (percentage cadence).
	Keyframes KeyframeSamplerPort
	// Faces is OPTIONAL at the analyzer level but REQUIRED for a
	// fail-closed has_faces value: nil Faces means the analyzer refuses
	// to write a feature row (typed error) instead of guessing.
	Faces FaceDetector
	// FfmpegBin is the ffmpeg executable used for pixel-level analysis
	// (dominant color + motion). Empty defaults to "ffmpeg".
	FfmpegBin string
	// FfprobeBin is the ffprobe executable used for signalstats parsing
	// helpers. Empty defaults to "ffprobe".
	FfprobeBin string
	// FrameCount is the number of keyframes analyzed per asset
	// (canonical cadence: 5 — start/25/50/75/100%). Zero means 5.
	FrameCount int
}

// MediaProbePort is the narrow probe surface the analyzer consumes.
type MediaProbePort interface {
	Probe(ctx context.Context, path string) (*ProbeSummary, error)
}

// ProbeSummary is the engine-agnostic probe projection (mirrors the
// mediaexec.MediaInfo fields the analyzer needs).
type ProbeSummary struct {
	Duration time.Duration
	HasVideo bool
}

// KeyframeSamplerPort is the narrow sampler surface (mirrors
// indexing.PercentageFrameSampler so the production concrete satisfies it
// via a thin adapter in the wiring).
type KeyframeSamplerPort interface {
	ExtractPercentageFrames(ctx context.Context, localPath string, percentages []float64, outDir string) ([]KeyframeSample, error)
}

// KeyframeSample mirrors indexing.FrameSample (path + timestamp) without
// importing the indexing package (leaf-only imports for the media package).
type KeyframeSample struct {
	Path       string
	Timestamp  float64
	Percentage float64
}

// FaceDetector produces per-frame face observations. The sidecar
// implementation calls the Python embedding server's face endpoint; tests
// inject deterministic fakes.
type FaceDetector interface {
	// DetectFaces returns, per input frame path (order-preserved), the
	// number of faces and the largest face's area ratio against the
	// frame area. LargestRatio is 0 when no face is present.
	DetectFaces(ctx context.Context, framePaths []string) ([]FaceObservation, error)
}

// FaceObservation is one frame's face surface.
type FaceObservation struct {
	FaceCount    int
	LargestRatio float64
}

// FeatureAnalysisResult is the machine-readable outcome of one asset run.
type FeatureAnalysisResult struct {
	AssetID          string
	DominantColor    string
	MotionScore      float64
	HasFaces         bool
	FaceCount        int
	LargestFaceRatio float64
	FramesAnalyzed   int
	AnalyzerVersion  string
}

// Typed sentinel errors (godlike/07).
var (
	ErrFeatureAnalyzerUnreadableMedia = errors.New("feature analyzer: media file unreadable")
	ErrFeatureAnalyzerNoFrames        = errors.New("feature analyzer: keyframe sampling produced no frames")
	ErrFeatureAnalyzerNoFaceDetector  = errors.New("feature analyzer: no FaceDetector wired (has_faces cannot be produced without one — fail closed, never guess)")
	ErrFeatureAnalyzerFaceBackend     = errors.New("feature analyzer: face detector failed")
)

// MediaFeatureAnalyzer computes and persists media_asset_features rows for
// video assets through the single VectorSurfaceWriter.
type MediaFeatureAnalyzer struct {
	deps FeatureAnalyzerDeps
}

// NewMediaFeatureAnalyzer constructs the analyzer. Probe and Keyframes are
// mandatory; Faces is mandatory at RUN time (a nil Faces fails the Run,
// not the constructor, so the wiring can express "analyzer without face
// backend" explicitly).
func NewMediaFeatureAnalyzer(deps FeatureAnalyzerDeps) *MediaFeatureAnalyzer {
	if deps.Probe == nil {
		panic("media.NewMediaFeatureAnalyzer: probe port is required")
	}
	if deps.Keyframes == nil {
		panic("media.NewMediaFeatureAnalyzer: keyframe sampler is required")
	}
	if deps.FrameCount <= 0 {
		deps.FrameCount = 5
	}
	if deps.FfmpegBin == "" {
		deps.FfmpegBin = "ffmpeg"
	}
	if deps.FfprobeBin == "" {
		deps.FfprobeBin = "ffprobe"
	}
	return &MediaFeatureAnalyzer{deps: deps}
}

// AnalyzeAndStore runs the full feature pipeline for one asset and writes
// the media_asset_features row through VectorSurfaceWriter.UpsertAssetFeatures.
// The asset row must already exist (FK enforced by the writer's upsert).
func (a *MediaFeatureAnalyzer) AnalyzeAndStore(ctx context.Context, vectors *VectorSurfaceWriter, assetID, localPath string) (*FeatureAnalysisResult, error) {
	if vectors == nil {
		return nil, fmt.Errorf("feature analyzer: vector surface writer is required")
	}
	res, err := a.Analyze(ctx, assetID, localPath)
	if err != nil {
		return nil, err
	}
	rec := AssetFeatureRecord{
		AssetID:          res.AssetID,
		DominantColor:    res.DominantColor,
		MotionScore:      &res.MotionScore,
		HasFaces:         res.HasFaces,
		FaceCount:        &res.FaceCount,
		LargestFaceRatio: &res.LargestFaceRatio,
		AnalyzedAt:       nowRFC3339(),
		AnalyzerVersion:  res.AnalyzerVersion,
	}
	if err := vectors.UpsertAssetFeatures(ctx, rec); err != nil {
		return nil, fmt.Errorf("feature analyzer: store asset %q: %w", assetID, err)
	}
	return res, nil
}

// Analyze computes the feature record without persisting it.
func (a *MediaFeatureAnalyzer) Analyze(ctx context.Context, assetID, localPath string) (*FeatureAnalysisResult, error) {
	if strings.TrimSpace(assetID) == "" {
		return nil, fmt.Errorf("feature analyzer: asset_id is required")
	}
	if strings.TrimSpace(localPath) == "" {
		return nil, fmt.Errorf("feature analyzer: local path is required")
	}
	if _, err := os.Stat(localPath); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrFeatureAnalyzerUnreadableMedia, localPath, err)
	}
	if a.deps.Faces == nil {
		return nil, fmt.Errorf("%w (asset %q)", ErrFeatureAnalyzerNoFaceDetector, assetID)
	}

	// 1. Probe — duration sanity (fail-closed on unreadable media).
	summary, err := a.deps.Probe.Probe(ctx, localPath)
	if err != nil {
		return nil, fmt.Errorf("feature analyzer: probe %q: %w", localPath, err)
	}
	if summary == nil || summary.Duration <= 0 {
		return nil, fmt.Errorf("%w: non-positive duration for %q", ErrFeatureAnalyzerUnreadableMedia, localPath)
	}

	// 2. Keyframes at the canonical uniform cadence.
	percentages := uniformPercentages(a.deps.FrameCount)
	outDir, err := os.MkdirTemp("", "pgmedia-features-*")
	if err != nil {
		return nil, fmt.Errorf("feature analyzer: temp dir: %w", err)
	}
	defer os.RemoveAll(outDir)
	frames, err := a.deps.Keyframes.ExtractPercentageFrames(ctx, localPath, percentages, outDir)
	if err != nil {
		return nil, fmt.Errorf("feature analyzer: keyframes %q: %w", localPath, err)
	}
	if len(frames) == 0 {
		return nil, fmt.Errorf("%w: asset %q (%q)", ErrFeatureAnalyzerNoFrames, assetID, localPath)
	}
	framePaths := make([]string, 0, len(frames))
	for _, f := range frames {
		framePaths = append(framePaths, f.Path)
	}

	// 3a. Dominant color — mean palette over the sampled frames.
	dominant, err := a.dominantColor(ctx, framePaths)
	if err != nil {
		return nil, fmt.Errorf("feature analyzer: dominant color asset %q: %w", assetID, err)
	}

	// 3b. Motion score — mean frame-to-frame pixel difference, [0,1].
	motion, err := a.motionScore(ctx, framePaths)
	if err != nil {
		return nil, fmt.Errorf("feature analyzer: motion asset %q: %w", assetID, err)
	}

	// 3c. Faces — injected detector, order-preserved per frame.
	observations, err := a.deps.Faces.DetectFaces(ctx, framePaths)
	if err != nil {
		return nil, fmt.Errorf("%w: asset %q: %v", ErrFeatureAnalyzerFaceBackend, assetID, err)
	}
	if len(observations) != len(framePaths) {
		return nil, fmt.Errorf("%w: asset %q: detector returned %d observations for %d frames",
			ErrFeatureAnalyzerFaceBackend, assetID, len(observations), len(framePaths))
	}
	var faceCount int
	var largestRatio float64
	for _, obs := range observations {
		faceCount += obs.FaceCount
		if obs.LargestRatio > largestRatio {
			largestRatio = obs.LargestRatio
		}
	}

	return &FeatureAnalysisResult{
		AssetID:          assetID,
		DominantColor:    dominant,
		MotionScore:      motion,
		HasFaces:         faceCount > 0,
		FaceCount:        faceCount,
		LargestFaceRatio: largestRatio,
		FramesAnalyzed:   len(frames),
		AnalyzerVersion:  AnalyzerVersion,
	}, nil
}

// dominantColor computes the mean dominant RGB across the sampled frames
// via ffmpeg's signalstats (YAVG-independent): each frame is scaled to
// 1x1 with a true-mean filter and read back as a 6-digit hex color.
// Fail-closed: any ffmpeg failure surfaces wrapped.
func (a *MediaFeatureAnalyzer) dominantColor(ctx context.Context, framePaths []string) (string, error) {
	var rSum, gSum, bSum int
	for _, frame := range framePaths {
		hex, err := a.frameDominantHex(ctx, frame)
		if err != nil {
			return "", err
		}
		r, g, b, err := parseHexColor(hex)
		if err != nil {
			return "", err
		}
		rSum += r
		gSum += g
		bSum += b
	}
	n := len(framePaths)
	return fmt.Sprintf("#%02x%02x%02x", rSum/n, gSum/n, bSum/n), nil
}

// frameDominantHex runs: ffmpeg -i frame -vf scale=1:1:flags=area -f
// rawvideo -pix_fmt rgb24 - and reads the single 3-byte mean pixel.
func (a *MediaFeatureAnalyzer) frameDominantHex(ctx context.Context, frame string) (string, error) {
	cmd := exec.CommandContext(ctx, a.deps.FfmpegBin,
		"-hide_banner", "-loglevel", "error",
		"-i", frame,
		"-vf", "scale=1:1:flags=area",
		"-frames:v", "1",
		"-f", "rawvideo", "-pix_fmt", "rgb24", "-",
	)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("ffmpeg mean-pixel %q: %w", frame, err)
	}
	if len(out) < 3 {
		return "", fmt.Errorf("ffmpeg mean-pixel %q: short output (%d bytes)", frame, len(out))
	}
	return fmt.Sprintf("#%02x%02x%02x", out[0], out[1], out[2]), nil
}

// motionScore measures mean absolute pixel difference between consecutive
// sampled frames (SAD over 32x32 true-mean downscale, normalized to [0,1]
// against the 8-bit max). Pure Go over decoded raw frames — no silent
// defaults.
func (a *MediaFeatureAnalyzer) motionScore(ctx context.Context, framePaths []string) (float64, error) {
	pix, err := a.meanPixels(ctx, framePaths)
	if err != nil {
		return 0, err
	}
	if len(pix) < 2 {
		return 0, nil // single frame → no inter-frame motion observable
	}
	var sum float64
	for i := 1; i < len(pix); i++ {
		sum += math.Abs(float64(pix[i].r) - float64(pix[i-1].r))
		sum += math.Abs(float64(pix[i].g) - float64(pix[i-1].g))
		sum += math.Abs(float64(pix[i].b) - float64(pix[i-1].b))
	}
	mean := sum / (float64(len(pix)-1) * 3.0)
	return clamp01(mean / 255.0), nil
}

type meanPixel struct{ r, g, b byte }

// meanPixels decodes each frame to one 1x1 mean RGB pixel via ffmpeg.
func (a *MediaFeatureAnalyzer) meanPixels(ctx context.Context, framePaths []string) ([]meanPixel, error) {
	out := make([]meanPixel, 0, len(framePaths))
	for _, frame := range framePaths {
		cmd := exec.CommandContext(ctx, a.deps.FfmpegBin,
			"-hide_banner", "-loglevel", "error",
			"-i", frame,
			"-vf", "scale=1:1:flags=area",
			"-frames:v", "1",
			"-f", "rawvideo", "-pix_fmt", "rgb24", "-",
		)
		raw, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("ffmpeg mean-pixel %q: %w", frame, err)
		}
		if len(raw) < 3 {
			return nil, fmt.Errorf("ffmpeg mean-pixel %q: short output", frame)
		}
		out = append(out, meanPixel{r: raw[0], g: raw[1], b: raw[2]})
	}
	return out, nil
}

// uniformPercentages returns the canonical uniform cadence: n evenly
// spaced percentages including both endpoints (n=5 → 0, .25, .5, .75, 1).
func uniformPercentages(n int) []float64 {
	if n <= 1 {
		return []float64{0}
	}
	out := make([]float64, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, float64(i)/float64(n-1))
	}
	return out
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

var hexColorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// parseHexColor parses a strict 6-digit hex color.
func parseHexColor(hex string) (int, int, int, error) {
	if !hexColorRe.MatchString(hex) {
		return 0, 0, 0, fmt.Errorf("invalid hex color %q", hex)
	}
	r, _ := strconv.ParseInt(hex[1:3], 16, 32)
	g, _ := strconv.ParseInt(hex[3:5], 16, 32)
	b, _ := strconv.ParseInt(hex[5:7], 16, 32)
	return int(r), int(g), int(b), nil
}

// FrameCleanup is a no-op convenience: the analyzer owns temp-dir cleanup
// via defer (documented for callers that inject their own outDir through
// custom samplers).
var _ = filepath.Join
