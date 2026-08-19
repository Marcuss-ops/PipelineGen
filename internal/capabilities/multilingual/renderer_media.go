package multilingual

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"go.uber.org/zap"
)

func (r *Renderer) renderRust(ctx context.Context, in VariantInput, outputPath string) error {
	width, height, fps := r.rustWidth, r.rustHeight, r.rustFPS
	if width <= 0 {
		width = renderWidth
	}
	if height <= 0 {
		height = renderHeight
	}
	if fps <= 0 {
		fps = 30
	}
	request := &cliprender.RenderRequest{
		SourceAssetID: in.SourceClipID,
		Output: &cliprender.OutputSpec{
			Contract: cliprender.OutputContractVeloxEditingClipV1,
			Width:    width, Height: height, FPSNum: fps, FPSDen: 1,
		},
	}
	request.Normalize()
	contract, err := cliprender.NewContractResolver().Resolve(ctx, request)
	if err != nil {
		return fmt.Errorf("resolve output contract: %w", err)
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
		return fmt.Errorf("compile sealed plan: %w", err)
	}
	result, err := r.rust.RenderClip(ctx, plan)
	if err != nil {
		return err
	}
	if result.OutputPath != "" && result.OutputPath != outputPath {
		return fmt.Errorf("rust returned unexpected output path %q", result.OutputPath)
	}
	return nil
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
// owner). burn() and the subtitle-visible source frame extraction MUST use
// the identical chain so the source reference frame and the rendered frame
// line up pixel-for-pixel except for the burned subtitles.
func scalePadFilter() string {
	return fmt.Sprintf(
		"scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,setsar=1",
		renderWidth, renderHeight, renderWidth, renderHeight,
	)
}

// burn runs the single ffmpeg pass: scale+pad to the canonical ASS PlayRes
// (1920x1080) so fonts stay legible, rasterize the .ass via libass, keep the
// original audio stream bit-exact (audio unchanged), encode h264/yuv420p.
func (r *Renderer) burn(ctx context.Context, src, ass, out string) error {
	filter := scalePadFilter() + ",subtitles=filename=" + escapeFilterPath(ass)
	args := []string{
		"-y", "-i", src,
		"-vf", filter,
		"-c:v", r.encoder.Codec, "-preset", r.encoder.Preset, "-crf", fmt.Sprint(r.encoder.CRF), "-pix_fmt", "yuv420p",
		"-c:a", "copy",
		"-movflags", "+faststart",
		out,
	}
	cmd := exec.CommandContext(ctx, r.ffmpegPath, args...)
	if combined, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg %s: %w\n%s", r.ffmpegPath, err, lastLines(string(combined), 20))
	}
	return nil
}

// escapeFilterPath escapes a filesystem path for use inside an ffmpeg filter
// graph single-quoted value.
func escapeFilterPath(p string) string {
	p = strings.ReplaceAll(p, "\\", "\\\\")
	p = strings.ReplaceAll(p, "'", `\'`)
	return "'" + p + "'"
}

type ffprobeDoc struct {
	Streams []struct {
		CodecType    string `json:"codec_type"`
		CodecName    string `json:"codec_name"`
		Width        int    `json:"width"`
		Height       int    `json:"height"`
		AvgFrameRate string `json:"avg_frame_rate"`
		RFrameRate   string `json:"r_frame_rate"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

type probeResult struct {
	HasVideo   bool
	HasAudio   bool
	DurationMs int64
	Width      int
	Height     int
	FPS        float64
	VideoCodec string
}

// probe runs ffprobe on the rendered bytes. ffprobe is resolved alongside the
// configured ffmpeg binary.
func (r *Renderer) probe(ctx context.Context, path string) (*probeResult, error) {
	ffprobe := ffprobePathFor(r.ffmpegPath)
	cmd := exec.CommandContext(ctx, ffprobe, "-v", "error", "-show_streams", "-show_format", "-of", "json", path)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe %s: %w\n%s", ffprobe, err, lastLines(string(out), 20))
	}
	var doc ffprobeDoc
	if err := json.Unmarshal(out, &doc); err != nil {
		return nil, fmt.Errorf("ffprobe decode: %w", err)
	}
	res := &probeResult{}
	for _, s := range doc.Streams {
		switch s.CodecType {
		case "video":
			res.HasVideo = true
			res.Width = s.Width
			res.Height = s.Height
			res.VideoCodec = s.CodecName
			if fps := ParseFPS(s.AvgFrameRate); fps > 0 {
				res.FPS = fps
			} else {
				res.FPS = ParseFPS(s.RFrameRate)
			}
		case "audio":
			res.HasAudio = true
		}
	}
	var dur float64
	if _, err := fmt.Sscanf(doc.Format.Duration, "%f", &dur); err == nil {
		res.DurationMs = int64(dur * 1000)
	}
	return res, nil
}

// ParseFPS parses an ffprobe frame-rate token ("30000/1001", "25/1", or a
// bare float) into frames per second. Returns 0 on malformed input. Exported
// so the admin CLI can pre-probe the source clip's fps and pass it through
// VariantInput.SourceFPS for the renderer's exact-match check.
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

// validate enforces the output contract: readable video, audio present,
// duration within tolerance of the source, non-empty file.
func (r *Renderer) validate(in VariantInput, path string, p *probeResult) error {
	if !p.HasVideo {
		return fmt.Errorf("output has no video stream")
	}
	if !p.HasAudio {
		return fmt.Errorf("output has no audio stream (original audio must be preserved)")
	}
	if p.DurationMs <= 0 {
		return fmt.Errorf("output duration is zero")
	}
	want := in.SourceDuration.Milliseconds()
	if want > 0 {
		drift := p.DurationMs - want
		if drift < 0 {
			drift = -drift
		}
		if drift > 600 {
			return fmt.Errorf("output duration %dms drifts %dms from source %dms", p.DurationMs, drift, want)
		}
	}
	// Resolution + codec: the burn profile scales/pads every output to the
	// canonical ASS PlayRes and encodes h264. A deviation means the render did
	// not honour the profile (e.g. a stray filter or a swapped encoder).
	if p.Width != renderWidth || p.Height != renderHeight {
		return fmt.Errorf("resolution %dx%d != expected %dx%d", p.Width, p.Height, renderWidth, renderHeight)
	}
	if p.VideoCodec != renderVideoCodec {
		return fmt.Errorf("video codec %q != expected %q", p.VideoCodec, renderVideoCodec)
	}
	// FPS: the profile never changes frame rate, so the output must keep the
	// source fps. When SourceFPS is unknown (0) only the sane-range check runs.
	if p.FPS < renderFPSMin || p.FPS > renderFPSMax {
		return fmt.Errorf("fps %.3f outside sane range [%.0f, %.0f]", p.FPS, renderFPSMin, renderFPSMax)
	}
	if in.SourceFPS > 0 {
		if drift := math.Abs(p.FPS - in.SourceFPS); drift/in.SourceFPS > 0.05 {
			return fmt.Errorf("fps %.3f drifts %.2f%% from source %.3f", p.FPS, drift/in.SourceFPS*100, in.SourceFPS)
		}
	}
	// Burn-in presence: the subtitles must contain at least one dialogue line
	// (an empty .ass would silently produce a subtitle-less clip).
	if !assHasDialogue(in.ASSPath) {
		return fmt.Errorf("subtitle burn-in absent: ASS %s has no dialogue lines", in.ASSPath)
	}
	st, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat output: %w", err)
	}
	if st.Size() <= 0 {
		return fmt.Errorf("output file is empty")
	}
	return nil
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

// assHasDialogue verifies the .ass given to the burn has at least one
// Dialogue line with non-empty text. The renderer always applies the
// subtitles filter, so "burn-in present" reduces to "there was text to
// burn": an empty/blank .ass is the one failure mode that produces a
// subtitle-less clip and is caught here instead of trusting the render
// boundary.
func assHasDialogue(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "Dialogue:") {
			continue
		}
		parts := strings.SplitN(line, ",", 10)
		if len(parts) >= 10 && strings.TrimSpace(parts[9]) != "" {
			return true
		}
	}
	return false
}

type publishResult struct {
	FileID      string
	WebViewLink string
	OutputHash  string
	SizeBytes   int64
}

// publish uploads the validated mp4 to the destination Drive folder and
// returns the canonical link + content hash + size.
func (r *Renderer) publish(ctx context.Context, in VariantInput, path string, p *probeResult) (*publishResult, error) {
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

func ffprobePathFor(ffmpegPath string) string {
	base := filepath.Base(ffmpegPath)
	if !strings.HasSuffix(base, "mpeg") {
		return ""
	}
	return filepath.Join(filepath.Dir(ffmpegPath), strings.TrimSuffix(base, "mpeg")+"probe")
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
