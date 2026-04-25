# Script Pipeline Documentation

## Overview

The script pipeline generates documentary scripts from topics using Ollama LLM, extracts entities, and associates video clips.

## Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                        HTTP API Layer                          │
│  /script-pipeline/generate-text → /divide → /extract-entities  │
└─────────────────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                   ScriptDocService (Go)                      │
│  • generateScriptForLangWithRetry() - Ollama LLM calls       │
│  • extractProperNouns() / extractKeywords() / extractEntities │
│  • associateClips() - Clip matching with priority:            │
│     1) Dynamic clips (clipsearch)                          │
│     2) StockDB clips                                      │
│     3) Artlist clips                                     │
└─────────────────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                   Data Sources                              │
│  • Ollama (LLM at localhost:11434)                        │
│  • StockDB (Drive stock clips indexed in SQLite)              │
│  • Artlist DB (Artlist clips indexed in SQLite)             │
│  • clipsearch.Service (Dynamic YouTube search)                │
└─────────────────────────────────────────────────────────────────────────────┘
```

## Pipeline Flow

### 1. Generate Text (Ollama)

**Endpoint:** `POST /script-pipeline/generate-text`

```go
// In service/scriptdocs/generator.go:31
func (s *ScriptDocService) generateScriptForLangWithRetry(
    ctx, topic, duration, langName, maxRetries) (string, error)
```

- Creates prompt based on template (documentary, storytelling, top10, biography)
- Calls Ollama at `http://localhost:11434`
- Retries with exponential backoff (1s, 2s, 4s...)
- Cleans AI preamble phrases

**Templates:**
| Template | Instructions |
|----------|--------------|
| documentary | "Genera testo COMPLETO su {topic}..." |
| storytelling | "Genera testo NARRATIVO..." with hook, conflict, resolution |
| top10 | "Genera testo TOP 10 LISTA..." |
| biography | "Genera testo BIOGRAFICO..." |

### 2. Extract Entities

**In:** `service/scriptdocs/service.go:517-529`

| Entity Type | Extraction Method |
|-----------|----------------|
| frasi_importanti | First 4 sentences with meaningful content |
| nomi_speciali | extractProperNouns() |
| parole_importanti | extractKeywords() |
| entita_con_immagini | extractEntitiesWithImages() |

### 3. Associate Clips

**In:** `service/scriptdocs/associator.go:712`

Priority order:
1. **Dynamic clips** - From clipsearch.SearchClips() using keywords
2. **StockDB clips** - Searched by tags matching concepts
3. **Artlist clips** - Fallback stock footage

**Scoring:**
```go
// associator.go:689
func scoreConceptForPhrase(fraseLower string, cm clipConcept) (int, string)
    // Counts keyword matches, returns best keyword
```

## Code Locations

| Component | File | Key Function |
|-----------|------|--------------|
| Service | `service/scriptdocs/service.go` | GenerateScriptDoc() |
| Generator | `service/scriptdocs/generator.go` | generateScriptForLangWithRetry() |
| Associator | `service/scriptdocs/associator.go` | associateClipsWithDedup() |
| HTTP Handler | `api/handlers/script_pipeline.go` | RegisterRoutes() |

## Key Functions

### GenerateScriptDoc
```go
// service/scriptdocs/service.go:466
func (s *ScriptDocService) GenerateScriptDoc(
    ctx context.Context, 
    req ScriptDocRequest) (*ScriptDocResult, error)

// ScriptDocRequest fields:
// - Topic: string (required)
// - Duration: int (30-180, default 80)
// - Languages: []string (default ["it"])
// - Template: string (documentary, storytelling, top10, biography)
// - BoostKeywords: []string
// - SuppressKeywords: []string
```

### associateClips
```go
// service/scriptdocs/associator.go:712
func (s *ScriptDocService) associateClipsWithDedup(
    frasi []string, 
    usedClipIDs map[string]bool) []ClipAssociation

// Returns for each phrase:
// - Phrase: the sentence
// - ClipID: matched clip ID  
// - ClipURL: link to clip
// - Source: "dynamic" | "stock" | "artlist"
// - Keyword: matched keyword
```

## Python Scripts (Legacy)

In `scripts/` directory:

| Script | Purpose |
|--------|---------|
| `full_entity_script.py` | Full pipeline with Ollama + entity extraction + clip association |
| `script_with_smart_clips.py` | Script generation + smart clip matching |
| `clip_script_matcher.py` | Clip-to-script matching |

### full_entity_script.py Usage
```bash
python3 scripts/full_entity_script.py --topic "Gervonta Davis" --duration 80
python3 scripts/full_entity_script.py --topic "Elvis Presley" --duration 120 --json
```

## Configuration

**Environment Variables:**
- `OLLAMA_ADDR` - Ollama URL (default: `http://localhost:11434`)

**Config file (`config.yaml`):**
```yaml
external:
  ollama_url: "http://localhost:11434"
```

## Testing

### Quick Test Script

```bash
# Test completo pipeline con Gervonta Davis (default)
./scripts/test_pipeline_complete.sh

# Test con topic custom
./scripts/test_pipeline_complete.sh "Elvis Presley" 120

# Test con JSON output
python3 scripts/full_entity_script.py --topic "Gervonta Davis" --duration 80 --json
```

### Manual Testing

```bash
# Test Go pipeline
cd src/go-master
go test ./internal/service/scriptdocs/... -v

# Test Python script
python3 scripts/full_entity_script.py --topic "Test Topic" --duration 30 --json

# Test entity images via API
curl -X POST http://localhost:8080/api/script-pipeline/extract-entities \
  -H "Content-Type: application/json" \
  -d '{"segments": [{"text": "Gervonta Davis is a professional boxer."}]}'
```

## Component Status

| Componente | Status | Note |
|------------|--------|------|
| 1. Generazione testo | ✅ | Ollama → script completo |
| 2. Estrazione entità | ✅ | frasi_importanti, nomi_speciali, parole_importanti |
| 3. Entity images | ✅ | Wikipedia + DuckDuckGo (https://duckduckgo.com/i/...) |
| 4. Clip association | ✅ | Drive clips con match score |
| 5. Smart matching | ✅ | Keyword + tag overlap |
| 6. Documentazione | ✅ | docs/SCRIPT_PIPELINE.md aggiornato |

## Go Modules

| Module | Location | Purpose |
|--------|----------|---------|
| entityimages | `internal/entityimages/finder.go` | Wikipedia + DDG image search for entities |
| ddgimages | `internal/ddgimages/search.go` | DuckDuckGo image API |
| clip/similar | `internal/clip/similar.go` | Smart clip matching by tags/name |
| interview | `internal/interview/analyzer.go` | YouTube interview → VTT → moments |

## Full Example: Gervonta Davis

```bash
python3 scripts/full_entity_script.py --topic "Gervonta Davis" --duration 80 --json
```

**Output:**
```
=== TITLE ===
Gervonta 'Tank' Davis: The Ruthless Force of Boxing

=== FRASI IMPORTANTI ===
1. Gervonta 'Tank' Davis. Il nome evoca potenza...
2. Dopo aver iniziato a fare sparring all'età di 12 anni...
3. Il suo record professionale è impressionante: 29 vittorie...

=== ENTITY IMAGES ===
Gervonta Davis: https://duckduckgo.com/i/5f7feee61ffe03f8.jpg...
Ryan Garcia: https://duckduckgo.com/i/...

=== CLIP ASSOCIATE ===
- clip_00 efa23ec6 (Entity: boxer, Score: 100)
  URL: https://drive.google.com/file/d/...
```