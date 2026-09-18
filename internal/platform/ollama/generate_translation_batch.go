package ollama

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	logger "github.com/Marcuss-ops/PipelineGen/internal/platform/logging"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/prompts"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/types"
)

// ErrBatchTranslationContract is the typed error returned when a batched
// answer violates the wire contract (unparsable JSON, a missing / duplicated /
// unknown segment id, an empty translation). Callers MUST treat it as
// "this chunk is not usable" and retry those segments one by one — never as a
// partial success (godlike/07: no fake availability).
var ErrBatchTranslationContract = errors.New("ollama: batch translation contract violated")

// DefaultBatchTranslationSegments is the canonical chunk size of a batched
// translation request. One request per chunk removes one LLM round-trip (and
// one 3-slot queue admission) per subtitle cue, while keeping the prompt small
// enough that the model cannot lose track of the ids.
const DefaultBatchTranslationSegments = 12

// BatchTranslationSegment is one independent text unit of a batched request.
// ID is the caller's stable correlation key (a cue index, a scene id). It is
// echoed by the model and validated verbatim on the way back — the response
// order is NOT trusted.
type BatchTranslationSegment struct {
	ID   string
	Text string
}

type batchTranslationPayload struct {
	Translations []struct {
		ID   string `json:"id"`
		Text string `json:"text"`
	} `json:"translations"`
}

// TranslateBatchWithModel translates many independent segments in ONE chat
// request per chunk.
//
// The per-cue path (TranslateTextWithModel) is correct but pays a full LLM
// round-trip per cue: a 100-cue subtitle track in 10 languages is ~1000
// requests, each dominated by the ~130-token system prompt. Batching collapses
// that to ~1000/DefaultBatchTranslationSegments requests.
//
// Cache-aware: segments already present in the translation cache (L1 memory +
// L2 SQLite) are returned from cache and never sent to the model, and every
// fresh translation is stored back, so a batched call and a per-cue call share
// one cache namespace.
//
// The returned slice mirrors the input order. An error means NO segment of the
// failed chunk is usable (the caller falls back to the per-cue path).
func (g *Generator) TranslateBatchWithModel(ctx context.Context, segments []BatchTranslationSegment, targetLanguage, model string, chunkSize int) ([]BatchTranslationSegment, error) {
	if g.client == nil {
		return nil, fmt.Errorf("ollama client not initialized")
	}
	if len(segments) == 0 {
		return nil, nil
	}
	if chunkSize < 1 {
		chunkSize = DefaultBatchTranslationSegments
	}

	out := make([]BatchTranslationSegment, len(segments))
	for i, segment := range segments {
		out[i] = segment
		if strings.TrimSpace(segment.ID) == "" || strings.TrimSpace(segment.Text) == "" {
			return nil, fmt.Errorf("%w: segment %d has an empty id/text", ErrBatchTranslationContract, i)
		}
	}

	// ── Cache pass: serve hits, collect misses ─────────────────────────────
	pending := make([]int, 0, len(segments))
	for i, segment := range segments {
		if g.translationCache != nil {
			if cached, ok := g.translationCache.Get(ctx, segment.Text, targetLanguage); ok {
				out[i].Text = cached
				continue
			}
		}
		pending = append(pending, i)
	}
	if len(pending) == 0 {
		return out, nil
	}

	langName := translateLanguageName(targetLanguage)

	for start := 0; start < len(pending); start += chunkSize {
		end := start + chunkSize
		if end > len(pending) {
			end = len(pending)
		}
		indexes := pending[start:end]
		chunk := make([]BatchTranslationSegment, 0, len(indexes))
		for _, index := range indexes {
			// The id sent to the model is the ORIGINAL index, so a response can
			// never be mis-attributed even when the model reorders the array.
			chunk = append(chunk, BatchTranslationSegment{ID: fmt.Sprintf("%d", index), Text: segments[index].Text})
		}

		translations, err := g.translateChunk(ctx, chunk, langName, model)
		if err != nil {
			return nil, err
		}
		for _, index := range indexes {
			translated := translations[fmt.Sprintf("%d", index)]
			out[index].Text = translated
			if g.translationCache != nil {
				if storeErr := g.translationCache.Set(ctx, segments[index].Text, targetLanguage, translated); storeErr != nil {
					logger.Warn("failed to store batched translation in cache", zap.Error(storeErr))
				}
			}
		}
	}

	return out, nil
}

// translateChunk performs one batched chat call and validates its answer.
//
// Deliberately ONE model: switching models mid-run would evict the single
// resident runner (OLLAMA_MAX_LOADED_MODELS=1) and pay a full reload. Retries
// and the client's own model-fallback chain stay inside Client.Chat.
func (g *Generator) translateChunk(ctx context.Context, chunk []BatchTranslationSegment, langName, model string) (map[string]string, error) {
	systemPrompt, userPrompt := batchTranslationPrompts(chunk, langName)

	// 1 char ≈ 0.25 tokens; a translation is not a composition, so the source
	// length is the honest upper bound (plus per-segment punctuation slack).
	sourceChars := 0
	for _, segment := range chunk {
		sourceChars += len([]rune(segment.Text))
	}
	predictLimit := sourceChars*2 + 256*len(chunk)
	if predictLimit < 512 {
		predictLimit = 512
	}
	if predictLimit > 8192 {
		predictLimit = 8192
	}

	options := map[string]any{
		"num_predict": predictLimit,
		"temperature": 0.1, // low temperature for faithful translation
	}
	if effectiveModel := g.resolveModel(model); effectiveModel != "" {
		options["model"] = effectiveModel
	}

	// Native JSON mode: the contract is a JSON object, so ask the server to
	// guarantee syntactic validity instead of parsing free-form prose.
	raw, err := g.client.Chat(ctx, []types.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}, options, json.RawMessage(`"json"`))
	if err != nil {
		return nil, fmt.Errorf("batched translation request failed: %w", err)
	}
	return parseBatchTranslationResponse(raw, chunk)
}

// batchTranslationPrompts renders the batched translation prompt. Exported
// shape (system, user) keeps the prompt a pure function so it is unit-testable
// without a client.
func batchTranslationPrompts(chunk []BatchTranslationSegment, langName string) (string, string) {
	systemPrompt := "You are a professional translator. CRITICAL RULES: 1. Translate the text LITERALLY — do NOT expand, explain, philosophize, or add any content. 2. Translate every numbered segment EXACTLY ONCE and keep the same ids and the same order. 3. Never merge, split, reorder, drop or add segments, and never drop a sentence. 4. Return ONLY the requested JSON object — no intros, no conclusions, no markdown fences, no meta-commentary. 5. If you don't know a word, keep it in the original language rather than guessing."
	if cfg := prompts.Get(); cfg != nil {
		// Reuse the operator-visible single-text system prompt (one owner of
		// the translation persona) and append the batch constraint, so a
		// deployment that tunes prompts/config/core.yaml tunes both paths.
		if s, _, err := cfg.RenderTranslation("", langName); err == nil && strings.TrimSpace(s) != "" {
			systemPrompt = strings.TrimSpace(s) + " 6. When asked for a JSON object, return ONLY that JSON object."
		}
	}

	var builder strings.Builder
	fmt.Fprintf(&builder, "Translate each of the following %d segments into %s faithfully. No additions, no explanations, no creative writing.\n", len(chunk), langName)
	builder.WriteString("One translation per segment, same ids, same order. Return ONLY this JSON object:\n")
	builder.WriteString(`{"translations":[{"id":"<id>","text":"<translated text>"}]}`)
	builder.WriteString("\n\nSEGMENTS:\n")
	for _, segment := range chunk {
		fmt.Fprintf(&builder, "[%s] %s\n", segment.ID, segment.Text)
	}
	return systemPrompt, builder.String()
}

// parseBatchTranslationResponse validates the model answer against the
// request. Every deviation (unknown / missing / duplicated id, empty text,
// unparsable JSON) is a contract violation, never a silent partial success.
func parseBatchTranslationResponse(raw string, chunk []BatchTranslationSegment) (map[string]string, error) {
	cleaned := strings.TrimSpace(raw)
	// Tolerate a fenced payload: some models wrap JSON in ```json fences even
	// when JSON mode is on. Everything else stays strict.
	if strings.HasPrefix(cleaned, "```") {
		cleaned = strings.TrimPrefix(cleaned, "```json")
		cleaned = strings.TrimPrefix(cleaned, "```")
		cleaned = strings.TrimSuffix(strings.TrimSpace(cleaned), "```")
		cleaned = strings.TrimSpace(cleaned)
	}
	if cleaned == "" {
		return nil, fmt.Errorf("%w: empty response", ErrBatchTranslationContract)
	}

	var payload batchTranslationPayload
	if err := json.Unmarshal([]byte(cleaned), &payload); err != nil {
		return nil, fmt.Errorf("%w: unparsable JSON: %v", ErrBatchTranslationContract, err)
	}

	expected := make(map[string]bool, len(chunk))
	for _, segment := range chunk {
		expected[segment.ID] = true
	}
	got := make(map[string]string, len(payload.Translations))
	for _, item := range payload.Translations {
		if !expected[item.ID] {
			return nil, fmt.Errorf("%w: unknown segment id %q", ErrBatchTranslationContract, item.ID)
		}
		if _, duplicate := got[item.ID]; duplicate {
			return nil, fmt.Errorf("%w: duplicated segment id %q", ErrBatchTranslationContract, item.ID)
		}
		text := strings.TrimSpace(item.Text)
		if text == "" {
			return nil, fmt.Errorf("%w: empty translation for segment id %q", ErrBatchTranslationContract, item.ID)
		}
		got[item.ID] = text
	}
	if len(got) != len(expected) {
		return nil, fmt.Errorf("%w: got %d translations for %d segments", ErrBatchTranslationContract, len(got), len(expected))
	}
	return got, nil
}
