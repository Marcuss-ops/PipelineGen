# Artlist LLM Query Generation - Technical Documentation

## Overview

This system uses LLM (Large Language Model) to transform documentary narrative text into visual search queries for Artlist stock footage platform. Instead of hardcoding keywords or using simple token extraction, it leverages semantic understanding to generate contextually relevant, visually-oriented search terms.

## Architecture Flow

```
┌─────────────────────────────────────────────────────────────────┐
│                    Timeline Planning Phase                       │
│  (internal/api/handlers/script/timeline_logic.go)              │
└───────────────────────────┬─────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────────┐
│              Segment Association Attempt                        │
│  - Check StockMatches (drive_stock)                            │
│  - Check ArtlistMatches (artlist_folder, artlist_dynamic)      │
└───────────────────────────┬─────────────────────────────────────┘
                            │
                     No matches found?
                            │
                            Yes
                            ▼
┌─────────────────────────────────────────────────────────────────┐
│         GenerateArtlistSearchSuggestions()                      │
│  (internal/service/visualquery/generator.go)                  │
└───────────────────────────┬─────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────────┐
│                    LLM Prompt Construction                      │
│  - System: "You are a visual search query generator..."        │
│  - User: buildVisualQueryPrompt()                              │
└───────────────────────────┬─────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────────┐
│                   Ollama LLM Call                              │
│  - Model: gemma3:4b (or configured model)                    │
│  - Endpoint: http://localhost:11434/api/chat                  │
│  - Context: Generator → Client → Chat()                       │
└───────────────────────────┬─────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────────┐
│                  Response Parsing & Validation                  │
│  - Extract JSON array from response                           │
│  - Validate: 2-4 words per query                             │
│  - Validate: only allowed characters (a-z, A-Z, 0-9, space)  │
│  - Validate: no banned words (the, a, an, is, was, etc.)      │
│  - Deduplicate queries                                        │
└───────────────────────────┬─────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────────┐
│              Store in seg.SearchSuggestions                     │
│  - Used by triggerBackgroundHarvest()                         │
│  - Creates Artlist jobs with Limit: 3                         │
│  - Downloads clips automatically                               │
└─────────────────────────────────────────────────────────────────┘
```

## Key Components

### 1. visualquery.Generator

**Location:** `internal/service/visualquery/generator.go`

**Main Function:**
```go
func GenerateArtlistSearchSuggestions(
    ctx context.Context,
    gen *ollama.Generator,
    topic string,
    subject string,
    narrative string,
    maxQueries int,
) []string
```

**Parameters:**
- `ctx`: Context for cancellation and timeouts
- `gen`: Ollama generator instance (uses gemma3:4b or configured model)
- `topic`: Overall video topic (e.g., "Archaeological Discovery")
- `subject`: Segment subject (e.g., "Ancient Cave Excavation")
- `narrative`: Full narrative text of the segment
- `maxQueries`: Maximum number of queries to generate (default: 3)

**Returns:** Slice of validated search query strings

### 2. Prompt Design

The prompt is constructed in `buildVisualQueryPrompt()`:

```
You are generating search queries for stock video platforms like Artlist.

Given a documentary sentence, create 3 short visual search queries.

Rules:
- Each query must be 2 to 4 words.
- Use concrete visual concepts.
- Avoid abstract words.
- Avoid filler words (the, a, an, is, was, were, are, been, have, has, had, but, and, or).
- Avoid full sentences.
- Do not copy the sentence mechanically.
- Prefer scenes, objects, environments, actions, historical period, scientific setting.
- Return only valid JSON array of strings.

Examples provided...

Sentence: "<narrative text>"
Context topic: "<topic>"
Segment subject: "<subject>"

JSON:
```

### 3. Validation Logic

Queries are validated in `isValidVisualQuery()`:

**Criteria:**
1. **Word Count:** 2-4 words per query
2. **Character Set:** Only letters (a-z, A-Z), numbers (0-9), spaces, and hyphens allowed
3. **Banned Words Filter:** Removes queries containing:
   - Articles: the, a, an
   - Verbs: is, was, were, are, been, have, has, had
   - Conjunctions: but, and, or
   - Others: then, they, their, further, each, continues, beginning, comprehend

**Deduplication:** Uses a `seen` map to prevent duplicate queries

### 4. Fallback Mechanism

If LLM call fails or returns invalid responses, the system falls back to `buildFallbackQueries()`:
1. Uses the segment subject/topic as the first query
2. Extracts first 3 words from narrative as secondary query
3. Falls back to "documentary landscape" if nothing else works

## Integration Points

### In timeline_logic.go

The generator is called in `BuildTimelinePlan()` after segment association:

```go
// Line ~178-195 in timeline_logic.go
if len(seg.StockMatches) == 0 && len(seg.ArtlistMatches) == 0 {
    zap.L().Info("segment has no matches, generating LLM-based Artlist queries",
        zap.Int("segment_index", seg.Index),
        zap.String("subject", seg.Subject),
    )

    llmQueries := GenerateArtlistSearchSuggestions(
        ctx, gen, req.Topic,
        firstNonEmpty(seg.CanonicalSubject, seg.Subject),
        seg.NarrativeText, DefaultMaxQueries,
    )

    seg.SearchSuggestions = sliceutil.UniqueStrings(
        append(seg.SearchSuggestions, llmQueries...),
    )
}
```

### In handler_generate.go

`triggerBackgroundHarvest()` reads `seg.SearchSuggestions` and creates Artlist jobs:

```go
// Line ~270-305 in handler_generate.go
for tag := range uniqueTags {
    req := &artlist.RunTagRequest{
        Term:         tag,
        Limit:        3,  // Download 3 clips per query
        Strategy:     "verify",
        ClipDuration: 7,  // 7 second clips
        Width:        1920,
        Height:       1080,
        FPS:          30,
    }
    // Enqueue job to jobsService...
}
```

## Logging

Comprehensive logging is added at each stage:

1. **Entry Point:** `visualquery.GenerateArtlistSearchSuggestions()` logs input parameters
2. **LLM Request:** Logs the prompt being sent to Ollama
3. **LLM Response:** Logs response length and preview
4. **Parsing:** Logs JSON parse failures
5. **Validation:** Debug logs for rejected queries
6. **Success:** Logs final validated queries
7. **Fallback:** Warns when falling back to non-LLM method

**Log Example:**
```
INFO  GenerateArtlistSearchSuggestions: starting LLM query generation
      topic="Archaeological Discovery"
      subject="Ancient Cave Excavation"
      narrative_length=245
      max_queries=3

DEBUG GenerateArtlistSearchSuggestions: sending request to LLM
      prompt="You are generating search queries..."

INFO  GenerateArtlistSearchSuggestions: LLM response received
      response_length=89
      response_preview='["archaeological excavation", "ancient cave discovery", "rock layer"]'

INFO  GenerateArtlistSearchSuggestions: successfully generated queries
      queries=["archaeological excavation","ancient cave discovery","rock layer"]
```

## Example Transformations

| Input Narrative | LLM Output Queries |
|-----------------|-------------------|
| "Further excavation is planned, and with each new layer of rock exposed..." | `["archaeological excavation", "ancient cave discovery", "rock layer excavation"]` |
| "Analysis of pollen and charcoal fragments within the chambers..." | `["archaeology laboratory", "ancient sample analysis", "prehistoric cave painting"]` |
| "The city disappeared beneath volcanic ash..." | `["ancient ruined city", "volcanic ash landscape", "archaeological ruins"]` |
| "The scientist examined the sample under ultraviolet light..." | `["scientist laboratory analysis", "microscope sample research", "ultraviolet light experiment"]` |

## Model Configuration

**Current Model:** `gemma3:4b` (located via `ollama list`)

**Model Selection:** The model is determined by the Ollama client configuration passed to `Generator`. To change models, update the client initialization in `cmd/server/main.go`.

**Fallback Chain:** The Ollama client has built-in model fallback (see `client_core.go`):
1. Primary model (e.g., gemma3:4b)
2. Fallback models (if configured)
3. If all fail, use `buildFallbackQueries()`

## Error Handling

1. **Nil Generator:** Falls back to `buildFallbackQueries()`
2. **LLM Call Failure:** Logs error, falls back to `buildFallbackQueries()`
3. **Invalid JSON Response:** Logs warning, returns nil (triggering fallback)
4. **No Valid Queries:** Logs warning, falls back to `buildFallbackQueries()`
5. **Timeout:** Context cancellation propagates through Ollama client

## Testing

To test the query generator manually:

```bash
# Ensure Ollama is running with gemma3:4b
ollama list | grep gemma3

# Test via API
curl -X POST http://127.0.0.1:8080/api/script-docs/generate \
  -H "Content-Type: application/json" \
  -d '{
    "topic": "Archaeological Discovery",
    "duration": 60,
    "language": "english",
    "template": "documentary",
    "source_text": "Further excavation is planned..."
  }'
```

Check logs:
```bash
journalctl -u pipelinegen -f | grep "GenerateArtlistSearchSuggestions"
```

## Files Modified/Created

1. **Removed:** `internal/api/handlers/script/artlist_query_generator.go` (Deprecated)
2. **Maintained:** `internal/api/handlers/script/timeline_logic.go` (using `visualquery` package)
3. **Documentation:** `docs/artlist-llm-query-generation.md` (this file)

## Benefits Over Previous Approach

| Aspect | Old Method (token extraction) | New Method (LLM) |
|--------|------------------------------|-------------------|
| Query Quality | "further excavation planned each" | "archaeological excavation" |
| Context Awareness | None (mechanical) | Semantic understanding |
| Visual Relevance | Poor | High (designed for stock footage) |
| Hardcoding | Some banned words | Only validation rules |
| Scalability | Breaks with new topics | Works with any topic |
| Abstract Concepts | Fails | Handles correctly |

## Future Improvements

1. **Batch Processing:** Generate queries for multiple segments in one LLM call
2. **Caching:** Cache queries for identical narrative segments
3. **Dynamic Model Selection:** Choose model based on query complexity
4. **Query Scoring:** Rate queries by visual relevance (0-100 score)
5. **A/B Testing:** Compare LLM queries vs. token extraction performance
