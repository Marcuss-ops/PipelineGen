package ollama

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Marcuss-ops/PipelineGen/pkg/jsonutil"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	logger "github.com/Marcuss-ops/PipelineGen/internal/platform/logging"
	metrics "github.com/Marcuss-ops/PipelineGen/internal/platform/observability"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/client"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/prompts"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/types"
)

// randomSeed returns a non-negative pseudo-random int suitable for Ollama's
// "seed" generation option. We use math/rand (not crypto/rand) because
// reproducibility/determinism are not security properties here — we just
// need a value that changes between calls.
var randomSeed = func() int {
	return int(rand.Int63())
}

// ErrDegenerateScriptOutput marks a provider reply that is technically
// non-empty but cannot be a script (observed live 2026-11-25: gemma4:e4b
// returned 5 characters / 1 word and poisoned the fan-out). The request is
// retried by segment validation; the client breaker is notified so a model
// that keeps doing this is not treated as healthy.
var ErrDegenerateScriptOutput = errors.New("ollama: degenerate script output")

const (
	minScriptOutputWords = 2
	minScriptOutputChars = 8
)

func validateGeneratedScript(content string) error {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return fmt.Errorf("%w: empty response", ErrDegenerateScriptOutput)
	}
	words := len(strings.Fields(trimmed))
	chars := len([]rune(trimmed))
	if words < minScriptOutputWords || chars < minScriptOutputChars {
		return fmt.Errorf("%w: %d word(s), %d chars", ErrDegenerateScriptOutput, words, chars)
	}
	return nil
}

type Generator struct {
	client           *client.Client
	translationCache TranslationCache
	metadataModel    string // lighter model for entity extraction, metadata, translations

	// residentModel is the SINGLE model this generator may keep loaded
	// (WST-02). The server runs with OLLAMA_MAX_LOADED_MODELS=1, so every
	// request naming another model evicts the resident runner and pays a full
	// reload (measured: 1,78 h of warm calls in one window, p50 16,4 s, max
	// 1.803,8 s). When set, ANY per-call model is mapped to this one before it
	// reaches the wire, which is the only place that can guarantee the
	// residency contract for every call path (generation, metadata, single and
	// batched translation). Empty preserves the historical per-call override
	// (used by tests and by compositions that pin their own model).
	residentModel string
	// residentPinned counts requests whose model was mapped to residentModel.
	// Exposed read-only so a run can prove the policy engaged instead of
	// inferring it from warm latency.
	residentPinned atomic.Int64

	// P0-2 (anti-muda): per-query web-context cache. All segments of one
	// job derive the same SearXNG query from the title, so without sharing
	// an N-segment fan-out pays N identical web round-trips and each reply
	// risks a byte-different prefix that defeats the shared-prefix KV
	// cache. TTL is short: freshness per job, not across jobs.
	webMu    sync.Mutex
	webCache map[string]webContextEntry
}

// webContextEntry is one cached SearXNG formatting result.
type webContextEntry struct {
	context   string
	expiresAt time.Time
}

// webContextTTL bounds reuse to the current job fan-out window.
const webContextTTL = 5 * time.Minute

func NewGenerator(c *client.Client) *Generator {
	return &Generator{client: c}
}

func (g *Generator) GetClient() *client.Client {
	return g.client
}

// cachedWebContext returns the shared web context for a query when it is
// still fresh. Double-checked under a short mutex: concurrent segments of
// the same fan-out may rarely double-search once, but the sequential case
// (the common one) always collapses N searches to 1.
func (g *Generator) cachedWebContext(query string) (string, bool) {
	if g == nil {
		return "", false
	}
	g.webMu.Lock()
	defer g.webMu.Unlock()
	if g.webCache == nil {
		return "", false
	}
	entry, ok := g.webCache[query]
	if !ok || time.Now().After(entry.expiresAt) {
		return "", false
	}
	return entry.context, true
}

// storeWebContext shares a fresh web context with sibling segments.
func (g *Generator) storeWebContext(query, formatted string) {
	if g == nil || strings.TrimSpace(query) == "" || formatted == "" {
		return
	}
	g.webMu.Lock()
	defer g.webMu.Unlock()
	if g.webCache == nil {
		g.webCache = make(map[string]webContextEntry)
	}
	g.webCache[query] = webContextEntry{context: formatted, expiresAt: time.Now().Add(webContextTTL)}
}

// WarmModel exposes the explicit residency seam to the script engine.
func (g *Generator) WarmModel(ctx context.Context, model string) error {
	if g == nil || g.client == nil {
		return fmt.Errorf("ollama client not initialized")
	}
	return g.client.WarmModel(ctx, g.pinToResident(model))
}

// SetMetadataModel sets a lighter model for post-generation phases
// (entity extraction, video metadata, translations).
func (g *Generator) SetMetadataModel(model string) {
	g.metadataModel = model
}

// MetadataModel reports the lighter model used for post-generation phases.
// Empty means "reuse the client default" (single-resident, no eviction).
func (g *Generator) MetadataModel() string {
	if g == nil {
		return ""
	}
	return g.metadataModel
}

// SetResidentModel pins the ONE model this generator may keep loaded. It is
// the residency policy seam (WST-02): once set, every per-call model is mapped
// to it, so a payload asking for another size cannot evict the resident runner.
func (g *Generator) SetResidentModel(model string) {
	if g == nil {
		return
	}
	g.residentModel = strings.TrimSpace(model)
}

// ResidentModel reports the pinned resident model (empty = no pin).
func (g *Generator) ResidentModel() string {
	if g == nil {
		return ""
	}
	return g.residentModel
}

// ResidentPinnedRequests reports how many requests were mapped onto the
// resident model by the policy.
func (g *Generator) ResidentPinnedRequests() int64 {
	if g == nil {
		return 0
	}
	return g.residentPinned.Load()
}

// pinToResident maps a requested model onto the resident one, recording the
// substitution. It is the single enforcement point of the residency policy.
func (g *Generator) pinToResident(requested string) string {
	requested = strings.TrimSpace(requested)
	resident := g.ResidentModel()
	if resident == "" {
		return requested
	}
	if requested != "" && requested != resident {
		g.residentPinned.Add(1)
	}
	return resident
}

// resolveModel returns the effective model: resident pin > explicit >
// g.metadataModel > client default.
func (g *Generator) resolveModel(model string) string {
	if resident := g.ResidentModel(); resident != "" {
		return g.pinToResident(model)
	}
	if model != "" {
		return model
	}
	return g.metadataModel
}

// GenerateVisualPrompt takes a narrative block and turns it into a concise visual prompt for image generation models.
func (g *Generator) GenerateVisualPrompt(ctx context.Context, text, topic, style string) (string, error) {
	if g.client == nil {
		return "", fmt.Errorf("ollama client not initialized")
	}

	systemPrompt := "You are a visual design assistant. Convert narrative script blocks into concise, concrete visual prompts for image generation models."
	userPrompt := fmt.Sprintf("Convert this narrative script block into a single concrete visual description for an image generator (like FLUX).\nSCRIPT BLOCK: %s\nTOPIC: %s\nSTYLE: %s\n\nRULES:\n1. Describe only what is visually seen in a single scene.\n2. Include mood, lighting, and camera angle.\n3. Do NOT include narrative text, subtitles, or time markers.\n4. Keep it concise (1-2 sentences maximum).\n5. Return ONLY the visual prompt.", text, topic, style)

	messages := []types.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}

	// P1-4 (anti-muda): bound the output. A visual prompt is 1-2 sentences
	// by contract; an unbounded num_predict lets the model ramble into an
	// essay and burns the dominant per-scene cost. 128 matches the visual
	// per-scene visual candidate planner has been retired; this cap belongs to
	// the separate image-prompt generation request below.
	options := map[string]any{
		"num_predict": 128,
		"temperature": 0.3,
	}
	result, err := g.client.Chat(ctx, messages, options, nil)
	if err != nil {
		return "", fmt.Errorf("visual prompt generation failed: %w", err)
	}

	return strings.TrimSpace(result), nil
}

func (g *Generator) GenerateScript(ctx context.Context, req types.TextGenerationRequest) (*types.GenerationResult, error) {
	if g.client == nil {
		return nil, fmt.Errorf("ollama client not initialized")
	}
	setTextDefaults(&req)

	modelLabel := req.Model
	if modelLabel == "" {
		modelLabel = "unknown"
	}
	languageLabel := req.Language
	if languageLabel == "" {
		languageLabel = "unknown"
	}
	genOutcome := "error"
	defer func() {
		metrics.ScriptGenerationTotal.WithLabelValues(modelLabel, languageLabel, genOutcome).Inc()
	}()

	// Auto-retrieve web context if SearXNG is configured and query is derivable.
	// P0-2 (anti-muda): de-duplicated per query with a short-TTL cache so an
	// N-segment fan-out pays ONE web round-trip instead of N. The cached
	// string is byte-identical for every segment, which also keeps the
	// shared-prefix KV cache usable across the fan-out.
	if ws := g.client.WebSearcher(); ws != nil && !req.DisableWebSearch && req.WebContext == "" {
		searchQuery := SearchQueryForScript(req)
		if searchQuery != "" {
			if cached, ok := g.cachedWebContext(searchQuery); ok {
				req.WebContext = cached
				logger.Info("web search context reused from job cache",
					zap.String("query", searchQuery),
				)
			} else {
				searchStart := time.Now()
				results, err := ws.Search(ctx, searchQuery)
				if err != nil {
					logger.Warn("web search failed in GenerateScript", zap.Error(err), zap.String("query", searchQuery))
				} else if len(results) > 0 {
					req.WebContext = client.FormatContext(results)
					g.storeWebContext(searchQuery, req.WebContext)
					logger.Info("web search context injected into prompt",
						zap.String("query", searchQuery),
						zap.Int("results", len(results)),
						zap.Duration("elapsed", time.Since(searchStart)),
					)
				}
			}
		}
	}

	messages := prompts.BuildChatMessages(&req)

	// Ensure sensible generation options when caller provides none. Seed is
	// randomized per call when zero so repeated runs on the same prompt do
	// not collapse to the same wording — Ollama gemma models are otherwise
	// highly deterministic at temperature=0.35.
	options := req.Options
	if options == nil {
		options = make(map[string]any)
	}
	// The request-level model is part of the canonical generation plan, but it
	// is resolved through the residency policy first: with a pinned resident
	// model an explicit item model is mapped onto it instead of forcing a
	// runner reload (WST-02). With no pin the historical override is forwarded
	// verbatim.
	effectiveModel := g.pinToResident(req.Model)
	if effectiveModel != "" {
		options["model"] = effectiveModel
	}
	if req.MaxChars > 0 {
		options["num_predict"] = ResolveOutputBudget(req)
	} else if _, ok := options["num_predict"]; !ok {
		options["num_predict"] = ResolveOutputBudget(req)
	}
	// num_ctx is derived from the actual prompt and output budget. Production
	// requests stay on the resident 8192 runner so Ollama does not reload the
	// same model when a later scene needs a larger prompt.
	if _, ok := options["num_ctx"]; !ok {
		options["num_ctx"] = ResolveContextBudget(messages, options["num_predict"])
	}
	if _, ok := options["temperature"]; !ok {
		options["temperature"] = types.DefaultTemperature
	}
	if _, ok := options["top_p"]; !ok {
		options["top_p"] = types.DefaultTopP
	}
	if !req.NoSeed {
		if _, ok := options["seed"]; !ok {
			if req.Seed == 0 {
				options["seed"] = randomSeed()
			} else {
				options["seed"] = req.Seed
			}
		}
	} else {
		delete(options, "seed")
	}
	if req.Temperature > 0 {
		options["temperature"] = req.Temperature
	}
	if req.TopP > 0 {
		options["top_p"] = req.TopP
	}

	// PR-3 (LLM-PLAIN-TEXT-CONTRACT wave, July 2026): the inline
	// JSON-mode trigger collapsed into resolveGenerationFormat.
	// See generate_format.go for the canonical 4-case decision tree.
	req.Format = resolveGenerationFormat(req)

	// The Ollama client is the owner of the inference boundary, so it emits
	// the canonical ollama/generate operation itself (MeasureOperation). The
	// legacy Prometheus histogram above remains the migration projection;
	// no caller re-times the same request independently. A parallel fan-out
	// binds its provenance (segment_id, worker_id, queued_at) to ctx via
	// WithOperationMeta; it is merged onto this single canonical operation so
	// the fan-out can be reconstructed without a second timer.
	info := kernobs.OperationInfo{
		Stage:     kernobs.StageGenerate,
		Component: kernobs.ComponentOllama,
		Operation: kernobs.OperationGenerate,
		Items:     1,
	}
	if meta, ok := kernobs.OperationMetaFromContext(ctx); ok {
		meta.Apply(&info)
	}
	// The Ollama server reports its own timing/token facts (load_duration,
	// prompt_eval_count, eval_count, eval_duration, total_duration). OnRecord
	// merges them into the operation's metadata_json so the RunReport carries
	// the split inference cost per call — model load, prompt eval, generation,
	// tokens, tokens/sec, cold start — without a second timer around the
	// boundary and without changing the wall/status/error semantics.
	var result string
	var chatMetrics *client.ChatMetrics
	info.OnRecord = func(op *kernobs.OperationReport) {
		if chatMetrics == nil {
			return
		}
		meta := map[string]any{}
		if op.MetadataJSON != "" {
			// Fail-open with observability: a corrupt operation record loses
			// only its merged facts, and the loss is logged.
			jsonutil.UnmarshalOrLog([]byte(op.MetadataJSON), &meta, "operation_report.metadata_json", logger.Get())
		}
		meta["model"] = chatMetrics.Model
		// Keep the request shape beside Ollama's server timings. In particular,
		// a load spike is only actionable when we know whether the call stayed
		// on the resident runner context or asked Ollama to resize it.
		if value, ok := options["num_ctx"]; ok {
			meta["num_ctx"] = value
		}
		if value, ok := options["num_predict"]; ok {
			meta["num_predict"] = value
		}
		meta["input_tokens"] = chatMetrics.PromptEvalCount
		meta["output_tokens"] = chatMetrics.EvalCount
		meta["model_load_ms"] = chatMetrics.ModelLoadMS()
		meta["prompt_eval_ms"] = chatMetrics.PromptEvalDurationNS / 1e6
		meta["inference_wall_ms"] = chatMetrics.InferenceWallMS()
		meta["inference_work_ms"] = chatMetrics.InferenceWorkMS()
		meta["tokens_per_second"] = chatMetrics.TokensPerSecond()
		meta["cold_start"] = chatMetrics.ColdStart()
		if b, err := json.Marshal(meta); err == nil {
			op.MetadataJSON = string(b)
		}
	}
	err := kernobs.MeasureOperation(ctx, info, func(ctx context.Context) error {
		chatResult, chatErr := g.client.ChatDetailed(ctx, messages, options, req.Format)
		result = chatResult.Content
		chatMetrics = chatResult.Metrics
		return chatErr
	})
	if err != nil {
		return nil, fmt.Errorf("script generation failed: %w", err)
	}
	if err := validateGeneratedScript(result); err != nil {
		// The HTTP call succeeded, so ChatDetailed already recorded a success.
		// The content contract belongs to the generator; record the failure at
		// the same model so the breaker is not fooled by a 200 with junk.
		g.client.RecordModelOutputFailure(effectiveModel)
		logger.Warn("ollama script response rejected as degenerate",
			zap.String("model", effectiveModel),
			zap.Int("chars", len([]rune(strings.TrimSpace(result)))),
			zap.Int("words", len(strings.Fields(strings.TrimSpace(result)))),
			zap.Error(err),
		)
		return nil, fmt.Errorf("script generation failed: %w", err)
	}
	genOutcome = "success"
	wordCount := len(strings.Fields(result))
	return &types.GenerationResult{
		Script:           result,
		WordCount:        wordCount,
		EstDuration:      estimateDurationSecondsWithWPM(wordCount, req.WordsPerMinute),
		Model:            effectiveModel,
		Prompt:           prompts.BuildTextPrompt(&req),
		GenerationSource: "ollama",
	}, nil
}
