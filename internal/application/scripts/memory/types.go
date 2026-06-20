package memory

import (
	"context"

	scriptrepo "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/scripts"
)

// ── Type aliases — keep the application layer talking the same names ────────

// MemoryEntry is the canonical memory row exposed to the Memory Gate.
// Type alias keeps the consumer code in `application/scripts/` readable:
// callers don't have to write `scriptrepo.MemoryEntry` everywhere.
type MemoryEntry = scriptrepo.MemoryEntry

// GenerationOutput is the Level-1 exact-cache row.
type GenerationOutput = scriptrepo.GenerationOutput

// ScriptChunk is the Level-2 chunk used for LIKE similarity search.
// We re-export it here so retrieval.go can compose projections
// (MemoryHit{Entry: MemoryEntry from a chunk}) without cross-package noise.
type ScriptChunk = scriptrepo.ScriptChunk

// SaveGenerationInput re-exports the upsert payload used by SaveAfterGeneration.
type SaveGenerationInput = scriptrepo.SaveGenerationInput

// SaveMemoryInput re-exports the memory upsert payload.
type SaveMemoryInput = scriptrepo.SaveMemoryInput

// ── Memory type taxonomy ────────────────────────────────────────────────────
//
// These constants are the strings persisted in `gemma_memory_entries.memory_type`
// AND broadcasted across the application as `MemoryHit.MemoryType = X`.
// They are stable across releases — adding new types is allowed, renaming is a
// breaking migration.

// MemoryPolicy is the canonical gate-policy blob. prompt_builders.go and
// similarity.go both branch on its fields; introducing this type as a
// struct (rather than per-flag parameters) makes the cross-package
// surface auditable and lets the worker pool pass a single struct
// across the gemma / ollama boundary.
//
// Fields:
//
//	UseMemory              — gate is enabled at all (master switch)
//	ForceRefresh           — bypass Level-1 cache (treat every request as cache miss)
//	MaxMemoryChars         — cap on EnrichedPrompt length (chars ≠ bytes for multi-byte scripts)
//	MaxMemories            — cap on MemoryHits returned by CheckGate
//	MaxChunks              — cap on script_chunk hits the LIKE search may surface
//	FreshVariantEnabled    — when true, exact-cache hits are rewritten through
//	                        BuildFreshVariantPrompt so the LLM sees a NEW prompt
//	                        (prevents user perception of "duplicate output")
//	CacheHitLimit          — number of recent exact-cache hits allowed before
//	                        a single topic triggers a forced refresh
type MemoryPolicy struct {
	UseMemory           bool
	ForceRefresh        bool
	MaxMemoryChars      int
	MaxMemories         int
	MaxChunks           int
	FreshVariantEnabled bool
	CacheHitLimit       int
	SimilarityThreshold float64
}

// DefaultMemoryPolicy is the policy used when one is not supplied via
// request context. Tuned for the pre-66c646b5 baseline: gate on,
// force-refresh off, 8 memories + 5 chunks per cascade plus a 12 KB
// ceiling on the enriched prompt so the writer LLM never sees a context
// block larger than its effective window preamble.
var DefaultMemoryPolicyValue = MemoryPolicy{
	UseMemory:           true,
	ForceRefresh:        false,
	MaxMemoryChars:      12000,
	MaxMemories:         8,
	MaxChunks:           5,
	FreshVariantEnabled: false,
	CacheHitLimit:       3,
	SimilarityThreshold: 0.78,
}

// NewMemoryPolicy returns a pointer to DefaultMemoryPolicy. Callers that
// need to override one field can &DefaultMemoryPolicy then set the field.
func NewMemoryPolicy() *MemoryPolicy {
	p := DefaultMemoryPolicyValue
	return &p
}

// DefaultMemoryPolicy returns the canonical policy as a value (not
// pointer). prompt_builders.go and similarity.go call it as a function
// (`policy = DefaultMemoryPolicy()`) — a matching var declaration in
// the same package would shadow this function. Keeping both the var
// (`DefaultMemoryPolicyValue`) AND the function (this one) lets
// changing-a-flag callers and value-copy callers coexist.
func DefaultMemoryPolicy() MemoryPolicy {
	return DefaultMemoryPolicyValue
}

// NewMemoryGateRequest constructs a MemoryGateRequest pre-populated
// with DefaultMemoryPolicy. Without this constructor, callers that
// build a MemoryGateRequest literal silently get Policy=zero (gate
// disabled). Promoting through this constructor means the gate stays
// on by default — the pre-66c646b5 baseline.
func NewMemoryGateRequest(channelID, title, prompt, language, mode string) MemoryGateRequest {
	return MemoryGateRequest{
		ChannelID: channelID,
		Title:     title,
		Prompt:    prompt,
		Language:  language,
		Mode:      mode,
		UseMemory: true,
		Policy:    DefaultMemoryPolicyValue,
	}
}

const (
	// MemoryTypeChannelStyle holds per-channel style rules (always relevant).
	MemoryTypeChannelStyle = "channel_style"

	// MemoryTypeTopicResearch is a topic-specific research blob.
	MemoryTypeTopicResearch = "topic_research"

	// MemoryTypeCharacterProfile is a recurring character / person / entity.
	MemoryTypeCharacterProfile = "character_profile"

	// MemoryTypeSuccessfulHook holds hooks proven to land well for the topic.
	MemoryTypeSuccessfulHook = "successful_hook"

	// MemoryTypeScriptStructure holds structural templates proven to work.
	MemoryTypeScriptStructure = "script_structure"

	// MemoryTypeBadPattern holds anti-patterns to avoid.
	MemoryTypeBadPattern = "bad_pattern"

	// MemoryTypeReusableCTA holds CTA templates proven to convert.
	MemoryTypeReusableCTA = "reusable_cta"

	// MemoryTypeScriptChunk is synthesised at retrieval time from
	// gemma_script_chunks; never persisted into gemma_memory_entries.
	MemoryTypeScriptChunk = "script_chunk"
)

// ── Gate types ──────────────────────────────────────────────────────────────

// MemoryGateRequest is the input contract for Service.CheckGate.
// The repository has no opinion on UseMemory / ForceRefresh — those are
// gate-level decisions owned by the engine.
//
// The Policy field was added during Stage 2 closure to thread the new
// MemoryPolicy struct (declared above) through the gate. Pre-66c646b5
// the gate accepted per-flag parameters; consolidating them into a
// MemoryPolicy blob keeps prompt_builders.go and similarity.go on the
// same shape and reduces cross-package surface to one struct.
type MemoryGateRequest struct {
	ChannelID    string
	Title        string
	Prompt       string
	Language     string
	Mode         string
	UseMemory    bool
	ForceRefresh bool
	Policy       MemoryPolicy
}

// MemoryGateResult is the output contract for Service.CheckGate.
// CacheHit + ExactOutput represent the Level-1 fast path;
// MemoryHits + EnrichedPrompt represent the Level-2 enrichment path.
type MemoryGateResult struct {
	CacheHit           bool
	EnrichedPrompt     string
	MemoryHits         []MemoryHit
	SourceGenerationID string
	ExactOutput        *GenerationOutput
}

// MemoryHit represents one synthesised match returned by Level 1-3
// retrieval. The Entry is always a MemoryEntry (the script_chunk
// variant is synthesised from a ScriptChunk row before projection).
type MemoryHit struct {
	// Source is a label identifying the retrieval channel:
	//   "channel_style" | "topic_key" | "cross_channel" | "search" | "recent"
	Source string
	// Entry is the MemoryEntry payload carried by this hit.
	Entry MemoryEntry
	// Relevance is in [0, 1]; used for sorting and limiting the cascade.
	Relevance float64
}

// ── Repository contract ─────────────────────────────────────────────────────

// Repository is the narrow interface Service depends on for the Memory Gate.
// The concrete implementation is *scriptrepo.MemoryRepository (in
// /internal/infrastructure/database/sqlite/scripts/memory.go), but the
// interface lets retrieval.go be tested with a fake.
type Repository interface {
	FindExactOutput(ctx context.Context, channelID, mode, inputHash string) (*GenerationOutput, error)
	FindMemoryByChannel(ctx context.Context, channelID, memoryType string, limit int) ([]MemoryEntry, error)
	FindMemoryByTopicKey(ctx context.Context, channelID, topicKey string, limit int) ([]MemoryEntry, error)
	FindMemoryCrossChannel(ctx context.Context, excludeChannelID, memoryType string, limit int) ([]MemoryEntry, error)
	FindSimilarChunksBySearchText(ctx context.Context, channelID string, tokens []string, limit int) ([]ScriptChunk, error)
	SaveGeneration(ctx context.Context, input SaveGenerationInput, normalizedInput, inputHash string) (string, error)
	SaveChunks(ctx context.Context, generationID, channelID, title, topicKey string, chunks []string) error
	SaveMemory(ctx context.Context, input SaveMemoryInput) (string, error)
	CountExactOutputsByTitle(ctx context.Context, channelID, mode, title string) (int, error)
	DeleteExactOutputsByTitles(ctx context.Context, titles []string) (int64, error)
}
