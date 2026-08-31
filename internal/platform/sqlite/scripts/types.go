// Package scripts owns the SQLite-backed repositories for the gemma
// script-generation memory system: exact output cache (Level 1),
// memory entries (Level 2), and chunk similarity search (Level 2).
// This file declares the data shapes the repositories serialise
// onto the three tables (gemma_script_outputs, gemma_memory_entries,
// gemma_script_chunks) defined in migrations/sqlite/013_create_gemma_memory.sql
// and elided in 014_create_research_cache.sql.
//
// Architecture rationale: pre-66c646b5 these structs lived inline in
// memory.go alongside the SQL. They were promoted to a shared types
// file so that engine.go in internal/capabilities/scripts can also
// reference them (SaveMemory returns *GenerationOutput, MemoryGateContext
// carries *Gemmamemory.GenerationOutput). Build closure for the
// application scripts package requires these shapes to live in this
// package (scripts) — the higher-level wrappers will alias them via
// `type GenerationOutput = scripts.GenerationOutput` later.
package scripts

import "time"

// GenerationOutput is one row of `gemma_script_outputs`. The schema
// is wide (13 columns) because the Level-1 exact cache must record
// enough metadata to re-derive a deterministic fresh-variant prompt
// without hitting Ollama again. The id field follows the format
// "gen_<12-char prefix>" — see SaveGeneration below.
type GenerationOutput struct {
	ID              string    `json:"id"`
	ChannelID       string    `json:"channel_id"`
	Mode            string    `json:"mode"`
	Language        string    `json:"language"`
	Title           string    `json:"title"`
	Prompt          string    `json:"prompt"`
	NormalizedInput string    `json:"normalized_input"`
	InputHash       string    `json:"input_hash"`
	OutputText      string    `json:"output_text"`
	OutputJSON      string    `json:"output_json"` // raw JSON blob for structured outputs
	Model           string    `json:"model"`
	JobID           string    `json:"job_id"`
	WordCount       int       `json:"word_count"`
	CreatedAt       time.Time `json:"created_at"`
}

// MemoryEntry is one row of `gemma_memory_entries`. The schema tracks
// a topic_key-clustered lookup so that cross-channel searches
// (FindMemoryCrossChannel) can issue a single ORDER BY usefulness_score
// DESC query. last_used_at is updated by TouchMemory (model of
// usefulness decay) — see migrations/sqlite/020_gemma_memory_last_used.sql.
type MemoryEntry struct {
	ID                 string    `json:"id"`
	ChannelID          string    `json:"channel_id"`
	MemoryType         string    `json:"memory_type"`
	TopicKey           string    `json:"topic_key"`
	Title              string    `json:"title"`
	Summary            string    `json:"summary"`
	ContentText        string    `json:"content_text"`
	ContentJSON        string    `json:"content_json"`
	SourceGenerationID string    `json:"source_generation_id"`
	SourceJobID        string    `json:"source_job_id"`
	UsefulnessScore    float64   `json:"usefulness_score"`
	CreatedAt          time.Time `json:"created_at"`
}

// ScriptChunk is one row of `gemma_script_chunks`. The search_text
// column is the lowercased, punctuation-stripped projection of `text`
// kept in sync at INSERT time by NormalizeSearchText. EmbeddingJSON is
// optional — when present, similarity search can short-circuit the
// LIKE-based scanner (Phase 2 of the intelligence roadmap).
type ScriptChunk struct {
	ID            string    `json:"id"`
	GenerationID  string    `json:"generation_id"`
	ChannelID     string    `json:"channel_id"`
	ChunkIndex    int       `json:"chunk_index"`
	ChunkType     string    `json:"chunk_type"`
	TopicKey      string    `json:"topic_key"`
	Title         string    `json:"title"`
	Text          string    `json:"text"`
	SearchText    string    `json:"search_text"`
	EmbeddingJSON string    `json:"embedding_json"`
	CreatedAt     time.Time `json:"created_at"`
}

// SaveGenerationInput is the argument bundle for the SaveGeneration
// upsert. NormalizedInput and InputHash are computed by the handler
// calling code (gemma normaliser pipeline) so they don't belong
// inline — they vary per channel style and must be derived against
// the same canonicaliser that produced the row in the first place.
type SaveGenerationInput struct {
	ChannelID  string `json:"channel_id"`
	Mode       string `json:"mode"`
	Language   string `json:"language"`
	Title      string `json:"title"`
	Prompt     string `json:"prompt"`
	Model      string `json:"model"`
	JobID      string `json:"job_id"`
	OutputText string `json:"output_text"`
	OutputJSON string `json:"output_json"`
	WordCount  int    `json:"word_count"`
}

// SaveMemoryInput is the argument bundle for SaveMemory. It mirrors
// the MemoryEntry columns minus the id (auto-generated), the
// timestamps (set by SQL default), and the initial usefulness_score
// (starts at 1.0).
type SaveMemoryInput struct {
	ChannelID          string `json:"channel_id"`
	MemoryType         string `json:"memory_type"`
	TopicKey           string `json:"topic_key"`
	Title              string `json:"title"`
	Summary            string `json:"summary"`
	ContentText        string `json:"content_text"`
	ContentJSON        string `json:"content_json"`
	SourceGenerationID string `json:"source_generation_id"`
	SourceJobID        string `json:"source_job_id"`
}
