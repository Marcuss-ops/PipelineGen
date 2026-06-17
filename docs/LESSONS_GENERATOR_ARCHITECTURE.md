# Lessons Generator — Architettura del Sistema

> **Data:** 3 Giugno 2026
> **Obiettivo:** Creare un sistema modulare in Go che, dato un source text (libro, articolo, documento), generi lezioni web strutturate con capitoli paralleli + immagini AI opzionali.
> **Tutto in Go** — nessuna dipendenza Python per il core.

---

## 1. Visione Generale

```
┌──────────────────────────────────────────────────────────────────────┐
│                     WEB LESSONS GENERATOR SYSTEM                      │
├──────────────────────────────────────────────────────────────────────┤
│                                                                       │
│  INPUT: SourceText (da libro processato, Google Doc, PDF, testo)     │
│       │                                                               │
│       ▼                                                               │
│  ┌──────────────────────────────────────┐                             │
│  │  1. SPLITTER — Divide in Capitoli    │  ← GO (nessun Python)      │
│  │     • Split strutturale (paragrafi)   │                             │
│  │     • Split per argomento (LLM)       │                             │
│  └──────────────┬───────────────────────┘                             │
│                 │                                                      │
│                 ▼                                                      │
│  ┌──────────────────────────────────────────────────────────────┐    │
│  │  2. GENERATOR POOL — Parallel Map (concurrent.ParallelMap)    │    │
│  │     ┌──────────┐  ┌──────────┐  ┌──────────┐                 │    │
│  │     │Cap. 1    │  │Cap. 2    │  │Cap. 3    │  ...            │    │
│  │     │Ollama    │  │Ollama    │  │Ollama    │                 │    │
│  │     │Chat API  │  │Chat API  │  │Chat API  │                 │    │
│  │     └────┬─────┘  └────┬─────┘  └────┬─────┘                 │    │
│  │          │              │              │                       │    │
│  └──────────┼──────────────┼──────────────┼────────────────────┘    │
│             │              │              │                           │
│             ▼              ▼              ▼                           │
│  ┌──────────────────────────────────────────────────────────────┐    │
│  │  3. IMAGE GENERATION (opzionale, per capitolo o batch)       │    │
│  │     • Modalità "per capitolo": appena il capitolo è pronto    │    │
│  │       → chiama images.Service.GenerateSmartImage()            │    │
│  │     • Modalità "batch": dopo tutti i capitoli → endpoint      │    │
│  │       separato POST /api/lessons/:id/generate-images          │    │
│  │     • Fallback: Google Vids → NVIDIA Flux                     │    │
│  └──────────────┬───────────────────────────────────────────────┘    │
│                 │                                                      │
│                 ▼                                                      │
│  ┌──────────────────────────────────────┐                             │
│  │  4. ASSEMBLER — Costruisce Lezione   │  ← GO                       │
│  │     • Markdown con front matter YAML  │                             │
│  │     • Indice dei capitoli             │                             │
│  │     • Immagini embeddate              │                             │
│  │     • Google Doc upload (opzionale)   │                             │
│  └──────────────┬───────────────────────┘                             │
│                 │                                                      │
│                 ▼                                                      │
│  OUTPUT: Lezione Web Completa                                         │
│  • File .md in data/lessons/{slug}/                                    │
│  • Google Doc su Drive (opzionale)                                     │
│  • Immagini in /assets/ (via images.Service già integrato)             │
│  • Record nel DB (tabella lessons futura)                              │
│                                                                       │
└──────────────────────────────────────────────────────────────────────┘
```

---

## 2. Package e File

```
internal/
├── media/
│   └── lessons/                           # ← NUOVO MODULO
│       ├── service.go                     # Service principale (wiring)
│       ├── splitter.go                    # Split testo in capitoli
│       ├── generator.go                   # Generazione capitoli paralleli
│       ├── assembler.go                   # Assemblaggio lezione finale (.md)
│       ├── types.go                       # Tipi e struct condivisi
│       ├── job_handler.go                 # Job handler per async
│       └── service_test.go                # Test
│
├── api/
│   └── handlers/
│       └── lessons/
│           └── handler.go                # ← NUOVO HANDLER (endpoint HTTP)
│
├── module/
│   └── lessons.go                         # ← NUOVA REGISTRAZIONE MODULO
│
└── app/
    ├── core_deps.go                       # + LessonsService *lessons.Service
    ├── service_manager.go                 # + Creazione LessonsService
    └── registry.go                        # + Registrazione modulo Lessons
```

---

## 3. Modello Dati (types.go)

```go
package lessons

// LessonRequest è l'input per la generazione di una lezione.
type LessonRequest struct {
    SourceText      string   `json:"source_text"`                  // Testo sorgente completo
    Title           string   `json:"title"`                        // Titolo della lezione
    Language        string   `json:"language,omitempty"`           // Lingua (default: "it")
    Tone            string   `json:"tone,omitempty"`               // Tono narrativo (default: "educational")
    Model           string   `json:"model,omitempty"`              // Modello Ollama (default: "gemma4:e4b")
    MaxChapters     int      `json:"max_chapters,omitempty"`       // Max capitoli (0 = auto)
    GenerateImages  bool     `json:"generate_images,omitempty"`    // Genera immagini AI per capitolo
    ImageStyle      string   `json:"image_style,omitempty"`        // Stile immagini (es. "medievale")
    ImageModel      string   `json:"image_model,omitempty"`        // Modello immagini (es. "flux-1-dev")
    ImageWidth      int      `json:"image_width,omitempty"`        // Larghezza immagini
    ImageHeight     int      `json:"image_height,omitempty"`       // Altezza immagini
    OllamaURL       string   `json:"ollama_url,omitempty"`         // URL Ollama override
    Async           bool     `json:"async,omitempty"`              // Modalità asincrona (job)
}

// ChapterResult è il risultato della generazione di un singolo capitolo.
type ChapterResult struct {
    Index     int       `json:"index"`
    Title     string    `json:"title"`
    Content   string    `json:"content"`
    WordCount int       `json:"word_count"`
    Image     *ImageRef `json:"image,omitempty"`
    Error     string    `json:"error,omitempty"`
}

// ImageRef rappresenta un riferimento a un'immagine generata (da models.ImageAsset).
type ImageRef struct {
    Hash        string `json:"hash"`
    PathRel     string `json:"path_rel"`
    URL         string `json:"url"`           // /assets/ + PathRel
    DriveLink   string `json:"drive_link"`
    DriveFileID string `json:"drive_file_id"`
    Prompt      string `json:"prompt"`        // Prompt usato per generarla
}

// LessonResult è il risultato finale della generazione.
type LessonResult struct {
    Success        bool             `json:"success"`
    Title          string           `json:"title"`
    Language       string           `json:"language"`
    Chapters       []ChapterResult  `json:"chapters"`
    TotalWords     int              `json:"total_words"`
    MarkdownPath   string           `json:"markdown_path,omitempty"`
    DriveDocURL    string           `json:"drive_doc_url,omitempty"`
    DriveFolderURL string           `json:"drive_folder_url,omitempty"`
    GeneratedAt    string           `json:"generated_at"`
    Error          string           `json:"error,omitempty"`
}

// ChapterSplit rappresenta un segmento di testo da elaborare.
type ChapterSplit struct {
    Index int
    Title string   // Titolo suggerito per il capitolo
    Text  string   // Testo del capitolo
}
```

---

## 4. Configurazione

```go
// LessonConfig — da aggiungere in internal/config/types.go
type LessonsConfig struct {
    Enabled              bool   `yaml:"enabled" default:"true"`
    DefaultModel         string `yaml:"default_model" default:"gemma4:e4b"`
    OllamaURL            string `yaml:"ollama_url" default:"http://127.0.0.1:11434"`
    DefaultTone          string `yaml:"default_tone" default:"educational"`
    DefaultLanguage      string `yaml:"default_language" default:"it"`
    DefaultImageModel    string `yaml:"default_image_model" default:"flux-1-dev"`
    MaxParallelChapters  int    `yaml:"max_parallel_chapters" default:"5"`
    DriveRootFolder      string `yaml:"drive_root_folder" env:"VELOX_DRIVE_LESSONS_ROOT" default:""`
}

// Aggiungere anche in Config:
// Lessons LessonsConfig `yaml:"lessons"`
```

```yaml
# config.yaml
lessons:
  enabled: true
  default_model: "gemma4:e4b"
  ollama_url: "http://127.0.0.1:11434"
  default_tone: "educational"
  default_language: "it"
  default_image_model: "flux-1-dev"
  max_parallel_chapters: 5
  drive_root_folder: ""

drive:
  lessons_root_folder: ""   # Già esistente? Da aggiungere in DriveConfig
```

**N.B.:** Il config `DriveConfig` ha già `BooksRootFolder`. Seguire lo stesso pattern per aggiungere `LessonsRootFolder` se serve una cartella Drive separata per le lezioni.

---

## 5. Service (service.go)

Il service principale aggrega i sottocomponenti e orchestra l'intero flusso.

```go
package lessons

import (
    "database/sql"
    "velox/go-master/internal/ml/ollama"
    imgservice "velox/go-master/internal/media/images"
    "velox/go-master/internal/upload/drive"
    "go.uber.org/zap"
)

type Service struct {
    generator   *ollama.Generator
    imgService  *imgservice.Service
    docClient   drive.DocClient
    db          *sql.DB
    cfg         *LessonsConfig
    log         *zap.Logger
}

func NewService(cfg *LessonsConfig, gen *ollama.Generator, imgSvc *imgservice.Service, 
                docClient drive.DocClient, db *sql.DB, log *zap.Logger) *Service {
    return &Service{
        cfg:        cfg,
        generator:  gen,
        imgService: imgSvc,
        docClient:  docClient,
        db:         db,
        log:        log,
    }
}
```

### Core Methods

- `func (s *Service) GenerateLesson(ctx, req) (*LessonResult, error)` — flusso completo sincrono
- `func (s *Service) GenerateLessonWithProgress(ctx, req, onProgress) (*LessonResult, error)` — con progress tracking per job async

---

## 6. Splitter (splitter.go)

Due strategie di split, selezionabili in futuro via config:

### 6.1 Split Strutturale (default, sempre usata)
- Divide il testo in base a paragrafi doppi (`\n\n`)
- Raggruppa N paragrafi per capitolo
  - Default: `len(paragraphs) / maxChapters` paragrafi per capitolo
  - Se `maxChapters` è 0, usa formula automatica: `sqrt(len(paragraphs))`
- Assegna titoli basati sulle prime parole significative

```go
func (s *Service) SplitIntoChapters(sourceText string, maxChapters int) []ChapterSplit {
    if len(sourceText) < 8000 {
        return []ChapterSplit{{
            Index: 0,
            Title: extractTitle(sourceText),
            Text:  sourceText,
        }}
    }
    return s.chunkByParagraphs(sourceText, maxChapters)
}
```

### 6.2 Split con LLM (futuro, opzionale)
- Invia il testo a Ollama per identificare i confini naturali dei capitoli
- Prompt dedicato restituisce array di `{title, start_offset, end_offset}`
- Da implementare solo se necessario

---

## 7. Generatore Parallelo (generator.go)

Usa `concurrent.ParallelMap` già presente in `pkg/concurrent/`:

```go
import "velox/go-master/pkg/concurrent"

// GenerateChapters genera tutti i capitoli in parallelo.
// Ogni capitolo viene elaborato in una goroutine separata.
func (s *Service) GenerateChapters(
    ctx context.Context,
    chapters []ChapterSplit,
    req *LessonRequest,
    onProgress func(int, string),  // per job progress
) []ChapterResult {
    
    return concurrent.ParallelMap(chapters, s.cfg.MaxParallelChapters, 
        func(idx int, split ChapterSplit) ChapterResult {
            
            result := ChapterResult{
                Index: idx,
                Title: split.Title,
            }
            
            // 1. Genera capitolo con Ollama Chat API
            genResult, err := s.generator.GenerateScript(ctx, types.TextGenerationRequest{
                Title:      split.Title,
                SourceText: split.Text,
                Language:   req.Language,
                Tone:       req.Tone,
                Model:      req.Model,
            })
            
            if err != nil {
                result.Error = err.Error()
                return result
            }
            
            result.Content = genResult.Script
            result.WordCount = genResult.WordCount
            
            // 2. Se richiesto, genera immagine per questo capitolo
            if req.GenerateImages && s.imgService != nil {
                image, err := s.generateChapterImage(ctx, split.Title, split.Text, req)
                if err == nil {
                    result.Image = image
                } else {
                    s.log.Warn("chapter image generation failed", 
                        zap.Int("chapter", idx), zap.Error(err))
                }
            }
            
            return result
        },
    )
}
```

### Timeout e Robustezza
- Ogni capitolo ha timeout individuale (dal context padre, default 5 minuti per capitolo)
- Se un capitolo fallisce, gli altri continuano (graceful degradation)
- Se `generate_images` fallisce, il capitolo viene comunque incluso — solo senza immagine

---

## 8. Generazione Immagini Post-Capitolo

Due modalità — entrambe opzionali:

### 8.1 Per-capitolo (in linea, `generate_images: true`)
Appena un capitolo è generato, si chiama `images.Service` per l'immagine:

```go
func (s *Service) generateChapterImage(
    ctx context.Context,
    chapterTitle, chapterText string,
    req *LessonRequest,
) (*ImageRef, error) {
    
    // Costruisci prompt significativo per l'immagine
    prompt := fmt.Sprintf(
        "Cinematic educational illustration for a lesson chapter titled '%s'",
        chapterTitle,
    )
    
    // Usa types.SanitizeInput() per il subject (esiste già)
    subject := types.SanitizeInput(chapterTitle)
    
    // GenerateSmartImage con fallback Google Vids → NVIDIA
    asset, err := s.imgService.GenerateSmartImage(
        ctx,
        subject,
        req.Title,
        req.ImageStyle,
        []string{prompt},
        []string{req.Title, chapterTitle},
        req.ImageWidth,
        req.ImageHeight,
        req.ImageModel,
        false, // skipDrive = false (upload sempre)
    )
    
    if err != nil {
        return nil, err
    }
    
    return &ImageRef{
        Hash:        asset.Hash,
        PathRel:     asset.PathRel,
        URL:         "/assets/" + asset.PathRel,
        DriveLink:   fmt.Sprintf("https://drive.google.com/file/d/%s/view", asset.DriveFileID),
        DriveFileID: asset.DriveFileID,
        Prompt:      prompt,
    }, nil
}
```

### 8.2 Batch (separato, dopo la lezione)
Endpoint dedicato per generare immagini per tutti i capitoli **dopo** che la lezione è stata creata:

```
POST /api/lessons/:id/generate-images
```
- Prende una lezione già generata
- Per ogni capitolo senza immagine → genera
- Aggiorna la lezione con le nuove immagini

---

## 9. Assembler (assembler.go)

```go
func (s *Service) Assemble(result *LessonResult) (string, error) {
    var b strings.Builder
    
    // YAML front matter
    b.WriteString("---\n")
    fmt.Fprintf(&b, "title: \"%s\"\n", result.Title)
    fmt.Fprintf(&b, "language: %s\n", result.Language)
    fmt.Fprintf(&b, "generated_at: %s\n", result.GeneratedAt)
    fmt.Fprintf(&b, "chapters: %d\n", len(result.Chapters))
    fmt.Fprintf(&b, "total_words: %d\n", result.TotalWords)
    b.WriteString("---\n\n")
    
    // Indice
    b.WriteString("## Indice\n\n")
    for _, ch := range result.Chapters {
        fmt.Fprintf(&b, "1. [%s](#capitolo-%d)\n", ch.Title, ch.Index+1)
    }
    b.WriteString("\n---\n\n")
    
    // Capitoli
    for _, ch := range result.Chapters {
        fmt.Fprintf(&b, "## Capitolo %d: %s\n\n", ch.Index+1, ch.Title)
        if ch.Image != nil {
            fmt.Fprintf(&b, "![%s](%s)\n\n", ch.Title, ch.Image.URL)
        }
        b.WriteString(ch.Content)
        b.WriteString("\n\n---\n\n")
    }
    
    return b.String(), nil
}
```

---

## 10. API Endpoint

### `POST /api/lessons/generate`

**Request:**
```json
{
    "source_text": "Testo completo del libro da elaborare...",
    "title": "Storia dell'Impero Romano",
    "language": "it",
    "tone": "educational",
    "model": "gemma4:e4b",
    "max_chapters": 5,
    "generate_images": true,
    "image_style": "medievale",
    "image_model": "flux-1-dev",
    "async": false
}
```

**Response (sync):**
```json
{
    "ok": true,
    "title": "Storia dell'Impero Romano",
    "language": "it",
    "chapters": [
        {
            "index": 0,
            "title": "Le Origini di Roma",
            "content": "Roma non fu costruita in un giorno...",
            "word_count": 1240,
            "image": {
                "hash": "abc123...",
                "path_rel": "images/generated/medievale/le-origini-di-roma/abc123.png",
                "url": "/assets/images/generated/medievale/le-origini-di-roma/abc123.png",
                "drive_link": "https://drive.google.com/file/d/.../view",
                "drive_file_id": "...",
                "prompt": "Cinematic educational illustration..."
            }
        }
    ],
    "total_words": 6200,
    "markdown_path": "data/lessons/storia-impero-romano.md",
    "drive_doc_url": "https://docs.google.com/document/d/.../edit",
    "generated_at": "2026-06-03T12:00:00Z"
}
```

### `GET /api/lessons/jobs`
Lista job di generazione (stesso pattern di `/api/books/jobs`).

### `POST /api/lessons/generate` (async)
Con `async: true` — ritorna `job_id`:
```json
{
    "ok": true,
    "async": true,
    "job_id": "lesson_abc123",
    "status_url": "/api/jobs/lesson_abc123/full"
}
```

---

## 11. Job Handler (job_handler.go)

Pattern identico a `internal/media/books/job_handler.go`:

```go
func (s *Service) HandleJob(ctx context.Context, job *models.Job, tools *jobs.JobTools) (map[string]any, error) {
    var req LessonRequest
    json.Unmarshal(job.Payload, &req)
    
    tools.Progress(10, "Splitting into chapters")
    chapters := s.SplitIntoChapters(req.SourceText, req.MaxChapters)
    
    tools.Progress(15, "Generating chapters in parallel")
    result := s.GenerateLessonWithProgress(ctx, &req, func(pct int, msg string) {
        tools.Progress(pct, msg)
    })
    
    tools.Progress(95, "Assembling lesson")
    markdown, _ := s.Assemble(result)
    // salva su file, upload a Drive...
    
    tools.Progress(100, "Lesson generation completed")
    return map[string]any{...}, nil
}
```

---

## 12. Registrazione Modulo

### module/lessons.go
```go
package module

func NewLessonsModule(cfg *config.Config, log *zap.Logger, handler *lessons.Handler) *RouteModule {
    return NewRouteModule(
        "lessons",
        func(cfg *config.Config) bool { return cfg.Lessons.Enabled },
        "/lessons",
        handler,
        log,
    )
}
```

### core_deps.go — Aggiungere:
```go
LessonsService *lessons.Service
```

### service_manager.go — Aggiungere:
```go
lessonsSvc := lessons.NewService(
    &lessons.LessonsConfig{
        Enabled:             cfg.Lessons.Enabled,
        DefaultModel:        cfg.Lessons.DefaultModel,
        OllamaURL:           cfg.Lessons.OllamaURL,
        DefaultTone:         cfg.Lessons.DefaultTone,
        DefaultLanguage:     cfg.Lessons.DefaultLanguage,
        DefaultImageModel:   cfg.Lessons.DefaultImageModel,
        MaxParallelChapters: cfg.Lessons.MaxParallelChapters,
        DriveRootFolder:     cfg.Drive.BooksFolder(), // o LessonsFolder quando aggiunta
    },
    coreDeps.ScriptGen,
    coreDeps.ImageService,
    coreDeps.DocClient,
    dbs.main.DB,
    log,
)
```

### registry.go — Aggiungere tra i moduli:
```go
{"Lessons", func() (module.Module, any, error) {
    if coreDeps.LessonsService == nil {
        return nil, nil, nil
    }
    handler := lessonshandler.NewHandler(coreDeps.LessonsService, coreDeps.JobsService, log)
    mod := module.NewLessonsModule(cfg, log, handler)
    return mod, nil, nil
}, nil},
```

---

## 13. Dipendenze Esistenti Riutilizzate

| Componente | Pacchetto | Uso |
|---|---|---|
| **Ollama Client** | `internal/ml/ollama/client/` | Chat API con retry, fallback, circuit breaker |
| **Generator** | `internal/ml/ollama/generate.go` | `GenerateScript()`, `TranslateText()` |
| **ParallelMap** | `pkg/concurrent/` | Concorrenza capitoli (già usato in `job_handler.go`) |
| **Image Generation** | `internal/media/images/` | `GenerateSmartImage()` (Google Vids → NVIDIA) |
| **Drive Upload** | `internal/upload/drive/` | Google Docs & Drive files |
| **Doc Client** | `drive.DocClient` | `CreateDoc()` per Google Docs |
| **Job System** | `internal/jobs/` | Elaborazione asincrona con progress |
| **Module Registry** | `internal/module/` | `NewRouteModule()` pattern |
| **API Utilities** | `pkg/apiutil/` | `BindJSON`, `OK`, `Error`, `BadRequest`, `InternalError` |
| **Types** | `internal/ml/ollama/types/` | `SanitizeInput()`, `TextGenerationRequest`, `CleanScript()` |

---

## 14. Progress Tracking (per Job Async)

```
[PROGRESS]  5%  Splitting source text into chapters
[PROGRESS] 10%  Starting chapter generation (N chapters, concurrency=M)
[PROGRESS] 15%  Chapter 1/5 completed
[PROGRESS] 25%  Chapter 2/5 completed (image generated)
[PROGRESS] 35%  Chapter 3/5 completed
[PROGRESS] 50%  Chapter 4/5 completed (image generated)
[PROGRESS] 65%  Chapter 5/5 completed
[PROGRESS] 70%  All chapters generated
[PROGRESS] 80%  Assembling lesson markdown
[PROGRESS] 90%  Uploading to Google Drive
[PROGRESS] 100% Lesson generation completed
```

---

## 15. Casi d'Uso

### Caso 1: Lezione semplice (testo < 8000 caratteri)
- Nessun split — un solo capitolo
- Generazione singola con Ollama
- Eventuale immagine

### Caso 2: Lezione da libro (testo lungo)
- Split strutturale in N capitoli
- Generazione parallela (default 5 capitoli simultanei)
- Ogni capitolo finito → trigger immagine (se `generate_images: true`)
- Assemblaggio in Markdown con indice e YAML front matter

### Caso 3: Solo testo, immagini dopo
- `generate_images: false` o omesso
- Lezione generata senza immagini
- Successivamente → `POST /api/lessons/:id/generate-images` per popolare le immagini

### Caso 4: Elaborazione asincrona
- `async: true`
- Job in background con progress tracking
- Polling via `/api/jobs/:id/full`

---

## 16. Roadmap Implementazione

```
Fase 1: Core (types.go, service.go, splitter.go, generator.go)
  - Tipi e service base
  - Split testo in capitoli
  - Generazione parallela con Ollama Chat API
  - Test unitari

Fase 2: Immagini (generator.go + image integration)
  - Integrazione con images.Service
  - Generazione immagine post-capitolo
  - Fallback gestito (logga errore, non blocca)

Fase 3: Assemblaggio (assembler.go)
  - Costruzione file .md con front matter
  - Salvataggio su disco
  - Upload Google Docs opzionale

Fase 4: API + Modulo (handler.go, module/lessons.go)
  - Endpoint REST /api/lessons/generate
  - Job asincrono
  - Wiring in core_deps.go, service_manager.go, registry.go

Fase 5: Database (futuro)
  - Tabella lessons nel DB
  - Storico generazioni
  - Endpoint GET per listare lezioni
```

---

## 17. Note Finali

- **Niente Python**: tutto il codice è in Go, usando `internal/ml/ollama/` direttamente
- **Immagini opzionali e non bloccanti**: se la generazione fallisce, la lezione è comunque valida
- **ParallelMap già pronto**: riutilizza `pkg/concurrent/` (stessa funzione usata in `job_handler.go`)
- **Modello predefinito**: `gemma4:e4b` per testo, `flux-1-dev` per immagini
- **Fallback immagini**: `GenerateSmartImage()` tenta Google Vids → NVIDIA Flux
- **Pattern familiare**: segue esattamente lo stesso schema di `internal/media/books/`
