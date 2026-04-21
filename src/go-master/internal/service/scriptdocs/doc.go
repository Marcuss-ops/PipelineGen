// Package scriptdocs provides services for generating AI-driven scripts and associating them with video clips.
//
// It orchestrates the full pipeline:
// 1. Script Generation: Uses LLMs (via Ollama) to generate multi-language scripts based on a topic.
// 2. Extraction: Extracts important phrases, entities, and keywords from the generated script.
// 3. Clip Association: Matches script phrases with relevant video clips from multiple sources:
//    - Artlist (pre-indexed or dynamically downloaded)
//    - StockDB (local database of indexed clips)
//    - Dynamic Clips (cached results from previous searches)
// 4. Persistence: Manages script documents and associated metadata in Google Drive.
//
// The package uses a concept mapping system (conceptMap) to translate abstract themes
// into specific visual keywords across multiple languages.
package scriptdocs
