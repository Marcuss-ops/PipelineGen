package memory

// ─── Memory types (what we store and retrieve) ───

const (
	MemoryTypeChannelStyle     = "channel_style"
	MemoryTypeScriptStructure  = "script_structure"
	MemoryTypeTopicResearch    = "topic_research"
	MemoryTypeCharacterProfile = "character_profile"
	MemoryTypeSuccessfulHook   = "successful_hook"
	MemoryTypeBadPattern       = "bad_pattern"
	MemoryTypeImagePromptStyle = "image_prompt_style"
	MemoryTypeReusableIntro    = "reusable_intro"
	MemoryTypeReusableCTA      = "reusable_cta"
)

const (
	ModeGenerate     = "generate"
	ModeClipToScript = "clip_to_script"
)

// GenerationOutput represents a saved generation in gemma_script_outputs.
type GenerationOutput struct {
	ID              string
	ChannelID       string
	Mode            string
	Language        string
	Title           string
	Prompt          string
	NormalizedInput string
	InputHash       string
	OutputText      string
	OutputJSON      string
	Model           string
	JobID           string
	WordCount       int
	CreatedAt       string // TEXT in SQLite, not time.Time
}

// MemoryEntry represents a reusable memory in gemma_memory_entries.
type MemoryEntry struct {
	ID                 string
	ChannelID          string
	MemoryType         string
	TopicKey           string
	Title              string
	Summary            string
	ContentText        string
	ContentJSON        string
	SourceGenerationID string
	SourceJobID        string
	UsefulnessScore    float64
	CreatedAt          string // TEXT in SQLite
}

// ScriptChunk represents a segment of a script in gemma_script_chunks.
type ScriptChunk struct {
	ID            string
	GenerationID  string
	ChannelID     string
	ChunkIndex    int
	ChunkType     string
	TopicKey      string
	Title         string
	Text          string
	SearchText    string
	EmbeddingJSON string
	CreatedAt     string // TEXT in SQLite
}

// ─── Request/Response types ───

// MemoryPolicy bounds how much old memory/output can be reused during a single
// generation. Defaults are conservative to prevent near-duplicate runs while
// keeping the cache useful as context.
type MemoryPolicy struct {
	// MaxOldOutputs caps the number of "recent" / past-script memory items
	// injected into the writer prompt. 0 = use default (2).
	MaxOldOutputs int `json:"max_old_outputs"`

	// MaxMemoryChars caps the total size of the enriched memory context that
	// gets prepended to the writer prompt. 0 = use default (1800 chars,
	// roughly 450 tokens).
	MaxMemoryChars int `json:"max_memory_chars"`

	// SimilarityThreshold (0.0-1.0) is the n-gram Jaccard score above which a
	// freshly generated text is flagged as "near-duplicate" of a previous
	// output. 0 = use default (0.72).
	SimilarityThreshold float64 `json:"similarity_threshold"`

	// CacheHitLimit is the number of consecutive exact cache hits on the
	// same topic after which the system forces a fresh generation
	// (ForceRefresh=true) instead of returning a variant of the cached
	// output. 0 = use default (2). Set to -1 to never force-refresh.
	// This prevents the "same script loop" where every variant is still
	// too close to the original because the LLM keeps seeing the same
	// avoid list at low temperature.
	CacheHitLimit int `json:"cache_hit_limit"`
}

// DefaultMemoryPolicy returns the conservative defaults.
func DefaultMemoryPolicy() MemoryPolicy {
	return MemoryPolicy{
		MaxOldOutputs:       2,
		MaxMemoryChars:      1800,
		SimilarityThreshold: 0.72,
		CacheHitLimit:       2,
	}
}

// MemoryGateRequest is the input to the memory gate before generation.
type MemoryGateRequest struct {
	ChannelID    string       `json:"channel_id"`
	Title        string       `json:"title"`
	Prompt       string       `json:"prompt"`
	Language     string       `json:"language"`
	Mode         string       `json:"mode"` // "generate" or "clip_to_script"
	ForceRefresh bool         `json:"force_refresh"`
	UseMemory    bool         `json:"use_memory"` // default true
	Policy       MemoryPolicy `json:"policy"`
}

// MemoryGateResult is the output of the memory gate check.
type MemoryGateResult struct {
	// Level 1: exact cache
	CacheHit           bool              `json:"cache_hit"`
	SourceGenerationID string            `json:"source_generation_id,omitempty"`
	ExactOutput        *GenerationOutput `json:"exact_output,omitempty"`

	// Level 2+3: context for the prompt
	MemoryHits     []MemoryHit   `json:"memory_hits,omitempty"`
	SimilarChunks  []ScriptChunk `json:"similar_chunks,omitempty"`
	EnrichedPrompt string        `json:"enriched_prompt,omitempty"`
}

// MemoryHit is a single memory retrieval result with relevance info.
type MemoryHit struct {
	Entry     MemoryEntry `json:"entry"`
	Relevance float64     `json:"relevance"`
	Source    string      `json:"source"` // "channel_style", "topic_key", "search", "recent"
}

// ─── Save input types ───

// SaveGenerationInput is the data to save after a completed generation.
type SaveGenerationInput struct {
	ChannelID  string
	Mode       string
	Language   string
	Title      string
	Prompt     string
	JobID      string
	Model      string
	OutputText string
	OutputJSON string
	WordCount  int
}

// SaveMemoryInput is the data to extract and save as reusable memory.
type SaveMemoryInput struct {
	ChannelID          string
	MemoryType         string
	TopicKey           string
	Title              string
	Summary            string
	ContentText        string
	ContentJSON        string
	SourceGenerationID string
	SourceJobID        string
}
