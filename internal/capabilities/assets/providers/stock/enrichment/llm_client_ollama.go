// Package enrichment — llm_client_ollama.go (PR-SPLIT-LLM-CLIENT, 2026-08-08).
//
// godlike/06 SSOT (one canonical owner per fact): the canonical
// production ollama-backed adapter + the system-prompt V1/V2
// consts + the i18n/prompt-iteration seam live ONLY in this file.
// Future adapter swaps (vllm / nvidia / mock) MUST declare a NEW
// file (e.g. llm_client_vllm.go) + implement the EnrichmentLLMClient
// port (NOT mutate the consts in place).
package enrichment

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/client"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/types"
)

// enrichmentSystemPromptV1 is the canonical English system message
// shipped to ollama for the stock RLM/LLM enrichment pass.
// Declares the 6-field JSON schema with `category` as the canonical
// "required" marker.
const enrichmentSystemPromptV1 = `You are a video metadata enrichment assistant. Return a JSON object with exactly these fields: "category" (string, required, single most specific category from the canonical taxonomy), "event" (string, event/fight/match name; empty if none), "round" (string, round number for boxing/MMA; empty if not applicable), "scene" (string, 5-15 word description of the visible action; empty if none), "subject" (string, primary subject/protagonist; empty if none), "entities" (array of up to 5 named entities: people, places, organizations; empty array if none). Respond with ONLY the JSON object, no prose, no markdown fences.`

// enrichmentSystemPromptV2 is the Italian-language variant of the
// enrichment system prompt (PR-011B follow-up i18n seam, July 2026).
// Selected when cfg.External.EnrichmentPromptVersion == "v2" or
// "v2-it" (canonical alias). JSON keys are byte-equivalent to V1
// so the parser (json tags) works unchanged — only the natural-
// language instructions differ.
const enrichmentSystemPromptV2 = `Sei un assistente di arricchimento dei metadati video. Restituisci un oggetto JSON con esattamente questi campi: "category" (stringa, obbligatoria, la singola categoria più specifica dalla tassonomia canonica), "event" (stringa, nome dell'evento/incontro/match; vuoto se assente), "round" (stringa, numero della ripresa per boxe/MMA; vuoto se non applicabile), "scene" (stringa, descrizione di 5-15 parole dell'azione visibile; vuoto se assente), "subject" (stringa, soggetto/protagonista principale; vuoto se assente), "entities" (array fino a 5 entità nominate: persone, luoghi, organizzazioni; array vuoto se assente). Rispondi SOLO con l'oggetto JSON, senza prosa, senza delimitatori markdown.`

// ollamaEnrichmentLLMClient is the PR-011B real adapter.
//
// godlike/06 SSOT: ollamaEnrichmentLLMClient lives ONLY in this
// file. The ollama client itself lives in
// internal/platform/ollama/client (canonical port for
// /api/chat wire).
type ollamaEnrichmentLLMClient struct {
	// client is the canonical ollama wire-contract adapter.
	client *client.Client

	// modelName is the per-capability model identifier.
	// Overrides client.Model() when non-empty.
	modelName string

	// promptVersion is the per-capability system-prompt version.
	promptVersion string
}

// NewOllamaEnrichmentLLMClient constructs the real ollama-backed
// adapter. Fail-closed when the underlying ollama client is nil.
func NewOllamaEnrichmentLLMClient(c *client.Client, model, promptVersion string) (*ollamaEnrichmentLLMClient, error) {
	if c == nil {
		return nil, WrapHandlerNotConfigured("ollamaClient")
	}
	return &ollamaEnrichmentLLMClient{
		client:        c,
		modelName:     model,
		promptVersion: promptVersion,
	}, nil
}

// selectSystemPrompt is the canonical i18n/prompt-iteration seam.
// Returns V1 by default; V2 for "v2"/"v2-it" versions; unknown
// versions fall back to V1 (godlike/07 fail-closed).
func (o *ollamaEnrichmentLLMClient) selectSystemPrompt(locale string) string {
	if o == nil {
		return enrichmentSystemPromptV1
	}
	version := strings.ToLower(strings.TrimSpace(o.promptVersion))
	switch version {
	case "v2", "v2-it":
		return enrichmentSystemPromptV2
	case "", "v1":
		return enrichmentSystemPromptV1
	default:
		return enrichmentSystemPromptV1
	}
}

// Enrich dispatches the canonical enrichment prompt to the
// configured ollama model with native JSON-mode forced.
//
// godlike/07 typed-error contract:
//   - c.Chat() returns error → ErrEnrichmentLLMUnavailable (retryable)
//   - JSON parse failure → ErrEnrichmentInvalidLLMResponse (terminal)
//   - Empty Category → ErrEnrichmentInvalidLLMResponse (terminal)
func (o *ollamaEnrichmentLLMClient) Enrich(ctx context.Context, req EnrichmentRequest) (*EnrichmentResponse, error) {
	if o == nil || o.client == nil {
		return nil, WrapHandlerNotConfigured("ollamaClient")
	}
	if req.ChunkID == "" {
		return nil, WrapHandlerNotConfigured("chunk_id")
	}

	start := time.Now()
	messages := []types.Message{
		{Role: "system", Content: o.selectSystemPrompt("")},
		{Role: "user", Content: buildEnrichmentUserPrompt(req)},
	}

	model := o.modelName
	if model == "" {
		model = o.client.Model()
	}
	options := map[string]any{
		"model":      model,
		"keep_alive": "30m",
	}

	format := json.RawMessage(`"json"`)
	content, err := o.client.Chat(ctx, messages, options, format)
	if err != nil {
		return nil, WrapLLMUnavailable(err)
	}

	var fields EnrichedFields
	if err := json.Unmarshal([]byte(content), &fields); err != nil {
		return nil, WrapInvalidLLMResponse(fmt.Errorf("ollama chat content did not parse as EnrichedFields JSON: %w (content_preview=%q)", err, previewForLog(content)))
	}

	if fields.Category == "" {
		return nil, WrapInvalidLLMResponse(fmt.Errorf("ollama chat returned empty Category (schema-drift signal; system message requires category as the canonical 6-field marker)"))
	}

	return &EnrichmentResponse{
		ChunkID: req.ChunkID,
		Fields:  fields,
		Model:   model,
		Elapsed: time.Since(start),
	}, nil
}

// Model returns the per-capability model identifier. Falls back
// to the underlying ollama client's default when modelName is empty.
func (o *ollamaEnrichmentLLMClient) Model() string {
	if o == nil {
		return ""
	}
	if o.modelName != "" {
		return o.modelName
	}
	if o.client != nil {
		return o.client.Model()
	}
	return ""
}

// Compile-time assertion: *ollamaEnrichmentLLMClient satisfies
// EnrichmentLLMClient per AGENTS.md Pattern 0 / godlike/06 SSOT.
var _ EnrichmentLLMClient = (*ollamaEnrichmentLLMClient)(nil)
