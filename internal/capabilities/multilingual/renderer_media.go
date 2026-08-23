package multilingual

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"go.uber.org/zap"
)

// renderRustResult is the internal outcome of rendering via the sealed clip.render plan.
type renderRustResult struct {
	outputPath string
	contract   *cliprender.ResolvedContract
}

func (r *Renderer) renderRust(ctx context.Context, in VariantInput, outputPath string) (*renderRustResult, error) {
	width, height := r.rustWidth, r.rustHeight
	fpsNum, fpsDen := r.rustFPSNum, r.rustFPSDen
	if width <= 0 {
		width = cliprender.DefaultWidth
	}
	if height <= 0 {
		height = cliprender.DefaultHeight
	}
	if fpsNum <= 0 || fpsDen <= 0 {
		fpsNum, fpsDen = cliprender.DefaultFPSNum, 1
	}
	request := &cliprender.RenderRequest{
		SourceAssetID: in.SourceClipID,
		Output: &cliprender.OutputSpec{
			Contract: cliprender.OutputContractVeloxAssemblyReadyV1,
			Width:    width, Height: height, FPSNum: fpsNum, FPSDen: fpsDen,
		},
	}
	request.Normalize()
	contract, err := cliprender.NewContractResolver().Resolve(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("resolve output contract: %w", err)
	}
	plan, err := cliprender.Compile(cliprender.CompileInput{
		RunID:          in.SourceClipID + ":" + in.Language + ":" + in.ASSHash,
		Source:         &cliprender.MaterializedAsset{AssetID: in.SourceClipID, LocalPath: in.SourcePath, SHA256: in.SourceSHA256},
		Subtitles:      &cliprender.SubtitleArtifact{LocalPath: in.ASSPath, SHA256: in.ASSHash, Mode: cliprender.SubtitlesModeBurn, StyleID: in.SubtitleStyleVersion},
		Background:     optionalMaterializedAsset(in.BackgroundAssetID, in.BackgroundPath, in.BackgroundSHA256),
		BackgroundMode: backgroundMode(in),
		Watermark:      optionalMaterializedAsset(in.WatermarkAssetID, in.WatermarkPath, in.WatermarkSHA256),
		WatermarkSpec: &cliprender.WatermarkSpec{
			Position: defaultWatermarkPosition(in.WatermarkPosition),
			Opacity:  defaultWatermarkOpacity(in.WatermarkOpacity),
			MarginPX: in.WatermarkMarginPX,
		},
		Contract:               contract,
		AudioMode:              cliprender.AudioModeCopyIfCompatible,
		OutputPath:             outputPath,
		ForegroundScalePercent: in.ForegroundScalePercent,
	})
	if err != nil {
		return nil, fmt.Errorf("compile sealed plan: %w", err)
	}
	result, err := r.rust.RenderClip(ctx, plan)
	if err != nil {
		return nil, err
	}
	if result.OutputPath != "" && result.OutputPath != outputPath {
		return nil, fmt.Errorf("rust returned unexpected output path %q", result.OutputPath)
	}
	return &renderRustResult{outputPath: result.OutputPath, contract: contract}, nil
}

func optionalMaterializedAsset(id, path, sha string) *cliprender.MaterializedAsset {
	if id == "" && path == "" && sha == "" {
		return nil
	}
	return &cliprender.MaterializedAsset{AssetID: id, LocalPath: path, SHA256: sha}
}

func backgroundMode(in VariantInput) string {
	if in.BackgroundPath != "" || in.BackgroundAssetID != "" {
		return cliprender.BackgroundModeAsset
	}
	return cliprender.BackgroundModeNone
}

func defaultWatermarkPosition(position string) string {
	if position == "" {
		return cliprender.PositionCenter
	}
	return position
}

func defaultWatermarkOpacity(opacity float64) float64 {
	if opacity == 0 {
		return 1
	}
	return opacity
}

func (r *Renderer) fail(res VariantResult, start time.Time, err error) VariantResult {
	res.Status = "failed"
	res.Error = err.Error()
	res.Validation = err.Error()
	res.RenderCompletedAt = time.Now()
	res.RenderMS = time.Since(start).Milliseconds()
	r.log.Warn("multilingual.render.failed", zap.String("language", res.Language), zap.Error(err))
	return res
}

// scalePadFilter is the canonical source→PlayRes scale+pad chain (single
// owner). The subtitle-visible source frame extraction MUST use the same
// chain the renderer applied so the source reference frame and the rendered
// frame line up pixel-for-pixel except for the burned subtitles.
func scalePadFilter() string {
	return fmt.Sprintf(
		"scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,setsar=1",
		cliprender.DefaultWidth, cliprender.DefaultHeight, cliprender.DefaultWidth, cliprender.DefaultHeight,
	)
}

// ParseFPS parses an ffprobe frame-rate token ("30000/1001", "25/1", or a
// bare float) into frames per second. Returns 0 on malformed input. Exported
// so the admin CLI can pre-probe the source clip's fps.
func ParseFPS(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "0/0" {
		return 0
	}
	if i := strings.IndexByte(s, '/'); i >= 0 {
		var num, den float64
		if _, err := fmt.Sscanf(s, "%f/%f", &num, &den); err == nil && den != 0 {
			return num / den
		}
		return 0
	}
	var f float64
	if _, err := fmt.Sscanf(s, "%f", &f); err == nil {
		return f
	}
	return 0
}

// assDialogue is one subtitle cue line with its timing window and text.
type assDialogue struct {
	StartMs int64
	EndMs   int64
	Text    string
}

// parseASSDialogues reads every Dialogue line with non-empty text and valid
// timing from an .ass file, in order. A missing/unreadable file yields an
// empty slice (fail-soft, matching assHasDialogue). Used by subtitleVisible
// to pick the cue timestamp at which a subtitle should be on screen.
func parseASSDialogues(path string) []assDialogue {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []assDialogue
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "Dialogue:") {
			continue
		}
		parts := strings.SplitN(line, ",", 10)
		if len(parts) < 10 || strings.TrimSpace(parts[9]) == "" {
			continue
		}
		startMs := parseASSTimeMS(parts[1])
		endMs := parseASSTimeMS(parts[2])
		if startMs < 0 || endMs <= startMs {
			continue
		}
		out = append(out, assDialogue{StartMs: startMs, EndMs: endMs, Text: strings.TrimSpace(parts[9])})
	}
	return out
}

// parseASSTimeMS parses an ASS timestamp "H:MM:SS.cc" into milliseconds.
// Returns -1 on malformed input.
func parseASSTimeMS(t string) int64 {
	var h, m, s, c int64
	if _, err := fmt.Sscanf(t, "%d:%d:%d.%d", &h, &m, &s, &c); err != nil {
		return -1
	}
	return h*3600000 + m*60000 + s*1000 + c*10
}

// extractBandGray extracts one grayscale frame's subtitle band from a video at
// the given timestamp. source=true applies the same scale+pad chain the burn
// uses (so the source reference aligns with the rendered frame); source=false
// reads the already-1080p rendered output directly. Output is raw 8-bit
// grayscale, subtitleBandWidth×subtitleBandHeight bytes.
func (r *Renderer) extractBandGray(ctx context.Context, videoPath string, tsSec float64, source bool) ([]byte, error) {
	vf := fmt.Sprintf("crop=%d:%d:0:%d", subtitleBandWidth, subtitleBandHeight, subtitleBandY)
	if source {
		vf = scalePadFilter() + "," + vf
	}
	args := []string{
		"-i", videoPath,
		"-ss", fmt.Sprintf("%.3f", tsSec),
		"-frames:v", "1",
		"-vf", vf,
		"-f", "rawvideo",
		"-pix_fmt", "gray",
		"-",
	}
	cmd := exec.CommandContext(ctx, r.ffmpegPath, args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg frame extract: %w", err)
	}
	return out, nil
}

// frameDiffFraction returns the fraction of aligned pixels whose absolute
// grayscale difference is >= threshold. It compares up to the shorter length.
func frameDiffFraction(a, b []byte, threshold byte) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	if n == 0 {
		return 0
	}
	diffs := 0
	for i := 0; i < n; i++ {
		d := int(a[i]) - int(b[i])
		if d < 0 {
			d = -d
		}
		if d >= int(threshold) {
			diffs++
		}
	}
	return float64(diffs) / float64(n)
}

// subtitleVisible verifies the subtitles are actually burned into the
// rendered pixels, not merely present in the .ass. It samples the subtitle
// band at the first cue's midpoint and requires a minimum fraction of pixels
// to differ strongly from the un-subtitled source frame at the same
// timestamp. Callers skip it in benchmark (SkipPublish) mode, which measures
// pure render cost.
func (r *Renderer) subtitleVisible(ctx context.Context, in VariantInput, outputPath string) error {
	if r.ffmpegPath == "" {
		return nil // ffmpeg not available (test mode); skip pixel-level verification
	}
	dialogues := parseASSDialogues(in.ASSPath)
	if len(dialogues) == 0 {
		return fmt.Errorf("subtitle-visible: ASS %s has no dialogue lines", in.ASSPath)
	}
	first := dialogues[0]
	tSec := float64(first.StartMs+first.EndMs) / 2 / 1000.0
	if durMS := in.SourceDuration.Milliseconds(); durMS > 0 && tSec >= float64(durMS)/1000.0 {
		tSec = float64(durMS) / 1000.0 / 2
	}
	if tSec < 0 {
		tSec = 0
	}
	srcBand, err := r.extractBandGray(ctx, in.SourcePath, tSec, true)
	if err != nil {
		return fmt.Errorf("subtitle-visible: source frame: %w", err)
	}
	outBand, err := r.extractBandGray(ctx, outputPath, tSec, false)
	if err != nil {
		return fmt.Errorf("subtitle-visible: output frame: %w", err)
	}
	frac := frameDiffFraction(srcBand, outBand, subtitleDiffThreshold)
	if frac < subtitleMinVisible {
		return fmt.Errorf("subtitle-visible: no burn-in detected (band diff fraction %.5f < %.5f)", frac, subtitleMinVisible)
	}
	return nil
}

// noLanguageContamination certifies wrong-language contamination = 0 at the
// render boundary: the .ass actually burned must be exactly the artifact
// generated for THIS language (hash equality). A wrong-language (or tampered)
// .ass has a different content hash and is rejected before ffmpeg runs.
func (r *Renderer) noLanguageContamination(in VariantInput) error {
	if in.ASSHash == "" {
		return nil // no expected hash to verify against (legacy callers)
	}
	actual, _, err := sha256File(in.ASSPath)
	if err != nil {
		return fmt.Errorf("wrong-language contamination: read ASS: %w", err)
	}
	if actual != in.ASSHash {
		return fmt.Errorf("wrong-language contamination: ASS %s hash %s != expected %s for language %q", in.ASSPath, actual, in.ASSHash, in.Language)
	}
	return nil
}

type publishResult struct {
	FileID      string
	WebViewLink string
	OutputHash  string
	SizeBytes   int64
}

// publish uploads the validated mp4 to the destination Drive folder and
// returns the canonical link + content hash + size.
func (r *Renderer) publish(ctx context.Context, in VariantInput, path string, durationMs int64) (*publishResult, error) {
	hash, size, err := sha256File(path)
	if err != nil {
		return nil, fmt.Errorf("hash output: %w", err)
	}
	res, err := r.publisher.Publish(ctx, delivery.PublishRequest{
		Destination:         delivery.DestinationClipMetadata,
		DestinationFolderID: in.DriveFolderID,
		LocalPath:           path,
		Filename:            in.OutputFilename,
		Language:            in.Language,
		ContentHash:         hash,
		IdempotencyKey:      delivery.DeriveIdempotencyKey(delivery.DestinationClipMetadata, in.SourceClipID+":"+in.Language, hash, 1),
		ConflictPolicy:      delivery.ConflictSkip,
	})
	if err != nil {
		return nil, fmt.Errorf("publish rendered clip: %w", err)
	}
	if res == nil || res.FileID == "" {
		return nil, fmt.Errorf("publish rendered clip: empty Drive result")
	}
	return &publishResult{FileID: res.FileID, WebViewLink: res.WebViewLink, OutputHash: hash, SizeBytes: size}, nil
}
