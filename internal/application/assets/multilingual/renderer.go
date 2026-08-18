// Package multilingual owns the idempotent per-language clip render fan-out:
// for a canonical source transcript + per-language .ass artifacts, burn the
// subtitles into independent per-language videos with ffmpeg, validate each
// output with ffprobe, upload to Drive, and persist a fingerprinted variant
// row (asset_render_variants) so a re-run reuses completed outputs.
//
// The fan-out starts ONLY after the canonical transcript exists (single
// source of truth): translation, cue alignment, and ASS generation happen
// upstream (texttracks); this package renders + validates + publishes.
package multilingual

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

// The canonical ffmpeg burn-in output profile (single owner): burn() and
// validate() MUST stay in sync on these values. The burn scales/pads every
// output to the canonical ASS PlayRes (1920x1080) so fonts stay legible,
// encodes h264/yuv420p, and copies the original audio bit-exact.
const (
	renderWidth      = 1920
	renderHeight     = 1080
	renderVideoCodec = "h264"
	renderFPSMin     = 1.0
	renderFPSMax     = 240.0

	// Subtitle burn-in verification (subtitle-visible): the subtitle band is
	// the bottom of the canonical 1920x1080 PlayRes where Alignment=2 /
	// MarginV=24 places cue text. The check samples one frame at a cue
	// timestamp and requires a minimum fraction of the band to differ from
	// the un-subtitled source frame at the same timestamp. Compression noise
	// stays far below the threshold; burned text (bright glyph + dark
	// outline) crosses it.
	subtitleBandY         = 900
	subtitleBandHeight    = 180
	subtitleBandWidth     = renderWidth
	subtitleDiffThreshold = 40    // gray-level delta that signals burned text
	subtitleMinVisible    = 0.003 // min fraction of band pixels that must differ
)

// VariantInput is the fully-resolved input for one language render. Every
// value is resolved upstream; the renderer makes no business selections.
type VariantInput struct {
	SourceClipID   string
	SourcePath     string // local source clip path
	SourceSHA256   string // source clip content hash (source_clip_sha256)
	SourceDuration time.Duration
	Language       string
	// Priority is the deterministic language order (source=0, targets=1..N).
	// The report is emitted in this order regardless of render completion order.
	Priority int
	// TextReadyAt is when this language's localized text (transcript or
	// translation) became ready — the moment its render could legally start.
	// Used to certify that a higher-priority language starts rendering before
	// a lower-priority language's text is even ready (no global barrier).
	TextReadyAt          time.Time
	TranscriptSHA256     string // hash of the text being subtitled for this language (track.TextHash)
	TranslationVersion   string // translated track model/version fingerprint
	SubtitleStyleVersion string // ASS style + generator version
	ASSPath              string
	ASSHash              string
	OutputFilename       string // e.g. "<base>.<lang>.mp4"
	DriveFolderID        string
	WorkDir              string // scratch dir for the rendered mp4

	// SourceFPS is the source clip's frame rate, used to verify the output
	// kept the source fps (the burn profile never changes frame rate). Zero
	// disables the exact-match check and leaves only the sane-range check.
	SourceFPS float64

	// Force bypasses the fingerprint reuse check (benchmark / cold-run mode:
	// always re-run ffmpeg even when a completed variant exists).
	Force bool
	// SkipPublish renders + validates locally without uploading to Drive or
	// persisting a variant row (benchmark mode).
	SkipPublish bool
}

// RustRenderResult is the media fact projection returned by the canonical
// Rust render_clip boundary. The multilingual fan-out deliberately depends
// on this small result contract, not on an FFmpeg command line.
type RustRenderResult struct {
	OutputPath string
}

// RustRenderer is the only execution seam used by the multilingual renderer
// when configured. The plan is already sealed before this method is called.
type RustRenderer interface {
	RenderClip(context.Context, cliprender.ClipRenderPlanV1) (RustRenderResult, error)
}

// VariantResult is the per-language outcome, one row in the final report.
type VariantResult struct {
	Language     string `json:"language"`
	Status       string `json:"status"` // ready | reused | failed
	Fingerprint  string `json:"fingerprint"`
	SubtitleHash string `json:"subtitle_hash"`
	OutputHash   string `json:"output_hash"`
	DriveFileID  string `json:"drive_file_id,omitempty"`
	DriveLink    string `json:"drive_link,omitempty"`
	DurationMs   int64  `json:"duration_ms"`
	SizeBytes    int64  `json:"size_bytes"`
	RenderMS     int64  `json:"render_ms"`
	QueueMS      int64  `json:"queue_ms"` // queued → started (pool saturation latency)
	WorkerID     int    `json:"worker_id"`
	// Per-language lifecycle timestamps (RFC3339). Priority + TextReadyAt are
	// copied from the input; queued/started/completed are recorded by the
	// render pool; upload_completed_at is set when the Drive upload lands.
	Priority          int       `json:"priority"`
	TextReadyAt       time.Time `json:"text_ready_at"`
	QueuedAt          time.Time `json:"queued_at"`
	RenderStartedAt   time.Time `json:"render_started_at"`
	RenderCompletedAt time.Time `json:"render_completed_at"`
	UploadCompletedAt time.Time `json:"upload_completed_at"`
	// Validation is the post-render output-contract verdict: "ok" on success,
	// or the first failing check (streams / duration / resolution / fps /
	// codec / burn-in / size) when the contract is violated.
	Validation string `json:"validation"`
	Error      string `json:"error,omitempty"`
}

// RenderReport is the aggregate fan-out result.
type RenderReport struct {
	Variants []VariantResult `json:"variants"`
	// Concurrency is the reconstructed real parallelism of the render fan-out.
	Concurrency observability.ConcurrencyStats `json:"concurrency"`
}

// Renderer burns subtitles into per-language videos, validates, uploads, and
// persists fingerprinted variants. Safe for concurrent RenderOne calls; the
// upstream DB/Drive ports must be concurrency-safe.
type Renderer struct {
	repo       asset.RenderVariantRepository
	publisher  delivery.Publisher
	ffmpegPath string
	log        *zap.Logger
	rust       RustRenderer
	rustWidth  int
	rustHeight int
	rustFPS    int
}

// NewRenderer constructs the canonical renderer. Fail-closed: repo and
// publisher are mandatory.
func NewRenderer(repo asset.RenderVariantRepository, publisher delivery.Publisher, ffmpegPath string, log *zap.Logger) (*Renderer, error) {
	if repo == nil {
		return nil, fmt.Errorf("multilingual.NewRenderer: variant repo is required")
	}
	if publisher == nil {
		return nil, fmt.Errorf("multilingual.NewRenderer: Drive publisher is required")
	}
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &Renderer{repo: repo, publisher: publisher, ffmpegPath: ffmpegPath, log: log}, nil
}

// WithRustRenderer enables the canonical Rust render_clip path. Width,
// height, and fps are resolved by the composition root and become part of
// every sealed plan. It is intentionally opt-in here so existing unit tests
// can continue to exercise the renderer with their fake FFmpeg boundary.
func (r *Renderer) WithRustRenderer(renderer RustRenderer, width, height, fps int) *Renderer {
	r.rust = renderer
	r.rustWidth, r.rustHeight, r.rustFPS = width, height, fps
	return r
}

// RenderAll fans out across languages with a bounded concurrency. Order of
// the returned variants matches the input order (deterministic report). It is
// a convenience wrapper over RenderPool for callers that already hold all
// inputs; streaming callers use NewRenderPool/Submit/Wait directly so a
// language starts rendering as soon as its ASS is ready.
func (r *Renderer) RenderAll(ctx context.Context, inputs []VariantInput, concurrency int) (*RenderReport, error) {
	if r == nil {
		return nil, fmt.Errorf("multilingual: renderer is nil")
	}
	if len(inputs) == 0 {
		return &RenderReport{Variants: []VariantResult{}}, nil
	}
	pool := r.NewRenderPool(ctx, concurrency)
	for _, in := range inputs {
		pool.Submit(in)
	}
	return pool.Wait(), nil
}

// RenderPool is a bounded streaming render fan-out. Languages are submitted
// as their ASS becomes ready (source first, then targets), so a language
// starts rendering as soon as it is ready instead of waiting behind a global
// "translate-all + ass-all" barrier. Results are returned in submission
// (priority) order, never completion order, so the report stays deterministic
// even when a lower-priority language finishes first.
type RenderPool struct {
	r          *Renderer
	g          *errgroup.Group
	ctx        context.Context
	tracker    *observability.ConcurrencyTracker
	configured int
	mu         sync.Mutex
	results    []VariantResult
}

// NewRenderPool starts a render pool bounded to concurrency workers.
func (r *Renderer) NewRenderPool(ctx context.Context, concurrency int) *RenderPool {
	if concurrency < 1 {
		concurrency = 1
	}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(concurrency)
	return &RenderPool{
		r: r, g: g, ctx: gctx, tracker: &observability.ConcurrencyTracker{}, configured: concurrency,
	}
}

// Submit enqueues one language variant in priority order. The submission index
// is the language's deterministic priority slot; the worker records the
// per-language timing (queued/started/completed) and stores the result in the
// same slot. Per-language errors are recorded on the result, never aborting
// the pool. Returns the submission index.
func (p *RenderPool) Submit(in VariantInput) int {
	p.mu.Lock()
	idx := len(p.results)
	p.results = append(p.results, VariantResult{})
	p.mu.Unlock()

	queuedAt := time.Now()
	p.g.Go(func() error {
		startedAt := time.Now()
		res := p.r.RenderOne(p.ctx, in)
		completedAt := time.Now()
		p.tracker.Record(observability.OpTiming{
			Operation:   "render",
			ID:          in.Language,
			WorkerID:    idx,
			QueuedAt:    queuedAt,
			StartedAt:   startedAt,
			CompletedAt: completedAt,
		})
		res.Priority = in.Priority
		res.TextReadyAt = in.TextReadyAt
		res.QueuedAt = queuedAt
		res.RenderStartedAt = startedAt
		res.QueueMS = startedAt.Sub(queuedAt).Milliseconds()
		if res.QueueMS < 0 {
			res.QueueMS = 0
		}
		res.WorkerID = idx
		p.mu.Lock()
		p.results[idx] = res
		p.mu.Unlock()
		return nil
	})
	return idx
}

// Wait blocks until all submitted renders complete and returns the report in
// submission (priority) order.
func (p *RenderPool) Wait() *RenderReport {
	_ = p.g.Wait()
	p.mu.Lock()
	variants := append([]VariantResult(nil), p.results...)
	p.mu.Unlock()
	return &RenderReport{Variants: variants, Concurrency: p.tracker.Stats(p.configured)}
}

// RenderOne renders a single language variant with full idempotency:
// fingerprint → reuse check → ffmpeg burn → ffprobe validate → upload → persist.
func (r *Renderer) RenderOne(ctx context.Context, in VariantInput) VariantResult {
	start := time.Now()
	res := VariantResult{
		Language:     in.Language,
		SubtitleHash: in.ASSHash,
	}
	fingerprint := asset.RenderVariantFingerprint(
		in.SourceSHA256, in.TranscriptSHA256, in.Language,
		in.TranslationVersion, in.SubtitleStyleVersion, asset.RenderProfileFFmpegAss1080pV1,
	)
	res.Fingerprint = fingerprint

	// Reuse: an existing READY variant with the same fingerprint is the final
	// output — no translate, no ASS, no render, no upload. Skipped in Force
	// mode (benchmark measures the real render cost every run).
	if !in.Force {
		if existing, err := r.repo.FindByFingerprint(ctx, in.SourceClipID, in.Language, fingerprint); err == nil && existing != nil {
			res.Status = "reused"
			res.Validation = "ok" // validated + persisted on first creation
			res.OutputHash = existing.OutputHash
			res.DriveFileID = existing.DriveFileID
			res.DriveLink = existing.DriveLink
			res.DurationMs = existing.DurationMs
			res.SizeBytes = existing.SizeBytes
			res.RenderMS = time.Since(start).Milliseconds()
			return res
		}
	}

	// Wrong-language contamination = 0: verify the .ass on disk is exactly the
	// artifact generated for this language before burning it.
	if err := r.noLanguageContamination(in); err != nil {
		return r.fail(res, start, err)
	}

	outputPath := filepath.Join(in.WorkDir, in.OutputFilename)
	if err := os.MkdirAll(in.WorkDir, 0o755); err != nil {
		return r.fail(res, start, fmt.Errorf("mkdir workdir: %w", err))
	}

	// Render through the sealed Rust plan when the composition root wires it.
	// The legacy FFmpeg path remains available only for isolated older tests;
	// production multilingual composition uses WithRustRenderer.
	if r.rust != nil {
		if err := r.renderRust(ctx, in, outputPath); err != nil {
			return r.fail(res, start, fmt.Errorf("rust render_clip: %w", err))
		}
	} else if err := r.burn(ctx, in.SourcePath, in.ASSPath, outputPath); err != nil {
		return r.fail(res, start, fmt.Errorf("ffmpeg burn: %w", err))
	}

	// Validate the actual bytes on disk (never trust the render boundary).
	probe, err := r.probe(ctx, outputPath)
	if err != nil {
		return r.fail(res, start, fmt.Errorf("ffprobe: %w", err))
	}
	if err := r.validate(in, outputPath, probe); err != nil {
		return r.fail(res, start, err)
	}
	// Subtitle-visible: verify the subtitles are actually burned into the
	// pixels (not just present in the .ass). Skipped in benchmark (SkipPublish)
	// mode, which measures pure render cost.
	if !in.SkipPublish {
		if err := r.subtitleVisible(ctx, in, outputPath); err != nil {
			return r.fail(res, start, err)
		}
	}
	// Render (burn + validate) is done; the bytes are final. Upload + persist
	// follow, so render_completed_at < upload_completed_at.
	res.RenderCompletedAt = time.Now()

	// Benchmark mode: stop at the validated local bytes (no Drive upload, no
	// variant persistence). The fingerprint + output hash + size are still
	// reported so the caller can compare across concurrency levels.
	if in.SkipPublish {
		hash, size, err := sha256File(outputPath)
		if err != nil {
			return r.fail(res, start, fmt.Errorf("hash output: %w", err))
		}
		res.Status = "ready"
		res.Validation = "ok"
		res.OutputHash = hash
		res.SizeBytes = size
		res.DurationMs = probe.DurationMs
		res.RenderMS = time.Since(start).Milliseconds()
		return res
	}

	// Upload + persist.
	pub, err := r.publish(ctx, in, outputPath, probe)
	if err != nil {
		return r.fail(res, start, err)
	}
	res.UploadCompletedAt = time.Now()

	variant := &asset.RenderVariant{
		SourceClipID:         in.SourceClipID,
		LanguageCode:         in.Language,
		Fingerprint:          fingerprint,
		SourceClipSHA256:     in.SourceSHA256,
		TranscriptSHA256:     in.TranscriptSHA256,
		TranslationVersion:   in.TranslationVersion,
		SubtitleStyleVersion: in.SubtitleStyleVersion,
		RenderProfileVersion: asset.RenderProfileFFmpegAss1080pV1,
		SubtitleHash:         in.ASSHash,
		OutputHash:           pub.OutputHash,
		DriveFileID:          pub.FileID,
		DriveLink:            pub.WebViewLink,
		DurationMs:           probe.DurationMs,
		SizeBytes:            pub.SizeBytes,
		Status:               asset.RenderVariantReady,
		IsCurrent:            true,
	}
	if err := r.repo.Upsert(ctx, variant); err != nil {
		return r.fail(res, start, fmt.Errorf("persist variant: %w", err))
	}

	res.Status = "ready"
	res.Validation = "ok"
	res.OutputHash = pub.OutputHash
	res.DriveFileID = pub.FileID
	res.DriveLink = pub.WebViewLink
	res.DurationMs = probe.DurationMs
	res.SizeBytes = pub.SizeBytes
	res.RenderMS = time.Since(start).Milliseconds()
	return res
}

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
			Width:    width, Height: height, FPS: fps,
		},
	}
	request.Normalize()
	contract, err := cliprender.NewContractResolver().Resolve(ctx, request)
	if err != nil {
		return fmt.Errorf("resolve output contract: %w", err)
	}
	plan, err := cliprender.Compile(cliprender.CompileInput{
		RunID:      in.SourceClipID + ":" + in.Language + ":" + in.Fingerprint,
		Source:     &cliprender.MaterializedAsset{AssetID: in.SourceClipID, LocalPath: in.SourcePath, SHA256: in.SourceSHA256},
		Subtitles:  &cliprender.SubtitleArtifact{LocalPath: in.ASSPath, SHA256: in.ASSHash, Mode: cliprender.SubtitlesModeBurn, StyleID: in.SubtitleStyleVersion},
		Contract:   contract,
		AudioMode:  cliprender.AudioModeCopyIfCompatible,
		OutputPath: outputPath,
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
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "20", "-pix_fmt", "yuv420p",
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
	if ffmpegPath == "" || ffmpegPath == "ffmpeg" {
		return "ffprobe"
	}
	return filepath.Join(filepath.Dir(ffmpegPath), "ffprobe")
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func sha256File(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
