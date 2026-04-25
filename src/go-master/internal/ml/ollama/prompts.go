// Package ollama provides prompt templates and system prompts for script generation.
package ollama

// This file now serves as a reference point. The actual implementations have been
// moved to the following modular files:
// - system_prompt.go (system prompt generation based on language/tone)
// - prompt_builders.go (prompt building functions for text, YouTube, regeneration, entities)
// - client_generate.go (sanitizeInput, cleanScript, estimateDuration, countWords)
//
// Import this package to access all ollama prompt functionality.
