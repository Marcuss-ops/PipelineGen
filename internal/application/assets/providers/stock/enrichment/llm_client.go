// Package enrichment — llm_client.go (PR-011A + PR-011B, July 2026).
//
// Pattern-0 typed port for the stock RLM/LLM enrichment LLM client.
// The canonical port per godlike/06 SSOT: EnrichmentLLMClient lives
// ONLY in this file; future adapters (ollama, vllm, nvidia, mock)
// implement this interface and are injected via composition root
// fluent setters per AGENTS.md Pattern 0.
//
// PR-011A scope: declares the typed contract + a stub adapter that
// returns ErrEnrichmentLLMUnavailable (so the worker retry path
// is exercised end-to-end without a real ollama call). PR-011B
// will replace the stub with a real ollama-backed adapter that
// parses the response into EnrichedFields and surfaces parse
// errors via ErrEnrichmentInvalidLLMResponse.
//
// godlike/07 typed-error contract: every error returned from
// Enrich() must be a typed sentinel reachable via errors.Is().
// The stub adapter returns ErrEnrichmentLLMUnavailable verbatim;
// the future ollama adapter will return
// ErrEnrichmentInvalidLLMResponse on parse failures and
// ErrEnrichmentLLMUnavailable on network errors.
//
// godlike/07 minimum-blast-radius: the port is additive — the
// stock pipeline (PR-001..PR-009 chain) does not call this
// port directly. The EnrichmentHandler consumes it; the
// EnrichmentHandler is the canonical SOLE owner of
// "when do we call the LLM and what do we do with the
// response". The port exposes only the minimal surface needed
// (input + output) so the LLM call site has one canonical
// entry point.
package enrichment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/client"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/types"
)

// EnrichedFields is the typed result envelope returned by
// EnrichmentLLMClient.Enrich. The 6 LLM-only fields mirror the
// ChunkMetadataEntry extension shipped in PR-007 (LLM enrichment
// plumbing) — PR-008 wire-shape projected them as IndexingStatus
// literal; PR-011B+C will populate them from the LLM response.
//
// godlike/06 SSOT (one canonical owner per fact): EnrichedFields
// lives ONLY in this file. The 6 fields are the canonical set
// per user spec (Category / Event / Round / Scene / Subject /
// Entities); future enrichment passes MUST extend this struct
// (NOT introduce a parallel envelope) to preserve one-canonical-
// owner-per-fact discipline.
//
// JSON tags mirror the metadata.json shape from PR-001..PR-009;
// the canonical SSOT pair (EnrichedFields + ChunkMetadataEntry)
// share the same JSON tag namespace so the ollama JSON-mode
// response deserializes byte-equivalently into either envelope.
type EnrichedFields struct {
	// Category is the LLM-inferred content category
	// (e.g. "Boxe", "Sport", "Documentario"). Free-form string —
	// the LLM prompt asks for the single most specific category
	// from the canonical taxonomy.
	Category string `json:"category"`

	// Event is the LLM-inferred event / fight / match name
	// (e.g. "Pacquiao vs Broner"). Empty when the chunk is not
	// associated with a specific event.
	Event string `json:"event,omitempty"`

	// Round is the LLM-inferred round number (e.g. "5") for
	// boxing / MMA content. Empty when the content is not
	// round-annotated.
	Round string `json:"round,omitempty"`

	// Scene is the LLM-inferred scene description
	// (e.g. "Combination in the corner"). Free-form — the
	// LLM prompt asks for a 5-15 word description of the
	// visible action.
	Scene string `json:"scene,omitempty"`

	// Subject is the LLM-inferred primary subject / protagonist
	// (e.g. "Mike Tyson", "Manny Pacquiao"). Empty when the
	// content has no specific subject.
	Subject string `json:"subject,omitempty"`

	// Entities is the LLM-extracted list of named entities
	// (people, places, organizations). The list is bounded
	// at 5 entries per the canonical prompt template
	// (matches Artlist phrases cap precedent).
	Entities []string `json:"entities,omitempty"`
}

// EnrichmentRequest is the typed input envelope for the LLM
// enrichment call. The metadata fields are sourced from the
// media_assets row the handler reads; the LLM uses them as
// context to produce the EnrichedFields output.
type EnrichmentRequest struct {
	// ChunkID is the canonical media_assets.id (caller's job
	// payload chunk_id). Echoed back in the response for
	// downstream audit logging.
	ChunkID string `json:"chunk_id"`

	// SourceURL is the original stock source URL (e.g. pexels
	// clip URL). Optional — the LLM uses it as context for
	// category inference.
	SourceURL string `json:"source_url,omitempty"`

	// Title is the chunk title (mirrors ChunkState.Title from
	// PR-007). Optional — the LLM uses it as context.
	Title string `json:"title,omitempty"`

	// Description is the chunk description (operator-supplied
	// or empty). Optional.
	Description string `json:"description,omitempty"`

	// StartSec is the clip start timestamp in seconds.
	// Optional.
	StartSec float64 `json:"start_sec,omitempty"`

	// EndSec is the clip end timestamp in seconds.
	// Optional.
	EndSec float64 `json:"end_sec,omitempty"`

	// SourceProvider is the canonical source identifier
	// (e.g. "pexels", "pixabay"). The LLM uses it to
	// adjust category vocabulary per provider conventions.
	SourceProvider string `json:"source_provider,omitempty"`
}

// EnrichmentResponse is the typed output envelope from the LLM
// enrichment call. The handler projects the response into
// media_assets.metadata_json + re-emits the asset.published
// outbox event (PR-011C).
type EnrichmentResponse struct {
	// ChunkID mirrors the input request (canonical SSOT
	// echo for downstream audit log correlation).
	ChunkID string `json:"chunk_id"`

	// Fields is the LLM-inferred enrichment. The 6 fields
	// are populated by the LLM prompt template.
	Fields EnrichedFields `json:"fields"`

	// Model is the actual model identifier used (mirrors
	// cfg.External.ParseArenaLLM at call time, or the
	// fallback cfg.External.OllamaModel when empty).
	Model string `json:"model"`

	// Elapsed is the wall-clock duration of the LLM call.
	// Operator-facing observability — surfaces on the
	// dashboard alongside the job's terminal status.
	Elapsed time.Duration `json:"elapsed_ms,omitempty"`
}

// EnrichmentLLMClient is the canonical Pattern-0 typed port
// for the LLM client used by the stock RLM/LLM enrichment pass.
// godlike/06 SSOT: this interface is the SOLE definition of
// the LLM-client contract; no other package redefines the
// signature.
//
// Implementations:
//   - enrichment.stubEnrichmentLLMClient (PR-011A — returns
//     ErrEnrichmentLLMUnavailable to drive the worker retry path)
//   - enrichment.ollamaEnrichmentLLMClient (PR-011B — wraps
//     internal/infrastructure/ai/ollama/client.Client.Chat)
//
// The future ollama adapter is FORWARD-POINTED to PR-011B;
// this PR ships only the typed contract + the stub.
type EnrichmentLLMClient interface {
	// Enrich sends the request to the LLM and returns the
	// typed EnrichedFields. Implementations MUST return a
	// typed sentinel on any failure mode (LLM unreachable →
	// ErrEnrichmentLLMUnavailable; response parse failure →
	// ErrEnrichmentInvalidLLMResponse).
	Enrich(ctx context.Context, req EnrichmentRequest) (*EnrichmentResponse, error)

	// Model returns the LLM model identifier currently
	// configured (mirrors cfg.External.ParseArenaLLM with
	// cfg.External.OllamaModel fallback). Used by the
	// handler for audit logging and dashboard rendering.
	Model() string
}

// StubEnrichmentLLMClient is the PR-011A stub adapter. Every
// Enrich() call returns ErrEnrichmentLLMUnavailable so the
// worker retry path is exercised end-to-end without a real
// ollama call. PR-011B will replace this stub with a real
// ollama-backed adapter; the typed contract is byte-stable
// across the migration.
//
// godlike/07 minimum-blast-radius: the stub is the
// composition-root default when cfg.External.StockEnrichmentEnabled
// is true but no real ollama adapter is wired (dev / test
// environments). Production deployments wire the real
// ollama adapter via fluent setter per AGENTS.md Pattern 0.
//
// godlike/06 SSOT (one canonical owner per fact):
// StubEnrichmentLLMClient lives ONLY in this file.
type StubEnrichmentLLMClient struct {
	// modelName is the model identifier the stub reports
	// via Model(). Mirrors the future real adapter's getter
	// contract so handlers can log the model without
	// special-casing stub vs real. Unexported to avoid
	// Go's field/method name collision with the Model()
	// method below.
	modelName string
}

// NewStubEnrichmentLLMClient constructs a stub adapter with
// the canonical model identifier. When model is empty,
// defaults to "stub:enrichment-unavailable" so audit logs
// can distinguish stub-originated responses from real-llm
// responses in the dashboard.
func NewStubEnrichmentLLMClient(model string) *StubEnrichmentLLMClient {
	if model == "" {
		model = "stub:enrichment-unavailable"
	}
	return &StubEnrichmentLLMClient{modelName: model}
}

// Enrich returns ErrEnrichmentLLMUnavailable verbatim per
// godlike/07 typed-error contract. The chunk_id is preserved
// in the wrapped error message for operator log correlation.
func (s *StubEnrichmentLLMClient) Enrich(ctx context.Context, req EnrichmentRequest) (*EnrichmentResponse, error) {
	if req.ChunkID == "" {
		return nil, WrapHandlerNotConfigured("chunk_id")
	}
	return nil, WrapLLMUnavailable(errors.New("stub adapter: PR-011B will wire the real ollama call"))
}

// Model returns the configured stub model identifier.
func (s *StubEnrichmentLLMClient) Model() string {
	return s.modelName
}

// Compile-time assertion: *StubEnrichmentLLMClient satisfies
// EnrichmentLLMClient. Catches signature drift at compile
// time per AGENTS.md Pattern 0 / godlike/06 SSOT.
var _ EnrichmentLLMClient = (*StubEnrichmentLLMClient)(nil)

// enrichmentSystemPromptV1 is the canonical system message
// shipped to ollama for the stock RLM/LLM enrichment pass.
// Declares the 6-field JSON schema with `category` as the
// canonical "required" marker. Extracted to a package-level
// const (per PR-011B follow-up) so the prompt can evolve
// (i18n / prompt-iteration / A-B testing) without touching
// the call site at ollamaEnrichmentLLMClient.Enrich.
//
// godlike/06 SSOT (one canonical owner per fact): the
// canonical system prompt lives ONLY in this const. Future
// enrichment passes that need a different prompt MUST
// declare a new const (e.g. enrichmentSystemPromptV2) and
// route via a new adapter — NOT mutate this const in place
// (that would silently change the wire contract for every
// existing call site).
//
// godlike/07 minimum-blast-radius: raw string literal
// (backticks) so the prompt is human-editable + future
// i18n / A-B testing requires only a const swap. The
// trailing backtick + newline marker is canonical Go
// practice (the constant value INCLUDES the trailing
// newline as the standard message separator).
const enrichmentSystemPromptV1 = `You are a video metadata enrichment assistant. Return a JSON object with exactly these fields: "category" (string, required, single most specific category from the canonical taxonomy), "event" (string, event/fight/match name; empty if none), "round" (string, round number for boxing/MMA; empty if not applicable), "scene" (string, 5-15 word description of the visible action; empty if none), "subject" (string, primary subject/protagonist; empty if none), "entities" (array of up to 5 named entities: people, places, organizations; empty array if none). Respond with ONLY the JSON object, no prose, no markdown fences.`

// enrichmentSystemPromptV2 is the Italian-language variant of the
// enrichment system prompt (PR-011B follow-up i18n seam, July 2026).
// Selected when cfg.External.EnrichmentPromptVersion == "v2" or
// "v2-it" (canonical alias). JSON keys are byte-equivalent to V1
// so the parser (json tags) works unchanged — only the natural-
// language instructions differ. This keeps the wire output shape
// stable across versions; only the model's vocabulary changes.
//
// godlike/06 SSOT (one canonical owner per fact): the canonical
// V2 prompt lives ONLY in this const. Future Italian variants
// (regional dialects, A/B-tested phrasing) MUST declare a new
// const (e.g. enrichmentSystemPromptV2ItNeapolitan) and route
// via selectSystemPrompt — NOT mutate this const in place.
const enrichmentSystemPromptV2 = `Sei un assistente di arricchimento dei metadati video. Restituisci un oggetto JSON con esattamente questi campi: "category" (stringa, obbligatoria, la singola categoria più specifica dalla tassonomia canonica), "event" (stringa, nome dell'evento/incontro/match; vuoto se assente), "round" (stringa, numero della ripresa per boxe/MMA; vuoto se non applicabile), "scene" (stringa, descrizione di 5-15 parole dell'azione visibile; vuoto se assente), "subject" (stringa, soggetto/protagonista principale; vuoto se assente), "entities" (array fino a 5 entità nominate: persone, luoghi, organizzazioni; array vuoto se assente). Rispondi SOLO con l'oggetto JSON, senza prosa, senza delimitatori markdown.`

// =============================================================================
// PR-011B (July 2026): real ollama-backed adapter
// =============================================================================
//
// ollamaEnrichmentLLMClient is the production adapter. Wraps
// *ollama.Client (internal/infrastructure/ai/ollama/client) and
// dispatches the enrichment prompt to the configured model with
// native JSON-mode forced (format = "json"). The 6 LLM-only fields
// are parsed back into EnrichedFields; parse failures surface as
// ErrEnrichmentInvalidLLMResponse (terminal after retries) and
// network/HTTP failures surface as ErrEnrichmentLLMUnavailable
// (retryable per the worker's exponential backoff).
//
// godlike/06 SSOT (one canonical owner per fact):
//   - ollamaEnrichmentLLMClient lives ONLY in this file.
//   - The ollama client itself (wrapping the /api/chat wire) lives
//     in internal/infrastructure/ai/ollama/client (the canonical
//     port for the ollama /api/chat wire contract).
//   - The EnrichmentLLMClient port (declared above) is the SOLE
//     definition of the LLM-client contract; ollama adapter
//     implements it via composition (not by redefining the
//     interface).
//
// godlike/07 typed-error contract:
//   - c.Chat() returns a non-nil error → WrapLLMUnavailable (retryable).
//   - c.Chat() returns valid JSON but missing the canonical
//     Category field (the "required" 6-field marker) →
//     WrapInvalidLLMResponse (terminal after retries).
//   - c.Chat() returns success and parses cleanly into EnrichedFields
//     with non-empty Category → success.
//
// godlike/07 minimum-blast-radius: the adapter is additive (the
// stub is RETAINED for dev / test environments). The composition
// root in internal/app/build_bundles_stock.go decides which to
// instantiate based on cfg.External.ParseArenaLLM (priority) +
// cfg.External.OllamaModel (fallback). The stub remains the
// canonical default when both are empty.
//
// godlike/07 NO-FAKE-AVAILABILITY: every code path returns a
// typed sentinel. Empty Category is a canonical "invalid response"
// signal — retrying without a different prompt or model will not
// help. Network errors are retryable (the ollama server may come
// back online within the worker's exponential backoff window).
//
// Prompt design: a single system message declares the canonical
// 6-field JSON schema (with `category` marked as the required
// marker); a single user message provides the chunk context
// (Title / Description / SourceProvider / SourceURL / StartSec /
// EndSec). The response is forced to valid JSON via ollama's
// `format: "json"` wire constraint (TOP-LEVEL body field, NOT
// nested in `options` — see ollama.Client.Chat godoc).
//
// model precedence: the adapter's own `modelName` field takes
// precedence over `c.Model()` so the composition root can pin a
// per-capability model (e.g. cfg.External.ParseArenaLLM) without
// mutating the shared ollama.Client. When modelName is empty,
// falls back to c.Model() (defense-in-depth per
// cfg.External.OllamaModel default).

// ollamaEnrichmentLLMClient is the PR-011B real adapter. Wraps
// the canonical *ollama.Client (internal/infrastructure/ai/ollama/client)
// and implements the EnrichmentLLMClient port with native JSON-mode
// forced. The composition root injects one instance per server
// (mirrors the production pattern at app/wire_script.go:125 for the
// script-generation metaWriter).
//
// godlike/06 SSOT: the adapter holds a *client.Client directly
// (no narrow interface shim) because the production concrete
// already exposes the minimal surface (Chat + Model) needed by
// the enrichment pass. Adding a local interface would create
// a second canonical surface for the same fact (godlike/06
// violation). Tests that need hermetic isolation use
// httptest.NewServer + client.NewClient(server.URL, ...) which
// exercises the real wire path with canned responses — no
// mock, no fakes.
type ollamaEnrichmentLLMClient struct {
	// client is the canonical ollama wire-contract adapter.
	// Wraps the /api/chat wire + retry + circuit-breaker +
	// model-fallback chain. The composition root constructs
	// the client (mirrors app/build_bundles_ai.go::buildAIBundle
	// — client.NewClient(cfg.External.OllamaURL, cfg.External.OllamaModel, ...)).
	client *client.Client

	// modelName is the per-capability model identifier. When
	// non-empty, it overrides client.Model() via the
	// `options["model"]` parameter on every Chat call. This
	// allows the composition root to pin a per-capability
	// model (e.g. cfg.External.ParseArenaLLM = "gemma4:e4b")
	// without mutating the shared ollama.Client.
	// Unexported to avoid Go's field/method name collision
	// with the Model() method below.
	modelName string

	// promptVersion is the per-capability system-prompt version
	// (PR-011B follow-up i18n seam). When set, selectSystemPrompt
	// consults the canonical version enum ("v1" | "v2" | "v2-it")
	// at Enrich() time to pick the right canonical prompt
	// (enrichmentSystemPromptV1 / enrichmentSystemPromptV2).
	// Empty value falls back to V1 (default) per godlike/07
	// fail-closed (so legacy configs without this field
	// continue to work byte-equivalently).
	promptVersion string
}

// NewOllamaEnrichmentLLMClient constructs the real ollama-backed
// adapter. model is the per-capability override (typically
// cfg.External.ParseArenaLLM); when empty, the adapter falls
// back to client.Model() at call time (defense-in-depth).
// promptVersion is the per-capability system-prompt version
// (typically cfg.External.EnrichmentPromptVersion); when empty,
// the adapter uses V1 (default).
//
// godlike/07 fail-closed: returns an error (not nil adapter) when
// the underlying ollama client is nil. Callers MUST propagate
// the error per the existing pattern in NewEnrichmentHandler.
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

// selectSystemPrompt is the canonical i18n/prompt-iteration seam
// (PR-011B follow-up). Returns the right canonical prompt for
// the configured promptVersion + locale combination.
//
// godlike/06 SSOT (one canonical owner per fact): the version
// enum ("v1" | "v2" | "v2-it") lives ONLY in this method.
// Future versions MUST add a new case here + a new const
// (NOT mutate V1/V2 in place per the canonical SSOT discipline).
//
// godlike/07 fail-closed: unknown versions fall back to V1 so a
// typo in cfg.External.EnrichmentPromptVersion does not silently
// break the enrichment pass.
//
// godlike/07 minimum-blast-radius: the locale parameter is
// forward-extensibility only. Today the version alone is the
// switch (V1 vs V2); the locale hook lets a future V3 do
// "v3-it" vs "v3-en" without a signature change. Passing
// locale="" is byte-equivalent to passing the adapter's
// configured locale.
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
		// Unknown version: fall back to V1 (godlike/07
		// fail-closed at the language-default level so a
		// typo in cfg.External.EnrichmentPromptVersion
		// does not silently break the enrichment pass).
		return enrichmentSystemPromptV1
	}
}

// Enrich dispatches the canonical enrichment prompt to the
// configured ollama model with native JSON-mode forced. The
// response is parsed into EnrichedFields; the typed-error
// contract surfaces:
//
//   - Network/HTTP failure from c.Chat() →
//     ErrEnrichmentLLMUnavailable (retryable per worker
//     exponential backoff).
//   - JSON parse failure → ErrEnrichmentInvalidLLMResponse
//     (terminal after retries).
//   - Empty Category (the canonical "required" 6-field marker) →
//     ErrEnrichmentInvalidLLMResponse (terminal after retries).
//
// godlike/07 NO-FAKE-AVAILABILITY: a successful ollama call that
// returns an empty Category is treated as an invalid response,
// not a successful enrichment. The LLM must produce the canonical
// 6 fields; the absence of any one of them (Category is the
// "required" marker) is a schema-drift signal.
func (o *ollamaEnrichmentLLMClient) Enrich(ctx context.Context, req EnrichmentRequest) (*EnrichmentResponse, error) {
	if o == nil || o.client == nil {
		return nil, WrapHandlerNotConfigured("ollamaClient")
	}
	if req.ChunkID == "" {
		return nil, WrapHandlerNotConfigured("chunk_id")
	}

	// Build the canonical prompt. The system message declares
	// the 6-field JSON schema (canonical SSOT = selectSystemPrompt
	// consults the configured promptVersion + locale to pick V1 or
	// V2 at runtime — PR-011B follow-up i18n seam); the user
	// message provides the chunk context. The response is forced
	// to valid JSON via ollama's native JSON-mode (format = "json"
	// top-level body field, NOT nested in options).
	start := time.Now()
	messages := []types.Message{
		{
			Role:    "system",
			Content: o.selectSystemPrompt(""),
		},
		{
			Role:    "user",
			Content: buildEnrichmentUserPrompt(req),
		},
	}

	// Model selection: per-capability modelName (set by the
	// composition root to cfg.External.ParseArenaLLM when
	// non-empty) wins; otherwise falls back to the underlying
	// ollama client's default model (typically cfg.External.OllamaModel).
	model := o.modelName
	if model == "" {
		model = o.client.Model()
	}
	options := map[string]any{
		"model":      model,
		"keep_alive": "30m", // keep the model in GPU VRAM across retries
	}

	// json.RawMessage("\"json\"") is the canonical wire-shape
	// for ollama's native JSON-mode (the body field is the JSON
	// string "json", NOT the Go map). See ollama.Client.Chat
	// godoc for the field placement invariant.
	format := json.RawMessage(`"json"`)
	content, err := o.client.Chat(ctx, messages, options, format)
	if err != nil {
		// Network/HTTP/circuit-breaker failure path.
		// Retryable per godlike/07 (the ollama server may
		// come back online within the worker's exponential
		// backoff window).
		return nil, WrapLLMUnavailable(err)
	}

	// Parse the response. The 6-field JSON shape is the
	// canonical wire-format (declared in the system message);
	// json.Unmarshal into EnrichedFields handles missing
	// optional fields (Event / Round / Scene / Subject /
	// Entities) by zero-value initialization. Extra fields
	// returned by the LLM are silently ignored by Go's
	// default unmarshal behavior.
	var fields EnrichedFields
	if err := json.Unmarshal([]byte(content), &fields); err != nil {
		// Parse failure: malformed JSON, schema drift, or
		// non-JSON response despite the format hint. Terminal
		// after retries (the LLM needs a different prompt or
		// the response schema needs to evolve).
		return nil, WrapInvalidLLMResponse(fmt.Errorf("ollama chat content did not parse as EnrichedFields JSON: %w (content_preview=%q)", err, previewForLog(content)))
	}

	// godlike/07 NO-FAKE-AVAILABILITY: the canonical
	// "required" marker in the system message is `category`.
	// A successful ollama call that returns an empty Category
	// is a schema-drift signal (the LLM ignored the system
	// message). Surface as ErrEnrichmentInvalidLLMResponse so
	// the operator can identify the drift (and so the worker
	// doesn't silently no-op on retry).
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

// Model returns the per-capability model identifier. When
// modelName is empty, falls back to the underlying ollama
// client's default model. The handler logs this value as
// `result["model"]` for dashboard observability.
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

// buildEnrichmentUserPrompt constructs the user-message text
// from the EnrichmentRequest fields. Mirrors the canonical
// metadata-input shape (Title / Description / SourceProvider /
// SourceURL / StartSec / EndSec). Pure function (no side
// effects) for hermetic testability.
func buildEnrichmentUserPrompt(req EnrichmentRequest) string {
	var b strings.Builder
	b.WriteString("Enrich this video clip with the canonical 6-field metadata:\n\n")
	if req.Title != "" {
		b.WriteString("Title: ")
		b.WriteString(req.Title)
		b.WriteString("\n")
	}
	if req.Description != "" {
		b.WriteString("Description: ")
		b.WriteString(req.Description)
		b.WriteString("\n")
	}
	if req.SourceProvider != "" {
		b.WriteString("Source provider: ")
		b.WriteString(req.SourceProvider)
		b.WriteString("\n")
	}
	if req.SourceURL != "" {
		b.WriteString("Source URL: ")
		b.WriteString(req.SourceURL)
		b.WriteString("\n")
	}
	if req.StartSec != 0 || req.EndSec != 0 {
		b.WriteString(fmt.Sprintf("Time range: %.1fs - %.1fs\n", req.StartSec, req.EndSec))
	}
	return b.String()
}

// previewForLog returns a bounded-length preview of the LLM
// response for log inclusion in parse-failure error messages.
// Bounded at 200 chars to keep audit logs compact (canonical
// project pattern per the existing log helpers in
// pkg/logging).
func previewForLog(s string) string {
	const maxLen = 200
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "...(truncated)"
}

// Compile-time assertion: *ollamaEnrichmentLLMClient satisfies
// EnrichmentLLMClient. Catches signature drift at compile time
// per AGENTS.md Pattern 0 / godlike/06 SSOT.
var _ EnrichmentLLMClient = (*ollamaEnrichmentLLMClient)(nil)
