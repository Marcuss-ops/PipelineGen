# Script Generation Pipeline - Technical Documentation

## Overview

The script generation pipeline (`PipelineGen`) is a Go-based backend service that generates documentary-style scripts with automated visual asset matching. It uses LLM (Ollama) for text generation and integrates with stock footage platforms (Artlist) and Google Drive.

**Entry Point**: `POST /api/script-docs/generate`

Note: the implementation has since been consolidated under `internal/api/handlers/script/`.
Package names below reflect the current behavior, but some internal helpers were moved or folded into that package during cleanup.

---

## Architecture Flow

```
HTTP Request
    ↓
ScriptDocsHandler.Generate()
    ↓
BuildScriptDocument()
    ↓
┌───────────────────────────────────────────────────┐
│  1. LLM Narrative Generation (ollama)            │
│     → GenerateScript()                            │
│     → Returns: title, narrative, metadata          │
└───────────────────────────────────────────────────┘
    ↓
┌───────────────────────────────────────────────────┐
│  2. Timeline Planning (LLM Segmentation)          │
│     → BuildTimelinePlan()                         │
│     → Segments narrative into timed sections       │
│     → Each segment: subject, narrative, timestamps │
└───────────────────────────────────────────────────┘
    ↓
┌───────────────────────────────────────────────────┐
│  3. Batch Visual Query Generation (LLM)          │
│     → GenerateBatchArtlistVisualQueries()          │
│     → For each segment, generate:                  │
│       - visual_subject (2-4 words)                │
│       - visual_caption (5-15 words)               │
│       - search_suggestions (2-4 word queries)     │
└───────────────────────────────────────────────────┘
    ↓
┌───────────────────────────────────────────────────┐
│  4. Asset Matching (per segment)                  │
│     → associateSegment()                          │
│     → Stock matches (Drive stock footage)          │
│     → Artlist matches (Artlist clips)             │
│     → Drive matches (Google Drive folders)         │
└───────────────────────────────────────────────────┘
    ↓
┌───────────────────────────────────────────────────┐
│  5. Document Assembly                             │
│     → Build sections:                              │
│       - Metadata (topic, duration, language)       │
│       - Narrative script                          │
│       - Timeline with asset associations           │
│       - Stock sections                            │
│       - Artlist suggestions                       │
│       - Image planning                            │
│     → Upload to Google Docs                        │
└───────────────────────────────────────────────────┘
    ↓
HTTP Response (doc_id, doc_url, full_content)
```

---

## Detailed Component Breakdown

### 1. HTTP Handler Layer

**File**: `internal/api/handlers/script/handlers/handler_generate.go`

```go
func (h *ScriptDocsHandler) Generate(c *gin.Context)
```

- Validates request using `ScriptDocsRequest` struct
- Applies defaults: language="it", template="documentary", duration=60
- Calls `BuildScriptDocument()`
- Returns JSON with `doc_id`, `doc_url`, `full_content`

**Request Format**:
```json
{
  "topic": "Evoluzione dei trasporti urbani",
  "duration": 60,
  "language": "it",
  "template": "documentary"
}
```

---

### 2. Script Document Builder

**File**: `internal/api/handlers/script/script_docs_builder.go`

```go
func BuildScriptDocument(
    ctx context.Context,
    gen *ollama.Generator,
    req ScriptDocsRequest,
    dataDir, pythonScriptsDir, nodeScraperDir string,
    StockDriveRepo, ArtlistRepo, ClipsRepo *clips.Repository,
    artlistService *artlist.Service,
    imgService *imgservice.Service,
    assocService *association.Service,
) (*ScriptDocument, error)
```

**Responsibilities**:
1. Generate narrative using LLM (`gen.GenerateScript()`)
2. Extract structured data (title, narrative text, keywords, entities)
3. Build timeline plan with asset matching
4. Assemble all sections into `ScriptDocument`
5. Optionally upload to Google Docs

---

### 3. LLM Narrative Generation

**File**: `internal/ml/ollama/generate.go`

```go
func (g *Generator) GenerateScript(
    ctx context.Context,
    req types.TextGenerationRequest,
) (*types.GenerationResult, error)
```

**Prompt Construction** (in `prompts/prompt_builders.go`):
- Uses template based on `req.Template` (default: "documentary")
- Language-specific prompts (Italian/English)
- Includes topic, duration, tone
- Returns structured JSON with:
  - `title`: Document title
  - `narrative`: Full script text with timing markers
  - `keywords`: Extracted keywords
  - `entities`: Named entities (people, places, etc.)

**Default Values** (centralized in `types/defaults.go`):
```go
DefaultLanguage = "it"
DefaultTemplate = "documentary"
DefaultDuration = 600  // 10 minutes (PipelineGen target = ~1400 words at 140 wpm)
DefaultTone = "documentary"
```

The previous default of 60 seconds produced scripts of ~140 words,
which was always clamped to 200 words by the `MinWords` floor and
resulted in tiny one-liner outputs. The new 600s default matches the
batch endpoint (`handler_batch.go`) and produces realistically sized
YouTube-style scripts.

---

### 4. Timeline Planning

**File**: `internal/api/handlers/script/timeline_logic.go`

```go
func BuildTimelinePlan(
    ctx context.Context,
    gen *ollama.Generator,
    req ScriptDocsRequest,
    dataDir, nodeScraperDir, sourceText, narrative string,
    stockRepo, artlistRepo, clipsRepo *clips.Repository,
    artlistService *artlist.Service,
    assocService *association.Service,
) (*TimelinePlan, error)
```

**Process**:

#### 4.1 LLM Segmentation
- Sends narrative to LLM with segmentation prompt
- LLM returns `timelineLLMPlan` with segments:
  - `Index`: Segment number
  - `StartTime`, `EndTime`: Timestamps in seconds
  - `Subject`: Segment subject
  - `NarrativeText`: Segment text
  - `Keywords`, `Entities`: Extracted data

**Fallback**: If LLM fails, creates single segment covering entire narrative.

#### 4.2 Batch Visual Query Generation
**File**: `internal/api/handlers/script/artlist_query_generator.go`

```go
func GenerateBatchArtlistVisualQueries(
    ctx context.Context,
    gen *ollama.Generator,
    topic string,
    segments []BatchSegmentInput,
    maxQueriesPerSegment int,
) map[int]VisualQueryResult
```

- Pre-generates visual queries for ALL segments in one LLM call
- Returns map: `segmentIndex → VisualQueryResult`
- Caches results to avoid re-generation

**VisualQueryResult**:
```go
type VisualQueryResult struct {
    VisualSubject string   `json:"visual_subject"`  // 2-4 words
    VisualCaption string   `json:"visual_caption"`  // 5-15 words
    Queries       []string `json:"queries"`         // 2-4 word search terms
}
```

**Prompt Example**:
```
Input: "Scientists mix chemicals in a lab to test new battery technology..."
Output: {
  "visual_subject": "science laboratory",
  "visual_caption": "Researchers conducting experiments with beakers and microscopes",
  "queries": ["science lab experiment", "battery research", "chemistry laboratory"]
}
```

#### 4.3 Segment Processing Loop

For each segment:

1. **Subject Resolution** (`resolveTimelineSegmentSubject()`):
   - Check association service for direct folder matches
   - Use entity-based subject if available
   - Fallback to topic if no better subject found

2. **Normalization**:
   - Canonicalize keywords and entities
   - Map to standard vocabulary
   - Implemented by the script package's internal catalog normalizer helper

3. **Association Hints**:
   - Apply preferred stock paths from associations
   - Set `PreferredStockGroup` and `PreferredStockPaths`

4. **Asset Matching** (`associateSegment()`):
   - Search Stock Drive repositories
   - Search Artlist database
   - Search clip repositories
   - Score and rank matches

5. **Visual Fields Population**:
   - Always populate from `batchResults` (generated in step 4.2)
   - `seg.VisualSubject`, `seg.VisualCaption`, `seg.SearchSuggestions`

6. **Fallback Logic**:
   - If no matches found: queries already generated in batch
   - Use `GenerateArtlistVisualQuery()` only if batch missed a segment

---

### 5. Visual Query Generator

**File**: `internal/api/handlers/script/artlist_query_generator.go`

**Key Functions**:

#### `GenerateArtlistVisualQuery()`
- Single segment query generation
- Checks cache first (`queryCache`)
- Calls Ollama LLM with structured prompt
- Parses JSON response
- Validates queries (2-4 words, no banned words)
- Falls back to subject/topic if LLM fails

#### `GenerateBatchArtlistVisualQueries()`
- Batch processing for efficiency
- Single LLM call for multiple segments
- Returns map indexed by segment index
- **Critical Fix**: Uses `seg.Index` directly (not loop index `i`)

**Cache Implementation**:
```go
var (
    queryCache   = make(map[string]VisualQueryResult)
    queryCacheMu sync.RWMutex
)

func buildCacheKey(topic, subject, narrative string, maxQueries int) string {
    hashInput := fmt.Sprintf("%s|%s|%s|%d|%s", topic, subject, narrative, maxQueries, cacheVersion)
    return fmt.Sprintf("%x", hash(hashInput))
}
```

**Fallback Strategy** (in `buildFallbackQueries()`):
1. Use `subject` as primary query
2. Add `topic` if different and needed
3. Final fallback: `"documentary landscape"`

**Prompt Design** (`buildVisualQueryPrompt()`):
- System message: "You are a visual search query generator..."
- Examples from varied domains (archaeology, science, politics, sports)
- Rules: 2-4 word queries, concrete visual concepts, no filler words

---

### 6. Asset Matching System

**File**: `internal/service/association/service.go`

**Matching Sources**:
- **Stock Drive**: Google Drive folders with stock footage
- **Artlist**: Artlist.io clips (via database or API)
- **Clips**: Local clip repository

**Scoring**:
- Token overlap with subject/keywords/entities
- Exact folder name matches
- Path-based similarity

**Output**: `[]ScoredMatch`
```go
type ScoredMatch struct {
    Title   string
    Path    string
    Score   int
    Source  string  // "drive_stock", "artlist_stock", "artlist_folder"
    Link    string
    Details string
}
```

---

### 7. Document Assembly

**Sections** (in `BuildScriptDocument()`):

1. **Metadata Section** (`renderMetadata()`):
   - Topic, duration, language, template, mode

2. **Narrative Script** (`renderNarrativeScript()`):
   - Full text with audio cues (music, ambience)
   - Markdown formatted

3. **Timeline** (`renderTimeline()`):
   - Each segment with:
     - Timestamp range
     - Subject and canonical subject
     - Narrative text (opening/closing sentences)
     - Visual subject, caption, search suggestions
     - Asset matches (Stock, Artlist, Drive)
     - Preferred stock group and paths

4. **Stock Section** (`buildStockMatchingSection()`):
   - Available stock footage
   - Folder structure

5. **Artlist Section** (`buildArtlistSection()`):
   - Suggested search tags
   - Direct links to Artlist queries

6. **Image Planning** (`buildImagePlanningSection()`):
   - Entities with image suggestions
   - Google Images integration

7. **Important Phrases/Words**:
   - First N sentences (currently hardcoded, TODO: LLM extraction)
   - Frequent non-stopwords (currently English-only, TODO: multilingual)

---

### 8. Google Docs Integration

**File**: `internal/storage/drive/doc_client.go`

- Uploads assembled document to Google Drive
- Returns `doc_id` and `doc_url`
- Uses service account credentials

---

## Key Data Structures

### ScriptDocsRequest
```go
type ScriptDocsRequest struct {
    Topic     string `json:"topic"`
    Duration  int    `json:"duration"`
    Language  string `json:"language"`
    Template  string `json:"template"`
}
```

### TimelineSegment
```go
type TimelineSegment struct {
    Index             int
    StartTime        float64
    EndTime          float64
    Subject          string
    CanonicalSubject string
    NarrativeText    string
    Keywords         []string
    Entities         []string
    VisualSubject    string      // LLM-generated
    VisualCaption    string      // LLM-generated
    SearchSuggestions []string  // LLM-generated
    StockMatches     []ScoredMatch
    ArtlistMatches   []ScoredMatch
    DriveMatches     []ScoredMatch
    PreferredStockGroup  string
    PreferredStockPaths  []string
    PreferredStockReason string
}
```

### VisualQueryResult
```go
type VisualQueryResult struct {
    VisualSubject string   `json:"visual_subject"`
    VisualCaption string   `json:"visual_caption"`
    Queries       []string `json:"queries"`
}
```

---

## Error Handling & Fallbacks

| Failure Point | Fallback Strategy |
|---------------|-------------------|
| LLM narrative generation fails | Returns error (no fallback currently) |
| LLM segmentation fails | Single segment covering full narrative |
| Batch visual query fails | Individual LLM calls per segment |
| Individual LLM query fails | `buildFallbackQueries()` using subject/topic |
| No asset matches | Empty matches, visual queries still populated |
| Google Docs upload fails | Returns content in HTTP response |

---

## Length Normalization (scriptcore.Engine)

Length normalization now lives in `internal/service/scriptcore/normalize.go`
and is shared by every endpoint through `Engine.GenerateAndNormalize`.
It uses **percentage-based tolerance** that scales with target length,
not a fixed `±300 words` window:

```go
// internal/service/scriptcore/normalize.go
func WordCountBounds(targetWords int) (int, int) {
    switch {
    case targetWords <= 800:  tolerance = 0.15  // 15% for short sections
    case targetWords <= 2000: tolerance = 0.10  // 10% for medium
    default:                   tolerance = 0.08  //  8% for long
    }
    ...
}
```

The `±300` constant was a bad fit: it was 60% of a 500-word target
and only 10% of a 3000-word one. The percentage model gives every
target the same relative precision.

`CalculateTargetWords` also rounds seconds up to minutes with
`math.Ceil`, so a 90-second request produces a 2-minute target
(~280 words) instead of silently truncating to 60 seconds.

---

## Engine.WriteScript (unified pipeline)

As of June 2026 the script-writing logic lives in a single method,
`scriptcore.Engine.WriteScript`. Handlers construct a
`WriteScriptRequest` and call it; the engine takes care of the rest.

```go
type WriteScriptRequest struct {
    Topic, Title, Language, Tone, Model, Mode string
    ChannelID                                  string
    UseMemory, ForceRefresh, SaveToDB          bool
    Duration, MinWords, NumPredict             int
    Temperature                                float64
    Prompt, SourceText, WebContext, Guidelines string
    ScriptID                                   int64 // optional override
    PromptVersion, EditorPromptVersion, QAPromptVersion string
}

func (e *Engine) WriteScript(ctx, req) (*WriteScriptResult, error)
```

The full flow inside `WriteScript`:

1. **Pre-save** the script with `status="generating"` so generation
   logs get a real `script_id` (the previous `LogGeneration(ctx, 0, ...)`
   inside `GenerateAndNormalize` violated the FK on `script_generation_logs`
   and produced orphan rows).
2. **Memory gate** via `CheckMemoryGate` + `ResolvePrompt`.
3. **Exact cache short-circuit** when the gate returns a hit.
4. **Generate + normalize** via `GenerateAndNormalize`.
5. **Save to memory** via `SaveMemory`.
6. **Log** with the real `script_id`.
7. **Update** the script row with the final content (handler-side
   in the single endpoint, server-side in the batch endpoint).

All three endpoints (`/generate-from-clips` in modalità text-only / clip-aware / auto-search, `/generate-batch`,
e `/api/script-docs/generate` in modalità sync) now go through `WriteScript` for the
generation + normalization + memory-save + log phase, eliminating
the previous hand-rolled `h.memorySvc.CheckGate` and
`h.memorySvc.SaveAfterGeneration` calls scattered across handlers.

### `GenerateFromClipsRequest` text-only / clip-aware mode flags

`/api/script/generate-from-clips` is now opt-in for side effects (text-only mode = `num_clips: 0`; clip-aware mode = `clip_ids` o `num_clips > 0`):

**Mode selection**:

| Param | Default | Description |
|-------|---------|-------------|
| `num_clips` | `0` | Number of clips to auto-search via `mediaCurator.Curate()`. When `> 0`, `clip_ids` is ignored. |
| `clip_ids` | `[]` | Explicit clip IDs (caller-supplied). Takes precedence over `num_clips` when non-empty. |

**Feature flags** (all opt-in, defaults match the handler's `if req.X == ""` fallbacks in `internal/api/handlers/script/handlers/handler_clip_source.go`):

| Flag | Default | Effect when true / non-empty |
|------|---------|------------------------------|
| `extract_entities`  | `false` | Run entity + insight extraction (entities, important words/phrases, special names, artlist phrases, clip suggestions) |
| `artlist_search`    | `false` | Curation pool includes Artlist clips (passed to `mediaCurator.Curate()` when `num_clips > 0`) |
| `stock_search`      | `false` | Curation pool includes stock media |
| `generate_metadata` | `false` | Run YouTube metadata generation (title, description, tags per language) |
| `save_to_db`        | `false` | Persist script + sections to `scripts` table |
| `generate_timeline` | `false` | Build visual timeline (subject/keywords/entities per segment) — the handler does NOT auto-enable this for clip-aware mode; caller must set it explicitly |
| `force_refresh`     | `false` | Bypass exact cache (memory gate) |
| `use_memory`        | `true`* | `*bool` with `omitempty`. Handler treats `nil` as `true` (memory gate enabled); pass `false` to force fresh generation |
| `transcript_policy` | `"auto"` | Handler defaults empty string to `"auto"`. Valid values: `"auto"`, `"full"`, `"evidence_only"`, `"summary_only"` (see `internal/service/scriptcore/clip_source.go:133`) |
| `ordering_strategy` | `"auto"` | Handler defaults empty string to `"auto"`. Valid values: `"auto"`, `"chronological"`, `"thematic"` (see `internal/service/scriptcore/clip_source.go:134`) |

**Google Doc** is **always created** for the unified flow (`flow_doc.go::maybeCreateGoogleDoc` is called unconditionally in `job_handler_clip_source.go`); there is no `create_doc` flag on `GenerateFromClipsRequest`. The previous behaviour always ran entities, insights, metadata, and the Google Doc in parallel, adding 5–15s of latency for callers that just wanted plain text — now everything is opt-in (except the Google Doc, which is hard-wired).

> **Note (June 2026):** The `create_doc` flag exists on the **separate** `GenerateFromCatalogRequest` (`POST /api/script/generate-from-catalog`), not on the unified `GenerateFromClipsRequest`. See `internal/api/handlers/script/handlers/types_catalog.go:22`.

---

## Database Migrations (June 2026)

Two new migrations extend the script schema:

- **029** `migrations/sqlite/029_script_research_sources_unique.sql`
  adds `UNIQUE INDEX idx_script_research_unique
  ON script_research_sources(script_id, url, query)`. The
  `SaveResearchSources` insert now uses `ON CONFLICT DO UPDATE` with
  `MAX(relevance_score)` to dedup across retries, parallel chapters,
  and source splits that resolve to the same URL.
- **030** `migrations/sqlite/030_outline_sections_emotional_role.sql`
  adds `emotional_role TEXT NOT NULL DEFAULT ''` to
  `script_outline_sections` so the per-section `ScriptPlan` is fully
  persisted (purpose, key_points, emotional_role).

---

## Recent Fixes (May 2026)

### Commit `7f46db9` - Batch Index Bug Fix
**Problem**: `parseBatchVisualQueryResponse()` returned map indexed by segment index, but the loop used `i` (loop counter) instead of `seg.Index`.

**Fix**: Changed loop to iterate over `uncachedSegments` and use `seg.Index` directly:
```go
// BEFORE (buggy)
for i, idx := range uncachedIndices {
    if i < len(parsedResults) {
        results[idx] = parsedResults[i]  // Wrong! i != idx
    }
}

// AFTER (fixed)
for _, seg := range uncachedSegments {
    if result, ok := parsedResults[seg.Index]; ok {
        results[seg.Index] = result
    }
}
```

### Commit `50f0d2c` - Always Populate Visual Fields
**Problem**: Visual fields (`visual_subject`, `visual_caption`, `search_suggestions`) were only populated when segments had NO asset matches.

**Fix**: Restructured `BuildTimelinePlan()` to always populate visual fields from `batchResults`:
```go
// Always populate from batch results
if batchResults != nil {
    if r, ok := batchResults[seg.Index]; ok {
        seg.VisualSubject = r.VisualSubject
        seg.VisualCaption = r.VisualCaption
        seg.SearchSuggestions = r.Queries
    }
}
```

---

## Configuration

**File**: `config.yaml`

```yaml
features:
  script_docs_enabled: true

security:
  admin_token: "YOUR_SECURE_TOKEN_HERE"

ollama:
  url: "http://localhost:11434"
  model: "llama3.1:8b"

google_drive:
  credentials_file: "credentials.json"
  root_folder_id: "1OAAf5dawAppdopsgCq1yHFGPUXCI9Vbk"
```

---

## Testing

### Manual Test
```bash
curl -X POST http://127.0.0.1:8080/api/script-docs/generate \
  -H "Content-Type: application/json" \
  -H "X-Velox-Admin-Token: CHANGE_ME_IN_PRODUCTION" \
  -d '{"topic": "Tecnologia nel XXI secolo", "duration": 60, "language": "it"}'
```

### Expected Output
```json
{
  "doc_id": "18nd7KWdFgYwTVwLP1IOFoMlBe8a0ykHMHNG6gbCAzxU",
  "doc_url": "https://docs.google.com/document/d/...",
  "timeline": {
    "segments": [{
      "visual_subject": "Shenzhen tech evolution",
      "visual_caption": "A time-lapse shot of Shenzhen's transformation...",
      "search_suggestions": ["Shenzhen technology growth", "Huawei data centers"]
    }]
  }
}
```

---

## TODO / Known Issues

1. **IMPORTANT PHRASES**: Currently extracts first N sentences. Should use LLM-based extraction.
2. **IMPORTANT WORDS**: English-only stopword filtering. Needs multilingual support.
3. **SPECIAL NAMES**: Disabled (was producing false positives). Needs LLM-based NER.
4. **Cache persistence**: `queryCache` is in-memory only. Should use SQLite or Redis.
5. **Batch prompt optimization**: Currently sends all segments. Should implement smart batching (max N segments per call).
6. **Error recovery**: Some failures (e.g., LLM timeout) could benefit from retry logic with exponential backoff.

---

## File Reference

| Component | File Path |
|-----------|-----------|
| HTTP Handler | `internal/api/handlers/script/handlers/handler_generate.go` |
| Document Builder | `internal/api/handlers/script/script_docs_builder.go` |
| Timeline Logic | `internal/api/handlers/script/timeline_logic.go` |
| Visual Query Generator | `internal/api/handlers/script/artlist_query_generator.go` |
| LLM Generation | `internal/ml/ollama/generate.go` |
| Prompt Builders | `internal/ml/ollama/prompts/prompt_builders.go` |
| Default Values | `internal/ml/ollama/types/defaults.go` |
| Association Service | `internal/service/association/service.go` |
| Timeline Types | `internal/api/handlers/script/timeline_types.go` |
| Script Types | `internal/api/handlers/script/script_docs_types.go` |

---

**Last Updated**: May 7, 2026
**Maintainer**: PipelineGen Team
