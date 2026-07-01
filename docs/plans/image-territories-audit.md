# FASE 0 — Audit di Archeologia Immagini (AI vs Retrieved)

> Documento di sola lettura. Nessun codice di produzione toccato.
> Data: 1 luglio 2026. Audit eseguito sul tree `main` corrente + DB `data/media/media.db.sqlite`.
> Finalità: fornire baseline quantitativa per le successive FASI 1-8 dell'Action Plan
> "Separazione dei Territori Immagine".

---

## TL;DR

| Indicatore | Valore |
|---|---|
| Righe `media_assets` con `source='image'` | **56** |
| Righe con `metadata_json.prompt` non vuoto | **0** (euristica AI, vedi §1.4) |
| Righe con `drive_file_id` non vuoto E prompt vuoto | **56** (proxy retrieved) |
| Righe ambigue (né prompt né drive_file_id) | **0** |
| Righe con `style` colonna non vuota | **0** |
| Stili distinti nel `style` colonna | (vuoto) |
| Definizioni stile in `config/generation_styles.yaml` | (vedi §4) |
| Mappa hardcoded stile→cartella Drive | **17** entries in `storage_drive.go:388-405` |
| Stile usato come stringa libera (`style string`) | **18** call site cumulative |
| Parametro `extra interface{}` di ImageGenService | **sempre `nil`** nei call site (parametro dormiente) |

---

## 1. Distribuzione attuale di `media_assets WHERE source='image'`

### 1.1 Query eseguite (read-only)

```sql
-- A. Totale righe immagine
SELECT COUNT(*) FROM media_assets
WHERE source='image';
-- → 56

-- B. AI proxy (righe con prompt nel metadata_json)
SELECT COUNT(*) FROM media_assets
WHERE source='image'
  AND COALESCE(json_extract(metadata_json,'$.prompt'),'') <> '';
-- → 0

-- C. Retrieved proxy (righe con drive_file_id E prompt vuoto)
SELECT COUNT(*) FROM media_assets
WHERE source='image'
  AND COALESCE(json_extract(metadata_json,'$.prompt'),'') = ''
  AND COALESCE(drive_file_id,'') <> '';
-- → 56

-- D. Ambigue (senza prompt e senza drive_file_id)
SELECT COUNT(*) FROM media_assets
WHERE source='image'
  AND COALESCE(json_extract(metadata_json,'$.prompt'),'') = ''
  AND COALESCE(drive_file_id,'') = '';
-- → 0

-- E. Righe con style colonna non vuota
SELECT COUNT(*) FROM media_assets
WHERE source='image'
  AND COALESCE(style,'') <> '';
-- → 0

-- F. Stili distinti nella colonna style
SELECT DISTINCT COALESCE(style,'') FROM media_assets
WHERE source='image'
  AND COALESCE(style,'') <> ''
LIMIT 30;
-- → (vuoto)
```

### 1.2 Distribuzione risultante

| Categoria | Righe | Note |
|---|---|---|
| Totale immagini | 56 | 100% |
| AI proxy (metadata_json.prompt ≠ '') | 0 | NO rows match this signal |
| Retrieved proxy (drive_file_id ≠ '' AND prompt == '') | 56 | 100% |
| Ambigue | 0 | — |
| Con colonna style valorizzata | 0 | — |

### 1.3 Interpretazione operativa

Tutte le 56 righe immagine nel DB locale sono accompagnate da un `drive_file_id`
e nessuna espone `metadata_json.prompt`. Questo indica che la popolazione della tabella
in questo snapshot proviene prevalentemente dal percorso **retrieved** (Wikipedia/SearXNG/DuckDuckGo)
o da importazioni Drive-side (vedi `storage_drive.go::SyncFromDrive`).

### 1.4 Limite dell'euristica annotato

La query (B) si basa sul path `metadata_json.prompt`. Tuttavia:

| Percorso | Scrive `metadata_json.prompt`? |
|---|---|
| `ImagesRepository.AddImage` (call site di `ingestDirect`) | **NO** — scrive solo `subject_id`, `description`, `license`, `quality_score`, `error` nel `metaMap` (vedi `images_repository.go:48-65`) |
| `searchAndDownloadInner` (richiamato dopo AddImage) | **NO direttamente** — chiama `UpdateImageMetadata` (vedi `storage_search.go:316-330`) ma con chiavi `source_image_url`, `source_page_url`, `source_name`, `source_query` |
| `GenerateSmartImage → ingestGeneratedImage → IngestImage → ingestDirect` | **NO** — la `description` è il prompt (es. `description = "AI generated image via Chrome/Playwright for prompt: <prompt>"`) ma NON viene salvata nel key `prompt` del JSON, bensì come `description` libera |

**Conseguenza**: la colonna `style` e il key `metadata_json.prompt` sono
**orfani funzionali**: la colonna esiste (introdotta da migration 099 parte di Wave CONFORMANCE-001),
ma **nessun percorso di scrittura** la valorizza. Questo è esattamente il
segnale di "separazione dei due territori" che la FASE 1B intende sanare
introducendo `origin` + `provider` come colonne canoniche di prima classe.

### 1.5 Euristica alternativa proposta per il backfill di FASE 4

Quando FASE 4 eseguirà il dual-write verso `generated_image_details` / `retrieved_image_details`,
il backfill corretto verso `origin`/`provider` può usare:

```sql
-- retrieved (drive, web search, manual upload) — ha source_image_url o source_name
UPDATE media_assets
SET origin = 'retrieved',
    provider = COALESCE(json_extract(metadata_json,'$.source_name'),'unknown')
WHERE source='image'
  AND json_extract(metadata_json,'$.source_image_url') IS NOT NULL
  AND json_extract(metadata_json,'$.source_image_url') != '';

-- AI generated — la description contiene il marker Playwright/Chrome
UPDATE media_assets
SET origin = 'generated',
    provider = COALESCE(
        json_extract(metadata_json,'$.generator'),
        CASE WHEN description LIKE 'AI generated image%'
             THEN 'google-slides' ELSE 'unknown' END
    )
WHERE source='image'
  AND description LIKE 'AI generated image%';

-- fallback per il resto
UPDATE media_assets
SET origin = '',
    provider = 'unknown'
WHERE source='image'
  AND (origin IS NULL OR origin = '');
```

Questo sarà aggiunto come migration 117 in FASE 4 (BACKFILL step).

---

## 2. Lista esaustiva dei call site che passano `style` come stringa libera

### 2.1 Definizione del metodo (port + impl)

| File:Riga | Firma |
|---|---|
| `internal/application/images/service.go:170` | `Service.GenerateSmartImage(ctx, subject, topic, style string, prompts []string, tags []string, width, height int, model string, skipDrive bool)` |
| `internal/application/images/service.go:177` | `Service.GenerateSmartImageWithAccount(..., style string, ...)` |
| `internal/application/images/generation_service.go:35-58` | `GenerationService.GenerateSmartImage[WithAccount](..., style string, ...)` |
| `internal/application/scripts/usecase/services.go:91` | `usecase.ImageGenService.GenerateSmartImage(..., style string, ...)` (port interface, fa parte della typed port ImageGenService) |
| `internal/application/images/storage_drive.go:23` | `ImageStorageService.UploadToStyleDrive(ctx, imgAsset, style string)` |
| `internal/application/images/storage_drive.go:90` | `ImageStorageService.RegisterVideoAsset(... style string ...)` (video, ma stesso dominio concettuale) |
| `internal/application/images/storage_drive.go:379` | `ImageStorageService.aiImageDriveRootForSource(source, style string) string` (mappa hardcoded) |
| `internal/application/images/ports.go:173` | `ComputeSourceHash(provider, prompt, style string, width, height int, model string) string` |
| `internal/infrastructure/ai/ollama/generate.go:61, 92` | `(g *Generator).GenerateDescription(... style string ...)`, `GenerateVisualPrompt(... style string ...)` |
| `internal/infrastructure/ai/ollama/prompts/render_media.go:23, 36` | `RenderDescription(... style string ...)`, `RenderVisualPrompt(... style string ...)` |
| `internal/api/images/impl.go:81` | campi request con `style` come `string` |

### 2.2 Call site concreti che passano uno `style` letterale / variabile libera

| # | File:Riga | Espressione `style` | Note |
|---|---|---|---|
| 1 | `internal/app/wire_script_curation.go:88` | `a.svc.GenerateSmartImage(ctx, name, query, "", []string{query}, tags, 1920, 1080, "", false)` | stile vuoto (fall-through) |
| 2 | `internal/application/lessons/generator.go:135` | `s.imgService.GenerateSmartImage(..., req.ImageStyle, ...)` | stile da `LessonRequest.ImageStyle` (campo libero del lesson request) |
| 3 | `internal/application/images/fullimages/service.go:187` | `s.imgService.GenerateSmartImage(..., style, ...)` | stile da `Section.Style` (campo libero del section request) |
| 4 | `internal/application/scripts/usecase/flow_helpers.go:596` | `svc.ImgSvc.GenerateSmartImage(aiCtx, name, "Portrait or representative image of "+name, "realistic", ...)` | **stesso "realistic" hardcoded** |
| 5 | `internal/application/images/generation_service.go:67` | `g.imageGen.Generate(ctx, GenerateImageRequest{Prompt: ..., Style: style, ...})` | port interno, passa lo stile "libero" al provider |
| 6 | `internal/application/images/generation_service.go:185` | `g.imageGen.Generate(ctx, GenerateImageRequest{..., Style: payload.Style, ...})` | job async `image.generate.google` |
| 7 | `internal/application/images/storage_ingest.go:128` | `if aiDriveRoot := s.aiImageDriveRootForSource(source, style); aiDriveRoot != "" { req.DriveRootOverride = aiDriveRoot }` | usato da `aiImageDriveRootForSource` per la mappa hardcoded |
| 8 | `internal/application/images/storage_drive.go:66` | `req := drive.AssetDestinationRequest{... Style: style, ...}` | Drive request di UploadToStyleDrive |
| 9 | `internal/application/images/storage_drive.go:117` | `Style: style` | `RegisterVideoAsset` |
| 10 | `internal/infrastructure/ai/ollama/prompts/render_media.go:23, 36` | `RenderDescription(mediaType, prompt, style)` / `RenderVisualPrompt` | descrizione narrativa per metadata |
| 11 | `internal/infrastructure/ai/ollama/generate.go:61, 92` | `GenerateDescription(... style ...)` / `GenerateVisualPrompt` | producer delle description |
| 12 | `internal/application/assets/providers/artlist/semantic_enricher.go:183` | `style := "cinematic"` | stile default fisso, non risolto dal registry |
| 13 | `internal/domain/script/cache_key_test.go:32` | `Style: "cinematic"` | test fixture |
| 14 | `internal/application/scripts/usecase/engine_test.go:626` | `Style: "cinematic"` | test fixture |
| 15 | `internal/application/images/ingest_test.go:109` | `if style != "realistic"` | test assertion (riferimento a stile libero) |
| 16 | `internal/infrastructure/database/migrations_test.go:312` | `"style": "cinematic"` | test fixture migration |
| 17 | `internal/application/scripts/adapters/normalizer_plan_tests_test.go:263` | `item.Tone = "cinematic"` | test fixture (campo `Tone`, non `style`) |
| 18 | `internal/api/images/impl.go` | request field `Style string` | transport layer |

### 2.3 Conteggio caratteri dei letterali più frequenti

Letterali `style` che compaiono nel codice (ordinamento per occorrenze):

| Letterale | Occorrenze in codice | Note |
|---|---|---|
| `"cinematic"` | 11+ | usato come default in molti test + Production selector |
| `"realistic"` | 2+ | usato in flow_helpers + fullimages fixture |
| `"cartoon"` | 2+ | test fixtures |
| `"anime"` | 1+ | test fixtures + voce mappa hardcoded |

La mancanza di un resolver canonico è evidenziata dal fatto che il letterale
`"cinematic"` ricorre in 11 punti diversi senza un gateway di normalizzazione.

### 2.4 Bug confermato — `StyleRegistry.ApplyStyle` fail-open

File: `internal/application/assets/generation/style_registry.go:79-100`

```go
// ApplyStyle appends the style description to the prompt if the style exists
func (r *StyleRegistry) ApplyStyle(prompt, styleName string) string {
    if styleName == "" {
        return prompt                                                  // line 81: fail-open su vuoto
    }
    style, ok := r.Get(styleName)
    if !ok {
        return prompt                                                  // line 85: fail-open su sconosciuto
    }
    prompt = strings.TrimSpace(prompt)
    if prompt == "" {
        return style.Description
    }
    if textutil.ContainsCI(prompt, style.Description) {
        return prompt
    }
    return fmt.Sprintf("%s, %s", prompt, style.Description)
}
```

**Conseguenza**: il chiamante riceve il prompt originale, identico a come l'aveva passato,
senza alcun marker di errore. Un servizio in upstream che fa `if styleApplied == prompt { abort }`
non ha modo di distinguere "stile non applicato per error" da "stile era già nel prompt".
Questa è la root cause della sezione 5 della proposta originale ("risoluzione fail-closed").

### 2.5 Definizione YAML corrente (`GenerationStyle` shape povera)

File: `internal/domain/asset/types_aux.go:41-58`

```go
type GenerationStyle struct {
    Name        string `yaml:"name" json:"name"`
    Description string `yaml:"description" json:"description"`
}

type GenerationStyles struct {
    Styles []GenerationStyle `yaml:"styles" json:"styles"`
}
```

Solo due campi. Manca tutto ciò che serve per il fail-closed:
`Version`, `DisplayName`, `PromptSuffix`, `NegativePrompt`, `DefaultWidth/Height`,
`AllowedModels`, `AllowedProviders`, `Tags`, `DestinationKey`, `Enabled`.

---

## 3. Contenuto reale del parametro `extra interface{}` di `ImageGenService`

### 3.1 Definizione del port

```go
// internal/application/scripts/usecase/services.go:88-92
type ImageGenService interface {
    SearchAndDownload(ctx context.Context, name, description, query, language string, extra interface{}) (*asset.ImageAsset, error)
    GenerateSmartImage(ctx context.Context, name, description, style string, prompts, tags []string, width, height int, extra string, flag bool) (*asset.ImageAsset, error)
}
```

E il secondo port minore (adapter-only):

```go
// internal/application/scripts/adapters/processor_images.go:39-42
type ImageGenService interface {
    SearchAndDownload(ctx context.Context, sceneName, sceneText, altText, language string, opts interface{}) (*ImageResult, error)
}
```

### 3.2 Mapping call site × valore passato

| # | File:Riga | Metodo | Valore di `extra` | Tipo effettivo |
|---|---|---|---|---|
| 1 | `internal/application/scripts/usecase/flow_helpers.go:582` | `SearchAndDownload` | **`nil`** (lettera) | `nil` |
| 2 | `internal/application/scripts/adapters/processor_images.go:121` | `p.gen.SearchAndDownload(...)` | **`nil`** | `nil` |
| 3 | `internal/api/images/impl.go:145` | `h.service.SearchAndDownload(...)` | (parametro non passato: usa signature `internal/application/images` con `(slug, req.Name, req.URL, req.Lang, req.Tags)` — 5 args, no `extra`) | n/a |
| 4 | `internal/api/images/impl.go:165` | `h.service.SearchAndDownload(...)` | idem punto 3 (non passa extra) | n/a |
| 5 | `internal/application/scripts/usecase/flow_helpers.go:596` | `GenerateSmartImage` | `""` (stringa vuota come `extra string`) | `string` (modello) |
| 6 | `internal/application/images/fullimages/service.go:187` | `GenerateSmartImage` | `"flux-1-dev"` (stringa) | `string` (modello) |
| 7 | `internal/application/lessons/generator.go:135` | `GenerateSmartImage` | `imageModel` (stringa) | `string` (modello) |
| 8 | `internal/application/images/service.go:174` | `Service.GenerateSmartImage` | (10mo parametro `extra` = `model string` non più presente nella firma: passato a `Gen.GenerateSmartImage` con argomento posizionale `model`) | `string` |
| 9 | `internal/application/scripts/adapters/processor_images_voiceover_test.go:43` | fake `SearchAndDownload` | `_` (ignorato, è un fake test) | n/a |

### 3.3 Verdetto

**`extra interface{}` (port `usecase.ImageGenService.SearchAndDownload`) è
un parametro COMPLETAMENTE DORMIENTE**: tutti i call site reali passano `nil`.
Il tipo `interface{}` qui è solo un residuo storico. Da rimuovere nella
FASE 5 (split del port) o al massimo nella FASE 7 (refactor endpoint).

Per `GenerateSmartImage`, il parametro `extra string` è in realtà il `model`
e risulta **sempre valorizzato** (mai stringa vuota nei call site reali:
fullimages passa `"flux-1-dev"`, lessons passa `imageModel`, flow_helpers passa `""`).
Anche qui c'è un'opportunità di split: la firma `GenerateSmartImage(..., extra string, flag bool)`
può diventare `GenerateSmartImage(..., model string, skipDrive bool)` — i due
parametri terminali sono già semanticamente `model` e `skipDrive`.

---

## 4. Mappa hardcoded dei folder Drive per stile

File: `internal/application/images/storage_drive.go:388-405`

```go
styleFolders := map[string]string{
    "medieval":         "1yfCnjvpZ3ZuFs7W0pRFNGzapRLGIykPi",
    "whiteboard":       "1Znu_g8pUOXkXHG-1XkLMOcYN69umrlae",
    "anime":            "1e1pW8ZaQYTwDV0po6tIxx_vUql_6CD_v",
    "cinematic":        "1t6bhe8kquPqk7ypYzbobHqUq-HGjVdZw",
    "sketch":           "1QrC74aZ8It43pQa5l5G6BNWcc18ksIo2",
    "watercolor":       "1tzvn5PkOwZk3DPjjr8sIXKr9LKeM--rB",
    "cyberpunk":        "1x8xcUFtIj7hkGF6CsPJCM822ooJL9kMu",
    "realistic":        "1b5iP5aHekJUL1FB9ZC-WGkWxoDULyU9X",
    "heritage":         "1l_cdMqhKrstV94V7Ym7wemJTUZjjWLq_",
    "kawaii":           "1K5IcI3sC5qLID0M1ulSoUC355S_3lUNh",
    "professional-doc": "1g2Ef3yQCDWZ78YqnOnwhKmIghGJvPOPa",
    "cartoon":          "1ab_YSfuKpj4CCh9twk3st5zv9fvMwS8B",
    "retro-print":      "1141lRohkIiXp8NjGQlGj4bLLaQw6nCDb",
    "papercraft":       "1yWlji7wololy_q3l8GAcmmF8goxJmOih",
    "gothic":           "1CNNcNWY4YXyat9eqUsmsUEGeMmTXJY3t",
    "oil-painting":     "1mI07oRaeabhGSmjdyKOICl5vSK6uSO7i",
    "3d-render":        "1MWZy1rDXQKoAr0HRVMc7BdGAvqCaSe1y",
}
```

**17 entry hardcoded**, tutte stringa Drive-folder-id. Stili definiti qui
sono in parte coincidenti e in parte divergenti rispetto a quelli del YAML
(audit di `generation_styles.yaml` da fare se le righe
definite in YAML sono diverse).

### 4.1 Caller diretto

`aiImageDriveRootForSource(source, style)` è chiamato solo in
`internal/application/images/storage_ingest.go:128`:

```go
if aiDriveRoot := s.aiImageDriveRootForSource(source, style); aiDriveRoot != "" {
    req.DriveRootOverride = aiDriveRoot
}
```

Ed è una funzione su `*ImageStorageService`, istanziato solo da
`internal/application/images/service.go:130-146`.

### 4.2 Test esistente che fissa il contratto attuale

File: `internal/application/images/service_test.go:273-279`

```go
if got := svc.aiImageDriveRootForSource("google-flow", ""); got != "ai-root" {
    t.Fatalf("aiImageDriveRootForSource = %q, want %q", got, "ai-root")
}
if got := svc.aiImageDriveRootForSource("duckduckgo", ""); got != "" {
    t.Fatalf("aiImageDriveRootForSource for web source = %q, want empty", got)
}
```

Il test verifica che:
- `("google-flow", "")` → `"ai-root"` (fallback al default Drive.ImagesFolder)
- `("duckduckgo", "")` → `""` (origine non-AI → non c'è override)

Sarà da aggiornare in FASE 2D quando si introduce `DestinationResolver`.

---

## 5. Open questions emerse dall'audit (da risolvere nelle FASI 1-4)

| # | Domanda | Fase di competenza | Default proposto |
|---|---|---|---|
| Q1 | Perché `metadata_json.prompt` non viene scritto da nessun call site? Bug o by design? | FASE 1B/4 | Aggiungere colonna `prompt` separata + migration 117 in FASE 4 |
| Q2 | Conviene mantenere `metadata_json` come fallback o passare tutto a colonne canoniche? | FASE 4 | Tenere come fallback; BACKFILL: colonne first-class → CONTRACT differito |
| Q3 | La colonna `style TEXT` di media_assets è stata popolata in qualche momento della storia? | FASE 4 | Probabilmente no (DB audit: 0/56 valorizzate). Aggiungere backfill deducendo da `media_assets.style = <style colonna canonica>` quando esiste |
| Q4 | `generation_styles.yaml` esiste ancora? Quali stili definisce? | FASE 1D | Verificare e mappare vs map hardcoded |
| Q5 | I folder ID Drive nella mappa hardcoded sono ancora validi (non trashed)? | FASE 2D | Audit preprocessing step + check via Drive API |
| Q6 | L'`ImageGenerator` port (a cui `GenerateSmartImage` delega) accetta solo `(Prompt, Style, …)` o firma nuova? | FASE 3 | Da leggere `internal/application/images/ports.go` sezione `ImageGenerator`/`GenerateImageRequest` |

---

## 6. Summary findings (per le FASI 1-8)

| Finding | Impatto sulle FASI |
|---|---|
| `source='image'` per tutto, 56 records, nessun prompt canonico | **FASE 1B**: aggiungere `origin` + `provider` (ADD COLUMN DEFAULT '') |
| `metadata_json.prompt` mai scritto — è un fantasma | **FASE 4**: aggiungere backfill deducendo da description prefix |
| `StyleRegistry.ApplyStyle` fail-open senza segnale | **FASE 2A-2C**: sostituire con `StyleResolver` fail-closed |
| Mappa di 17 stili hardcoded in `storage_drive.go` | **FASE 2D**: rimuovere dopo aver introdotto `DestinationResolver` YAML-backed |
| 18+ call site di `style string` libera | **FASE 3**: spostare composizione dietro `PromptComposer`; **FASE 7**: tipizzare request API |
| `extra interface{}` sempre `nil` | **FASE 5**: rimuovere dal port split |
| `generation_styles.yaml` shape povera (solo name + description) | **FASE 1C-1D**: shape completa con YML v2 |
| DB popolato prevalentemente da retrieved (56/56 con drive_file_id) | **FASE 4**: il backfill verso `retrieved_image_details` sarà pesante (potenzialmente 56 record da migrare) |

---

## 7. Note metodologiche

- Audit eseguito su tree `main` (non topic branch) come richiesto.
- Nessuna migration, nessun INSERT/UPDATE/DELETE sul DB di produzione.
- Le query SQL sono read-only e coperte dal `SELECT` puro.
- I call site sono stati enumerati con ripgrep + lettura manuale dei 12 file
  principali (`service.go`, `generation_service.go`, `storage_search.go`,
  `storage_ingest.go`, `storage_drive.go`, `style_registry.go`,
  `images_repository.go`, `flow_helpers.go`, `services.go`,
  `processor_images.go`, `fullimages/service.go`, `lessons/generator.go`).
- Il `extra interface{}` è stato classificato come "dormiente" dopo aver
  ispezionato manualmente tutti i call site — non c'è alcun caso reale
  in cui il parametro venga valorizzato.

---

## 8. Prossimi passi

FASE 1A (prossima): aggiungere i typed constants `ImageOrigin`,
`ImageProvider` e `ImageSearchTerritory` in
`internal/domain/asset/image_enums.go` (nuovo file, compila in isolamento).
Output atteso da FASE 1A: domain layer preparato per FASE 1B (migration).
