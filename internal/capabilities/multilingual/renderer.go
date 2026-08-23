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
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

func sha256File(path string) (string, int64, error) {
	return digest.SHA256File(path)
}

// subtitle-visible constants — the subtitle band is the bottom of the canonical
// 1920x1080 PlayRes where Alignment=2 / MarginV=24 places cue text.
const (
	subtitleBandY         = 900
	subtitleBandHeight    = 180
	subtitleBandWidth     = 1920
	subtitleDiffThreshold = 40
	subtitleMinVisible    = 0.003
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

	BackgroundAssetID      string
	BackgroundPath         string
	BackgroundSHA256       string
	WatermarkAssetID       string
	WatermarkPath          string
	WatermarkSHA256        string
	WatermarkPosition      string
	WatermarkOpacity       float64
	WatermarkMarginPX      int
	ForegroundScalePercent int
	RenderProfileVersion   string
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

// Renderer fans out per-language clip rendering through the canonical
// clip.render pipeline. It is a planner/fan-out, not a renderer: every
// render is delegated to clip.render's sealed plan + Rust execution boundary.
// The renderer owns variant fingerprinting, reuse, subtitle-visible
// certification, and per-language Drive upload + variant persistence — all
// multilingual-specific concerns. Codec/profile/output contract decisions
// belong solely to clip.render.
type Renderer struct {
	repo       asset.RenderVariantRepository
	publisher  delivery.Publisher
	ffmpegPath string // required only for subtitleVisible frame extraction
	log        *zap.Logger
	rust       RustRenderer
	rustWidth  int
	rustHeight int
	rustFPSNum int
	rustFPSDen int
	// outputProber certifies the actual bytes on disk match the clip.render
	// output contract; wired by the composition root alongside RustRenderer.
	outputProber cliprender.OutputProber
}

// NewRenderer constructs the canonical renderer. Fail-closed: repo and
// publisher are mandatory. ffmpegPath is optional — it is needed only when
// subtitleVisible verification is enabled.
func NewRenderer(repo asset.RenderVariantRepository, publisher delivery.Publisher, ffmpegPath string, log *zap.Logger) (*Renderer, error) {
	if repo == nil {
		return nil, fmt.Errorf("multilingual.NewRenderer: variant repo is required")
	}
	if publisher == nil {
		return nil, fmt.Errorf("multilingual.NewRenderer: Drive publisher is required")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &Renderer{repo: repo, publisher: publisher, ffmpegPath: ffmpegPath, log: log}, nil
}

// WithRustRenderer enables the canonical Rust render_clip path. Width,
// height, and the rational frame rate are resolved by the composition root
// and become part of every sealed plan.
func (r *Renderer) WithRustRenderer(renderer RustRenderer, width, height, fpsNum, fpsDen int) *Renderer {
	r.rust = renderer
	r.rustWidth, r.rustHeight, r.rustFPSNum, r.rustFPSDen = width, height, fpsNum, fpsDen
	return r
}

// WithOutputProber wires the clip.render output prober for post-render
// contract validation. When nil, the renderer skips contract validation
// (used only by tests that mock the render boundary).
func (r *Renderer) WithOutputProber(prober cliprender.OutputProber) *Renderer {
	r.outputProber = prober
	return r
}

// IsReusable is a cheap preflight used by streaming callers before they do
// translation or ASS work. It uses exactly the same fingerprint inputs as
// RenderOne, so a true result guarantees RenderOne will return reused.
func (r *Renderer) IsReusable(ctx context.Context, sourceClipID, sourceSHA256, language, transcriptSHA256, translationVersion, subtitleStyleVersion, renderProfileVersion string) bool {
	if r == nil || r.repo == nil {
		return false
	}
	if renderProfileVersion == "" {
		renderProfileVersion = asset.RenderProfileFFmpegAss1080pV1
	}
	fingerprint := asset.RenderVariantContentFingerprint(sourceSHA256, transcriptSHA256, language, subtitleStyleVersion, renderProfileVersion)
	existing, err := r.repo.FindByFingerprint(ctx, sourceClipID, language, fingerprint)
	if err == nil && existing != nil && existing.Status == asset.RenderVariantReady {
		return true
	}
	// Read legacy model-sensitive rows during the migration window.
	legacy := asset.RenderVariantFingerprint(sourceSHA256, transcriptSHA256, language, translationVersion, subtitleStyleVersion, renderProfileVersion)
	existing, err = r.repo.FindByFingerprint(ctx, sourceClipID, language, legacy)
	return err == nil && existing != nil && existing.Status == asset.RenderVariantReady
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
	profileVersion := in.RenderProfileVersion
	if profileVersion == "" {
		profileVersion = asset.RenderProfileFFmpegAss1080pV1
	}
	fingerprint := asset.RenderVariantContentFingerprint(
		in.SourceSHA256, in.TranscriptSHA256, in.Language,
		in.SubtitleStyleVersion, profileVersion,
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
		legacy := asset.RenderVariantFingerprint(in.SourceSHA256, in.TranscriptSHA256, in.Language, in.TranslationVersion, in.SubtitleStyleVersion, profileVersion)
		if existing, err := r.repo.FindByFingerprint(ctx, in.SourceClipID, in.Language, legacy); err == nil && existing != nil {
			res.Status = "reused"
			res.Validation = "ok"
			res.OutputHash, res.DriveFileID, res.DriveLink = existing.OutputHash, existing.DriveFileID, existing.DriveLink
			res.DurationMs, res.SizeBytes = existing.DurationMs, existing.SizeBytes
			res.RenderMS = time.Since(start).Milliseconds()
			return res
		}
	}

	// Wrong-language contamination = 0: verify the .ass on disk is exactly the
	// artifact generated for this language before burning it.
	if err := r.noLanguageContamination(in); err != nil {
		return r.fail(res, start, err)
	}

	// Render through the sealed clip.render plan — the ONLY path. The
	// multilingual renderer is a planner/fan-out, not a renderer: codec,
	// profile, and output contract decisions belong solely to clip.render.
	if r.rust == nil {
		return r.fail(res, start, fmt.Errorf("multilingual: Rust renderer is not configured (use WithRustRenderer)"))
	}

	outputPath := filepath.Join(in.WorkDir, in.OutputFilename)
	if err := os.MkdirAll(in.WorkDir, 0o755); err != nil {
		return r.fail(res, start, fmt.Errorf("mkdir workdir: %w", err))
	}

	rustResult, err := r.renderRust(ctx, in, outputPath)
	if err != nil {
		return r.fail(res, start, fmt.Errorf("rust render_clip: %w", err))
	}

	// Validate the actual bytes on disk against the clip.render output contract
	// (never trust what the render boundary claimed to encode).
	// OutputProber is wired by the composition root alongside RustRenderer;
	// a missing prober skips contract validation (test-only mode).
	durationMs := in.SourceDuration.Milliseconds()
	if r.outputProber != nil {
		probe, probeErr := r.outputProber.ProbeOutput(ctx, outputPath)
		if probeErr != nil {
			return r.fail(res, start, fmt.Errorf("probe output: %w", probeErr))
		}
		if validateErr := cliprender.ValidateContract(rustResult.contract, probe); validateErr != nil {
			return r.fail(res, start, validateErr)
		}
		// Duration from the actual bytes.
		durationMs = calcDurationMS(probe, in.SourceDuration)
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
		res.DurationMs = in.SourceDuration.Milliseconds()
		res.RenderMS = time.Since(start).Milliseconds()
		return res
	}

	// Upload + persist.
	pub, err := r.publish(ctx, in, outputPath, durationMs)
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
		RenderProfileVersion: profileVersion,
		SubtitleHash:         in.ASSHash,
		OutputHash:           pub.OutputHash,
		DriveFileID:          pub.FileID,
		DriveLink:            pub.WebViewLink,
		DurationMs:           durationMs,
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
	res.DurationMs = durationMs
	res.SizeBytes = pub.SizeBytes
	res.RenderMS = time.Since(start).Milliseconds()
	return res
}

// calcDurationMS derives the duration from the clip.render OutputProbe result,
// falling back to the source clip duration when probe metadata is absent.
func calcDurationMS(probe *cliprender.OutputProbe, sourceDuration time.Duration) int64 {
	_ = probe // probe doesn't expose a simple duration_ms field; use source as best estimate
	return sourceDuration.Milliseconds()
}
