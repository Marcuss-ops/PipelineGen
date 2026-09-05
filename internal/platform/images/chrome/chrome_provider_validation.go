// Package images — chrome_provider_validation.go (commit 5, 2026-07):
// post-success validation, dimension decoding, response build,
// and observability log emission for the ChromeImageProvider.
//
// PR-CHROME-PROVIDER-SPLIT (commit 5, July 2026): per godlike/06 SSOT,
// chrome_provider_validation.go is the SINGLE canonical owner of
// "what happens AFTER the worker reported status=ok and the bytes
// are read back". The previous inline block in
// chrome_provider.go::generateOnce (~120 LOC across 3 error
// branches, image.DecodeConfig, visual_validate.ComputeStats, and
// the 16-field zap.Info emission) was a maintainability hazard:
// every audit-trail regression-detection rule had to reason about
// each field inline. Extraction collapses those responsibilities
// into a typed GenerationLogContext struct (single typed shape)
// + 4 named helpers.
//
// GenerationLogContext is the godlike/07 audit-trail SSOT for
// "the post-success observability record". Every regression-
// detection rule that wants to verify "did the worker report
// validation parity" must consult this struct's fields.
//
// godlike/06 SSOT (thinker-ratified, fix #7): the 16+ field
// signature MUST NOT be expressed as a flat arg list. Using a
// struct keeps the type system honest about which fields are
// required for which audit story and collapses the maintenance
// surface area for "fields that change together".
package chrome

import (
	"bytes"
	"fmt"
	imggeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/generation"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/images/chrome/visual_validate"
	"image"
	"math"

	"go.uber.org/zap"
)

// ComposedPrompt mirrors the internal shape returned by
// internal/capabilities/images/generation/prompt.go::imggeneration.ComposePrompt.
// We declare a local alias instead of importing the type because
// this file's only use is field-access for the post-success
// observability log; importing the concrete struct would force
// every test in this file to depend on the composer's lifecycle
// for no real benefit.
//
// godlike/06 SSOT: the field names here MUST stay byte-byte
// aligned with the prompt_composer.go return value.
type ComposedPrompt struct {
	Composed      string
	ComposedLen   int
	StyleAffix    string
	NegativeAffix string
	WasCompressed bool
}

// GenerationLogContext bundles the post-success observability
// fields. Constructed by generateOnce; passed by-value to
// logGenerationDiagnostics.
//
// godlike/07 audit-trail SSOT: this struct is the canonical
// shape for "the per-request generation log line". Every field
// is required by at least one regression-detection rule (P2
// audit, ratio-drift detector, white_pct fail-closed etc.) —
// adding a field requires a paired audit-rule update.
type GenerationLogContext struct {
	// Identity.
	RequestID    string
	GenerationID string

	// Composition audit (P1.2).
	Prompt           string
	Style            string
	RawPromptLen     int
	ComposedLen      int
	StyleAffixLen    int
	NegativeAffixLen int
	ComposedDirty    bool

	// Result dimensions.
	Bytes             int
	ReqWidth          int
	ReqHeight         int
	RealWidth         int
	RealHeight        int
	RatioMatch        bool
	NaturalW          int
	NaturalH          int
	CandidateComplete bool
	ElapsedMS         int64

	// Candidate-side diagnostics (P2 replication).
	Method             string
	CandidatesBaseline int
	CandidatesAfter    int
	CandidatesReported int
	ImageModeActive    bool
	RatioSelected      string
	PromptOriginal     string
	PromptDOM          string
	ScreenshotPath     string

	// Worker stats (canonical primary source from PIL pass).
	WorkerPhashHex    string
	WorkerWhitePct    float64
	WorkerVariance    float64
	WorkerEdgeDensity float64

	// Go-side recompute (cross-validation).
	GoRecomputePhashHex string
	GoWhitePct          float64
	GoVariance          float64
	GoEdgeDensity       float64

	// Parity (THE shape-check; bit-equality is intentionally NOT asserted
	// because worker and Go use different sampling strides).
	PhashParityOK  bool
	ComputeStatsOK bool
}

// validateGeneratedOutput runs the post-extraction content
// validator. FAIL-CLOSED on blank/placeholder: the file is
// removed AND the canonical images.ErrImageGenBlankOrPlaceholder
// sentinel is wrapped so callers (retry policy, audit logs,
// smoke tests) can errors.Is-probe the typed sentinel surface.
//
// godlike/07 contract (P0.2, July 2026): the worker claimed
// status=ok with valid bytes; we cannot trust those bytes without
// content validation. The worker could have surfaced a slide-export
// from a stale panel, an old blob: with no real content, or a
// near-white render the model didn't reject.
func (p *ChromeImageProvider) validateGeneratedOutput(outputPath string, style string, requestID string) error {
	if valErr := visual_validate.Validate(outputPath, style); valErr != nil {
		p.cleanupFailedOutput(outputPath, requestID)
		return fmt.Errorf("%w (validator: %v)", imggeneration.ErrImageGenBlankOrPlaceholder, valErr)
	}
	return nil
}

// decodeGeneratedDimensions returns the real image dimensions
// (decoded from the PNG header) plus a ratioMatch boolean that
// compares against the requested width/height using a 5% tolerance.
//
// godlike/07 audit-trail: the previous ChromeImageProvider
// reported req.Width/Height — a lie when the worker produced e.g.
// a 1280x720 image despite a 1920x1080 request. Real dims are now
// what imggeneration.GeneratedImage.Width/Height carries; the requested w/h are
// preserved in the log line so operators can audit
// requested-vs-actual ratio drift without code changes.
func decodeGeneratedDimensions(data []byte, reqWidth, reqHeight int) (int, int, bool) {
	realW, realH := reqWidth, reqHeight
	if cfg, _, decErr := image.DecodeConfig(bytes.NewReader(data)); decErr == nil {
		realW, realH = cfg.Width, cfg.Height
	}
	ratioMatch := true
	if reqWidth > 0 && reqHeight > 0 && realW > 0 && realH > 0 {
		reqRatio := float64(reqWidth) / float64(reqHeight)
		realRatio := float64(realW) / float64(realH)
		ratioMatch = math.Abs(reqRatio-realRatio) < 0.05
	}
	return realW, realH, ratioMatch
}

// buildGeneratedImage returns the canonical typed envelope the
// downstream ingestion layer consumes (clipindexer + Qdrant
// projection). SourceHash uses REAL dims (so a 1920x1080 request
// that comes back 1280x720 reuses the same hash as a direct
// 1280x720 request — downstream ingestion is dim-correct).
//
// godlike/06 SSOT: this function is the SINGLE typed-builder for
// the success-path envelope. The compose-prompt audit fields are
// NOT included here on purpose — they live in the
// GenerationLogContext struct consumed by logGenerationDiagnostics.
func buildGeneratedImage(data []byte, outputPath string, req imggeneration.GenerateImageRequest, realW, realH int, sourceHash string, format string) *imggeneration.GeneratedImage {
	return &imggeneration.GeneratedImage{
		Data:       data,
		Format:     format,
		Width:      realW,
		Height:     realH,
		PromptUsed: req.Prompt,
		Provider:   "google-slides",
		SourceHash: sourceHash,
		OutputPath: outputPath,
	}
}

// logGenerationDiagnostics emits the canonical Zap log line for
// every successful Generate. godlike/07 observability revision
// (P2 review, July 2026): the pre-fix strict parity check was
// misleading because Go's pHash uses full-iteration step-bounds
// sampling while the Python worker's _compute_pixel_stats uses a
// 16-stride pre-sample then 8x8 downsample — the two routines do
// NOT land on the same physical pixels for non-trivial images,
// so bit-equality would surface as phash_parity_ok=false for every
// real image even when both sides are canonical-correct. The
// post-fix contract is a SHAPE check via isWellFormedPhashHex;
// operators can still eyeball the two hex strings in audit logs
// to detect a tampered extraction.
//
// godlike/06 SSOT (single-typed-struct arg): this signature
// replaces the pre-extraction 16-arg signature which was
// unmaintainable. Adding a new observability field requires
// updating GenerationLogContext + the log call site — both
// visible at the type level.
func (p *ChromeImageProvider) logGenerationDiagnostics(ctx GenerationLogContext) {
	p.log.Info("ChromeImageProvider: generated image",
		zap.String("request_id", ctx.RequestID),
		zap.String("generation_id", ctx.GenerationID),
		zap.String("method", ctx.Method),
		zap.String("style", ctx.Style),
		zap.String("prompt", ctx.Prompt),
		zap.Int("prompt_raw_len", ctx.RawPromptLen),
		zap.Int("composed_prompt_len", ctx.ComposedLen),
		zap.Int("composed_prompt_style_affix_len", ctx.StyleAffixLen),
		zap.Int("composed_prompt_negative_affix_len", ctx.NegativeAffixLen),
		zap.Bool("composed_prompt_dirty", ctx.ComposedDirty),
		zap.Int("bytes", ctx.Bytes),
		zap.Int("req_width", ctx.ReqWidth),
		zap.Int("req_height", ctx.ReqHeight),
		zap.Int("real_width", ctx.RealWidth),
		zap.Int("real_height", ctx.RealHeight),
		zap.Bool("ratio_match", ctx.RatioMatch),
		zap.Int("natural_w", ctx.NaturalW),
		zap.Int("natural_h", ctx.NaturalH),
		zap.Bool("candidate_complete", ctx.CandidateComplete),
		zap.Int64("elapsed_ms", ctx.ElapsedMS),

		// P2 diagnostic replication (worker.primary, Go.recompute).
		zap.Int("candidates_baseline", ctx.CandidatesBaseline),
		zap.Int("candidates_after", ctx.CandidatesAfter),
		zap.Int("candidates_reported", ctx.CandidatesReported),
		zap.Bool("image_mode_active", ctx.ImageModeActive),
		zap.String("ratio_selected", ctx.RatioSelected),
		zap.String("prompt_original", ctx.PromptOriginal),
		zap.String("prompt_dom", ctx.PromptDOM),
		zap.String("screenshot_path", ctx.ScreenshotPath),

		// Worker-side PIL stats (canonical primary source).
		zap.String("worker_phash_hex", ctx.WorkerPhashHex),
		zap.Float64("worker_white_pct", ctx.WorkerWhitePct),
		zap.Float64("worker_variance", ctx.WorkerVariance),
		zap.Float64("worker_edge_density", ctx.WorkerEdgeDensity),

		// Go-side recompute (cross-validation).
		zap.String("go_phash_hex", ctx.GoRecomputePhashHex),
		zap.Float64("go_white_pct", ctx.GoWhitePct),
		zap.Float64("go_variance", ctx.GoVariance),
		zap.Float64("go_edge_density", ctx.GoEdgeDensity),

		// Shape-parity flag.
		zap.Bool("phash_parity_ok", ctx.PhashParityOK),
		zap.Bool("compute_stats_ok", ctx.ComputeStatsOK),
	)
}

// isWellFormedPhashHex is the shape-only check used by the
// phash_parity_ok field on the success-path log. Returns true
// when s is exactly 16 chars of lowercase hex (the canonical
// encoding emitted by fmt.Sprintf("%016x", uint64) on both the
// worker side and the Go ComputeStats side).
//
// godlike/07 observability: this is NOT a bit-equality check.
// The worker and Go use different sampling strides, so the two
// pHash uint64 values rarely coincide. The parity field is a
// cheap "is the diagnostic shape sane?" smoke detector;
// bit-level tampering detection is left to operators
// inspecting the two hex strings in the audit log.
func isWellFormedPhashHex(s string) bool {
	if len(s) != 16 {
		return false
	}
	for i := 0; i < 16; i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		default:
			return false
		}
	}
	return true
}

// computeGenerationLogContext constructs the canonical
// post-success observability struct from the typed inputs +
// the worker's resp + the Go-recomputed pixel stats. This is
// a build-phase helper; the emission is owned by
// logGenerationDiagnostics.
//
// godlike/06 SSOT: this is THE canonical builder for the
// observability struct. Splitting field-computation across
// multiple helpers risks field-drift between computed and
// emitted — keeping the build here keeps the two surfaces
// synchronized.
func computeGenerationLogContext(
	requestID, generationID string,
	req imggeneration.GenerateImageRequest,
	composed ComposedPrompt,
	resp *workerResponse,
	data []byte,
	outputPath string,
	realW, realH int,
	ratioMatch bool,
) (GenerationLogContext, error) {
	pixelStats, statsErr := visual_validate.ComputeStats(outputPath)
	workerPhash := resp.PhashHex
	goRecomputePhash := ""
	phashParityOK := false
	if statsErr == nil {
		goRecomputePhash = pixelStats.PHashHex
		phashParityOK = isWellFormedPhashHex(workerPhash) && isWellFormedPhashHex(goRecomputePhash)
	}

	format := "png"
	var goWhitePct, goVariance, goEdgeDensity float64
	if statsErr == nil {
		goWhitePct = pixelStats.WhitePct
		goVariance = pixelStats.Variance
		goEdgeDensity = pixelStats.EdgeDensity
	}
	_ = format // format is consumed by buildGeneratedImage externally.

	return GenerationLogContext{
		RequestID:           requestID,
		GenerationID:        generationID,
		Prompt:              req.Prompt,
		Style:               req.Style,
		RawPromptLen:        len(req.Prompt),
		ComposedLen:         composed.ComposedLen,
		StyleAffixLen:       len(composed.StyleAffix),
		NegativeAffixLen:    len(composed.NegativeAffix),
		ComposedDirty:       composed.WasCompressed,
		Bytes:               len(data),
		ReqWidth:            req.Width,
		ReqHeight:           req.Height,
		RealWidth:           realW,
		RealHeight:          realH,
		RatioMatch:          ratioMatch,
		NaturalW:            resp.NaturalW,
		NaturalH:            resp.NaturalH,
		CandidateComplete:   resp.Complete,
		ElapsedMS:           resp.ElapsedMS,
		Method:              resp.Method,
		CandidatesBaseline:  resp.CandidatesBaseline,
		CandidatesAfter:     resp.CandidatesAfter,
		CandidatesReported:  len(resp.Candidates),
		ImageModeActive:     resp.ImageModeActive,
		RatioSelected:       resp.RatioSelected,
		PromptOriginal:      resp.PromptOriginal,
		PromptDOM:           resp.PromptDOM,
		ScreenshotPath:      resp.ScreenshotPath,
		WorkerPhashHex:      workerPhash,
		WorkerWhitePct:      resp.WhitePct,
		WorkerVariance:      resp.Variance,
		WorkerEdgeDensity:   resp.EdgeDensity,
		GoRecomputePhashHex: goRecomputePhash,
		GoWhitePct:          goWhitePct,
		GoVariance:          goVariance,
		GoEdgeDensity:       goEdgeDensity,
		PhashParityOK:       phashParityOK,
		ComputeStatsOK:      statsErr == nil,
	}, statsErr
}
