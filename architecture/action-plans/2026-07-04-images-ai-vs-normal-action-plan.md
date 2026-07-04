# Images AI vs Normal — Refactor & Stabilization Wave

> **Data**: 2026-07-04
> **Wave-tracker**: `architecture/current.yaml#PR-IMAGES-AI-VS-NORMAL-PLAN`
> **Owner**: `architecture` (waves-domain)
> **Lint gate**: `scripts/ci-architectural-checks.sh` (Check 44 file-size cap, Check 51 raw-string `.Enqueue(`, ecc.)
> **Pattern reference**: AGENTS.md §1 (Fragilità Architetturale), §2 (Complessità), §3 (Prestazioni), §4 (Dead Code), Pattern 0 (Port abstraction) + godlike/06 (one-owner-per-fact) + godlike/07 (typed-error / no-fake-availability)
> **Direct-to-main**: ogni sotto-azione atterra sul proprio SHA canonico su `origin/main` (AGENTS.md Git-Lesson-2). NO topic branch. NO PR. NO `--force` (race-handling via Git-Lesson-4/5).

---

## TL;DR

| Indicatore (90g) | Valore |
|---|---|
| File totali `internal/application/images/**` + `internal/api/images/**` | **78** (>30 → architecture review obbligatoria, AGENTS.md Pattern 5) |
| `internal/application/images/service.go` commit/90g | **46** (top hotspot 🔥) |
| `internal/api/images/impl.go` commit/90g | **27** (🔥 trasporto thin-rich) |
| `internal/infrastructure/database/sqlite/assets/clips_repository.go` commit/90g | **21** (🔥 repository centralizzato) |
| Atomic `style string` call site | **18+** (Primitive Obsession, audit `docs/plans/image-territories-audit.md` §2) |
| Magic literal `"cinematic"` ripetuto | **11+** |
| `extra interface{}` (zombie param) call site production | **6/6 passa `nil`** |
| Hardcoded `style → drive_folder_id` map | **17** entries in `storage_drive.go:388-405` |
| `scanImageAsset`/`scanImageAssetRows` (duplicati byte-equivalent) | **2** |
| Search helpers HTTP inline-duplicati | **5+** |

Azioni totali: **10**, organizzate in **3 bande** per parallelismo (`A` tipizzazione land, `B` DRY + perf, `C` cleanup YAGNI).

---

## Hotspot matrix (Frequenza × Complessità)

| File | Commit / 90g | Complessità | Quadrante |
|---|---|---|---|
| `internal/application/images/service.go` | **46** | 25+ LoC × 25 parametri | 🔥 **ASSOLUTO** |
| `internal/api/images/impl.go` | **27** | 12 endpoint × 4 typed-ports | 🔥 alto |
| `internal/infrastructure/database/sqlite/assets/clips_repository.go` | **21** | repository dual-write + ListImages | 🔥 medio |
| `internal/application/images/google_vids_assets.go` | **14** | media | ⚠️ |
| `internal/infrastructure/database/sqlite/assets/images_repository.go` | **12** | dual-write + ListImages | ⚠️ |
| `internal/domain/asset/types_media.go` | **12** | 50+ type def cross-cutting | ⚠️ |
| `internal/application/images/google_generate.go` | **12** | Chrome entry | ⚠️ |
| `internal/application/images/ingest_semantic.go` | **10** | `pythonEmbeddingAdapter` zombie | ⚠️ |

Frequenza calcolata con `git log --since='90.days' --pretty=format: --name-only` (commit frequency axis). Complessità = stima statica (line-count × cyclomatic × branching density).

---

## Banda A — Tipizzazione del dominio `style` (azioni 1-3)

> Banda critica. La Primitive Obsession su `style string` è root-cause di YAGNI, magic-string dispersion, fail-open behavior e mappe hardcoded. Banda A atterra PRIMA delle altre per sbloccare il refactor banda B/C.

### A1 — Tipizzazione `StyleID` + value-object `StyleDefinition` (🔥 #1)

**Goal**: introdurre `type StyleID string` con `Valid()`, `type StyleVersion int` con `Valid()`, value-object `StyleDefinition{ID StyleID, Version StyleVersion, Name, DisplayName, PromptSuffix, NegativePrompt, DestinationKey, Enabled}`. Risolve Primitive Obsession, abilita fail-closed resolution.

**Wave-tracker**: forward-pointer `PR-IMAGES-AI-VS-NORMAL-PLAN#A1`

**Files**:
- NEW: `internal/domain/asset/types_style.go` (~80 LoC)
- MODIFY: `internal/domain/asset/types_aux.go` (deprecate `GenerationStyle` vecchia shape, replacement alias per 1 wave)
- MODIFY: `internal/application/assets/generation/style_registry.go` (typed accessor; deprecation alias)
- NEW: `internal/domain/asset/types_style_test.go` (~150 LoC)

**Diff direction**:
```go
// NEW in types_style.go
package asset

import "fmt"

type StyleID string
func (s StyleID) Valid() bool { /* non-empty + lowercase + dash-separated */ }
type StyleVersion int
func (v StyleVersion) Valid() bool { return v > 0 }

type StyleDefinition struct {
    ID             StyleID
    Version        StyleVersion
    Name           string
    DisplayName    string
    PromptSuffix   string
    NegativePrompt string
    DestinationKey string
    Enabled        bool
}

// back-compat alias (1-wave horizon)
type GenerationStyle = StyleDefinition
```

**Tests (TDD, ~12 test)**:
1. `TestStyleID_Valid_*` — happy + 5 failure shape (empty / uppercase / underscore-separated / leading-digit / ASCII-only)
2. `TestStyleVersion_Valid_*` — happy + 3 failure (zero / negative / overflow)
3. `TestStyleDefinition_YAML_RoundTrip` — marshal/unmarshal preserva tutti gli 8 campi
4. `TestStyleDefinition_DisplayName_DefaultFromID` — empty DisplayName fallback deterministico
5. `TestGenerationStyle_BackCompatAlias_SameUnderlyingType` — `type GenerationStyle = StyleDefinition` semantic preservation

**Exit-gate**:
- `gofmt -l internal/domain/asset/types_style*.go` → vuoto
- `go test ./internal/domain/asset/ -run "Style" -count=1` → green
- `git grep "type GenerationStyle " internal/` → 1 sola riga (alias)
- Zero new magic literal `"cinematic"` aggiunti da questo commit

**Deadline**: 2026-08-04 (1 wave)
**Owner**: `bg-port` (architecture sub-team)

---

### A2 — `StyleResolver` fail-closed + typed sentinel `ErrUnknownStyle` (🔥 #5)

**Goal**: sostituire `StyleRegistry.ApplyStyle` fail-open (ritorna `(prompt, nil)` su stile sconosciuto = invisible bug) con `(prompt, ErrUnknownStyle)` typed-error. Compositori upstream possono discriminare via `errors.Is`. Sblocca la `style.StyleID` → composizione prompt deterministica con segnale di errore.

**Wave-tracker**: forward-pointer `PR-IMAGES-AI-VS-NORMAL-PLAN#A2`

**Files**:
- MODIFY: `internal/application/assets/generation/style_registry.go` (sostituire body di `ApplyStyle`)
- NEW: `pkg/styleerrors/errors.go` (~20 LoC) — sentinels `ErrUnknownStyle`, `ErrStyleDisabled`, `ErrEmptyPrompt`
- MODIFY: 8 call site che usano il prompt di ritorno senza check: `style_apply_user_log.go` (se esiste), `lessons/generator.go:135`, `fullimages/service.go:187`, `flow_helpers.go:596`, `usecase/engine.go`, `wire_script_curation.go:88`
- NEW tests in `style_registry_test.go` (~80 LoC)

**Diff direction**:
```go
// MODIFY ApplyStyle: ritorno ora (*StyleComposedPrompt, error)
func (r *StyleRegistry) ApplyStyle(prompt string, id StyleID, v StyleVersion) (*StyleComposedPrompt, error) {
    if prompt == "" {
        return nil, fmt.Errorf("%w: prompt is empty", styleerrors.ErrEmptyPrompt)
    }
    def, ok := r.catalog[id]
    if !ok {
        return nil, fmt.Errorf("%w: %q (registry has %d entries)", styleerrors.ErrUnknownStyle, id, len(r.catalog))
    }
    if v != def.Version {
        return nil, fmt.Errorf("%w: requested v%d, catalog has v%d", styleerrors.ErrStyleVersionMismatch, v, def.Version)
    }
    if !def.Enabled {
        return nil, fmt.Errorf("%w: %q", styleerrors.ErrStyleDisabled, id)
    }
    composed := &StyleComposedPrompt{
        BasePrompt:   prompt,
        StyleID:      id,
        StyleVersion: v,
        FinalPrompt:  compose(prompt, def.PromptSuffix),
        NegativePrompt: def.NegativePrompt,
    }
    return composed, nil
}
```

**Tests (TDD, ~7 test)**:
1. `TestApplyStyle_Happy_*` — (cinematic, v1) sul prompt base
2. `TestApplyStyle_EmptyPrompt_ReturnsErrEmptyPrompt` — typed error
3. `TestApplyStyle_UnknownID_ReturnsErrUnknownStyle` — typed error + registry-size nel messaggio
4. `TestApplyStyle_MismatchVersion_ReturnsErrStyleVersionMismatch` — typed error
5. `TestApplyStyle_DisabledID_ReturnsErrStyleDisabled` — typed error
6. `TestApplyStyle_AlreadyContainsSuffix_IsIdempotent` — no duplicazione
7. `TestApplyStyle_PromptSuffixCaseInsensitive_Folded` — NFKC fold

**Exit-gate**:
- Tutti gli 8 call site production aggiornati con `if err := styleReg.ApplyStyle(...); err != nil { ... }` (NO silent fall-through)
- `go test ./internal/application/assets/generation/ -count=1` → green
- `bash scripts/ci-architectural-checks.sh` → zero nuove violazioni
- `rg "\.\.ApplyStyle\(" internal/ --glob '!**/*_test.go'` (production-only) → tutte le chiamate hanno secondo return checked

**Deadline**: 2026-08-11 (1 wave)
**Owner**: `bg-port` + `composition-root` (cross-team)

---

### A3 — `GenerationStyle` shape v2 (audit §2.5 forward-pointer)

**Goal**: completare la shape YML v2 (audit `docs/plans/image-territories-audit.md` §2.5): aggiungere `Version`, `DisplayName`, `PromptSuffix`, `NegativePrompt`, `DestinationKey`, `Enabled` come colonne canoniche. Migrazione `migrations/sqlite/122_styles_v2.sql` (opzionale, fast lane se approccio DB-first; altrimenti upgrade YML-only senza migration).

**Wave-tracker**: forward-pointer `PR-IMAGES-AI-VS-NORMAL-PLAN#A3`

**Files**:
- MODIFY: `internal/domain/asset/types_aux.go` (estende `GenerationStyle`)
- MODIFY: `internal/application/assets/generation/style_registry.go::LoadFromYAML` (parsing tutti i nuovi campi)
- NEW (opzionale): `config/generation_styles.v2.yaml` (esempio shape nuova)
- NEW tests in `style_registry_test.go` (~50 LoC)

**Diff direction**: aggiungere `Version int yaml:"version"`, `DisplayName string yaml:"display_name,omitempty"`, `PromptSuffix string yaml:"prompt_suffix,omitempty"`, `NegativePrompt string yaml:"negative_prompt,omitempty"`, `DestinationKey string yaml:"destination_key,omitempty"`, `Enabled bool yaml:"enabled"`.

**Tests (TDD, ~4 test)**:
1. `TestLoadFromYAML_ParsesAllNewFields_*` — happy + 2 partial
2. `TestLoadFromYAML_BackwardsCompatibleWithV1` — file v1 (no `version`) carica con default v1
3. `TestLoadFromYAML_RejectsDisabled` — disabled rimane caricato ma flaggato
4. `TestGenerationStyle_YAMLRoundTrip_PreservesAllFields`

**Exit-gate**: nessun call site esistente richiede migration codice-path. `Go zero-downtime-deploy`: vecchio YML continua a funzionare.

**Deadline**: 2026-08-18
**Owner**: `bg-port`

---

## Banda B — DRY + Performance (azioni 4-7)

> Banda post-A. Sblocca le riduzioni di LoC, errgroup per fan-out, dedup territori, riuso HTTP JSON helpers.

### B4 — `pkg/httpjson.GetJSON[T]` helper (DRY le 5+ search helpers)

**Goal**: collassare le 7+ copie inline di `req, _ := http.NewRequest("GET", url, nil); req.Header.Set("User-Agent", userAgent); resp, err := s.client.Do(req); io.ReadAll(resp.Body)` in un helper generico `pkg/httpjson.GetJSON[T]`.

**Wave-tracker**: forward-pointer `PR-IMAGES-AI-VS-NORMAL-PLAN#B4`

**Files**:
- NEW: `pkg/httpjson/get_json.go` (~80 LoC)
- NEW: `pkg/httpjson/get_json_test.go` (~120 LoC)
- MODIFY: `internal/application/images/storage_search.go` (sostituire 5 inline copies)

**Diff direction**:
```go
// NEW in pkg/httpjson/get_json.go
package httpjson

type GetOptions struct {
    Headers  map[string]string
    Timeout  time.Duration
    ResponseLimit int64 // default 20MB
}

func GetJSON[T any](ctx context.Context, c *http.Client, url string, opts GetOptions) (T, error) { ... }

// GetBytes: variant per immagini binarie
func GetBytes(ctx context.Context, c *http.Client, url string, opts GetOptions) ([]byte, error)
```

**Tests (TDD, ~12 test)**:
1. `TestGetJSON_Happy_UnmarshalCorrect`
2. `TestGetJSON_CTXCancelBeforeDo_Abort`
3. `TestGetJSON_Non200Status_ReturnsErrStatus{Code}`
4. `TestGetJSON_InvalidJSON_ReturnsErrUnmarshal`
5. `TestGetJSON_TimeoutExceeded_ReturnsCtxErr`
6. `TestGetBytes_Happy_*`
7. `TestGetBytes_ResponseLimitExceeded_ReturnsErrTooLarge`
8. `TestGetJSON_HeadersEchoed_ServerSeesCustomUserAgent`
9. `TestGetJSON_ReusableClient_ConnectionPool` — verify *http.Client non viene clonato
10-12: typed-pointer generic edge cases (zero value, nil pointer fields)

**Exit-gate**:
- `git grep "http.NewRequest" internal/application/images/storage_search.go` → 0 occorrenze
- `git grep "io.ReadAll" internal/application/images/storage_search.go` → 0 occorrenze (eccetto download binario specifico)
- `go test ./pkg/httpjson/ -count=1` → green

**Deadline**: 2026-08-11
**Owner**: `bg-common`

---

### B5 — `errgroup` fan-out in `searchAndDownloadInner`

**Goal**: 5 backend seriali (Wikidata → retrievalRegistry → Wikipedia-thumb → SearXNG → DDG) → fan-out parallelo con `pkg/concurrent.WithContext` first-error-wins + panic recovery.

**Wave-tracker**: forward-pointer `PR-IMAGES-AI-VS-NORMAL-PLAN#B5`

**Files**:
- MODIFY: `internal/application/images/storage_search.go::searchAndDownloadInner`
- MODIFY: `internal/application/images/storage_search.go::runRetrievalFallback` (routing fan-out)
- NEW tests in `storage_search_test.go` (~80 LoC)

**Diff direction**:
```go
// MODIFY: da seriale a fan-out
results := concurrent.WithContext(ctx)  // first-error-wins + panic recover
group := errgroup.Group{}  // già importato via pkg/concurrent

for _, backend := range s.retrievalRegistry.AllBackends() {
    backend := backend
    group.Go(func() error {
        url, err := backend.SearchImageURL(ctx, query, lang)
        if err != nil {
            return nil // skip individual backend errors
        }
        return urlResultSink.Mutex(url, backend.Name())  // race-safe collect
    })
}
// First non-empty URL wins (early-exit)
```

**Tests (TDD, ~5 test)**:
1. `TestSearch_FirstHitWins_AbortsSlowBackends_*`
2. `TestSearch_FanOut_AllTimeout_StillReturnsErr`
3. `TestSearch_PanicInOneBackend_RecoversNeighbor`
4. `TestSearch_PartialSuccess_ReturnsFirst`
5. `TestSearch_DeterministicOrder_LogOrderMatchesProviderRegistryOrder`

**Exit-gate**: `git log --since=10.days` conferma zero regression; benchmark improvement documentato (target: p99 search latency 50% lower).

**Deadline**: 2026-08-18
**Owner**: `bg-perf`

---

### B6 — DRY `scanImageAsset` (2 funzioni byte-equivalent → 1)

**Goal**: `scanImageAsset` e `scanImageAssetRows` in `images_repository.go` sono **duplicati byte-equivalent** (righe 282-318 e 320-356). Refuso a 1 sola funzione con interfaccia unificata.

**Wave-tracker**: forward-pointer `PR-IMAGES-AI-VS-NORMAL-PLAN#B6`

**Files**:
- MODIFY: `internal/infrastructure/database/sqlite/assets/images_repository.go` (~75 LoC ridotti a ~50)
- MODIFY: test esistenti (nessun cambio API esterna)

**Diff direction**: estrarre `scanImageAssetFromRow(s scanner) (*asset.ImageAsset, error)` con constraint che il `scanner` è l'unica variazione tra `*sql.Row` e `*sql.Rows`.

**Tests (TDD, ~3 test)**:
1. `TestScanImageAsset_RowForm_MatchesRowsForm` — property-based: stessa input row+rows → stesso output
2. `TestScanImageAsset_NullFields_DefaultApplied` — nullability handling preservato
3. `TestScanImageAsset_AllOriginsPopulated_F1BCompliance` — origin/provider first-class columns

**Exit-gate**: `git grep "func scanImageAssetRows" internal/infrastructure/database/sqlite/assets/` → 0.

**Deadline**: 2026-08-04
**Owner**: `bg-sqlite`

---

### B7 — Dedupe `SearchAll` su `routing.Router` per top-K territory merge

**Goal**: territorio dual-write può ritornare duplicati in `Router.SearchAll` (un'immagine retrieved promossa a generated via backfill finisce in entrambi). Deduplica per `AssetID` o score-merge quando territories si sovrappongono.

**Wave-tracker**: forward-pointer `PR-IMAGES-AI-VS-NORMAL-PLAN#B7`

**Files**:
- MODIFY: `internal/application/images/routing/search_resolver.go::SearchAll`
- NEW helper: `internal/application/images/routing/dedupe.go` (~40 LoC)
- NEW tests

**Diff direction**: merge con `max(score)` per-id + tier-keep (generated>retrieved>uploaded) per evitare collisioni visualizzate.

**Tests (TDD, ~4 test)**:
1. `TestSearchAll_DedupesAcrossTerritories_*`
2. `TestSearchAll_NoDedup_AcrossDistinctIDs`
3. `TestSearchAll_TierPrecedence_GeneratedBeatsRetrieved`
4. `TestSearchAll_StableOrderForTiedScores`

**Exit-gate**: dedupe latency < 5ms per 1000 hits.

**Deadline**: 2026-08-25
**Owner**: `bg-port`

---

## Banda C — Cleanup YAGNI + Dead Code (azioni 8-10)

> Banda tail. Cleanup di zombie parametri, stub non risolti e superfici difensive in eccesso.

### C8 — Rimozione `extra interface{}` zombie param

**Goal**: `ImageGenService.SearchAndDownload` porta un parametro `extra interface{}` che **tutti i call site passano `nil`** (audit `image-territories-audit.md` §3.2). Rimuovere la firma + 6 call site + 1 fake-test.

**Wave-tracker**: forward-pointer `PR-IMAGES-AI-VS-NORMAL-PLAN#C8`

**Files**:
- MODIFY: `internal/application/scripts/usecase/services.go` (~5 LoC delta)
- MODIFY: 6 call site production (`flow_helpers.go`, `processor_images.go`, `impl.go`, `fullimages/service.go`, `lessons/generator.go`, fake test in `processor_images_voiceover_test.go`)
- MODIFY: 1 fake interface (`processor_images_voiceover_test.go`)

**Diff direction**: drop param, drop unused imports.

**Tests (TDD, ~3 test)**:
1. `TestSearchAndDownload_NoExtraParam_Compiles`
2. `TestFakeService_NoExtraMethod_InTestFile`
3. `TestFlowHelpers_DoesNotPassExtra_Nil`

**Exit-gate**: `git grep "extra interface{}" internal/ --glob '!**/*_test.go'` → 0 production occurrences.

**Deadline**: 2026-07-25 (1 wave)
**Owner**: `bg-port`

---

### C9 — Risolvere o rimuovere `extractSubjectAndTags` stub

**Goal**: `internal/application/images/ingest_direct.go::extractSubjectAndTags` è dichiarato stub MUST-be-replaced ("behaviour of tags-only / unknown-slug paths will degrade silently otherwise"). Va o **concluso** (introdurre `SubjectTagsService` reale) o **fisicamente rimosso** (e i call site migrati via `pkg/termutil.ExtractLikelyNames` già esistente).

**Wave-tracker**: forward-pointer `PR-IMAGES-AI-VS-NORMAL-PLAN#C9`

**Files** (opzione CONCLUDERE):
- MODIFY: `internal/application/images/ingest_direct.go` (~30 LoC parsers da `pkg/termutil`)
- NEW: `internal/application/images/subject_tags_service.go` (~50 LoC)
- NEW tests (~80 LoC)

**Files** (opzione RIMUOVERE):
- DELETE: `internal/application/images/ingest_direct.go` (interamente)
- MODIFY: 3 call site → droppare la chiamata (i SubjectID sono già nel payload upstream)
- MODIFY: `internal/application/images/service.go` (rimuove wiring)

**Exit-gate (entrambe)**:
- `rg "extractSubjectAndTags" internal/` → 0 production
- Test sui SubjectID derivation copre i 4 path: prompt-only / slug-known / multi-tag / unknown-slug

**Decision**: open question pending user confirmation via `ask_user` followup. Default proposal: REMOVE the stub (YAGNI residue) + subjects resolved at handler side. Alternative: CONCLUDE the stub by introducing a typed `SubjectTagsService` provider. Default removes 1 file outright; alternative adds ~3 new files (~150 LoC).

**Deadline**: 2026-07-25
**Owner**: `bg-port`

---

### C10 — Rimuovere `pythonEmbeddingAdapter` zombie

**Goal**: `internal/application/images/ingest_semantic.go::pythonEmbeddingAdapter` (40 LoC) chiama un server di embedding via HTTP che non esiste in produzione (tutti i vector store passano per `internal/media/vectorstore/`). Surface fall-through silenzioso per chiunque lo chiami.

**Wave-tracker**: forward-pointer `PR-IMAGES-AI-VS-NORMAL-PLAN#C10`

**Files**:
- DELETE o FAR-REWRITE: `internal/application/images/ingest_semantic.go` (se nessun call site reale, delete)
- NEW (opzionale): port `vectorstore.LocalEmbedder` in `internal/media/vectorstore/embedder.go` per chi ha bisogno di un fallback locale CPU-only

**Decision**: rg audit pre-commit:
```bash
rg "pythonEmbeddingAdapter" internal/ --glob '!**/*_test.go'
```
→ 0 production callers oggi. Default: DELETE.

**Exit-gate**:
- `git grep "embedding.server.URL" internal/` → 0 across production
- `git grep "EmbeddingPassage" internal/application/` → 0 (era solo l'adapter)
- Test fallback locale `vectorstore.LocalEmbedder.EmbedPassage` se introdotto

**Deadline**: 2026-07-25
**Owner**: `bg-port`

---

## Execution order (cross-band co-dependencies)

```
A1 (StyleID typed)
  └── A2 (StyleResolver fail-closed)         [richiede A1 value-object]
       └── A3 (GenerationStyle v2 shape)    [richiede A1 + A2]

B4 (httpjson helper)
  └── B5 (errgroup fan-out)                 [richiede B4 per riuso]
       └── B7 (Routing SearchAll dedupe)    [richiede B5 per ordering stabile]
            └── B6 (DRY scanImageAsset)     [indipendente da B5]

C8 (extra param removed)        [indipendente — può partire subito]
C9 (extractSubjectAndTags)      [indipendente]
C10 (pythonEmbeddingAdapter)    [indipendente]

Race-trackable parallelism: (A1, B4, C8, C9, C10) → A2 → A3, B6 → B7
```

**First 5 paralleli** (1 wave): A1, B4, C8, C9, C10
**Second 3**: A2, A3, B6
**Last 1**: B7 + B5 (se non già fatto da B5 closure)

---

## Honest limitations (godlike/07 disclosure)

1. **Static analysis baseline**: la classifica 46/27/21 commits è calcolata sul tree corrente (snapshot 2026-07-04). Una rotazione di $90g$ successiva riposiziona i numeri. Forward-pointer obbligatorio: `PR-IMAGES-AI-VS-NORMAL-CROSSREF-2026-08-15` riapre lo stesso `git log --since=90.days` post-chiusura banda A/B/C e aggiorna la matrice.

2. **Dipendenze non mappate**: questo action plan copre ESCLUSIVAMENTE il confine immagini AI/generate ⊕ immagini normali/retrieved. Non copre:
   - Script generation pipeline (vedi `architecture/action-plans/2026-07-02-unified-semantic-multimodal-search.md`)
   - YouTube clip extraction (§12 cutover, già completato)
   - Voiceover destination resolution (separato)
   - Clipindexer (separato)

3. **Forward-pointer a incompiuti**: il `model string` field su `GenerateRequest`/`GeneratedImage` non è rimosso (è solo "retired by surface-4" — il commento di `port_out.Model` fallback c'è ancora). `PR-MODEL-RETIREMENT` è un'azione fuori scope; va tracciata in `architecture/deprecations.yaml` separatamente.

4. **Race-condition della catena A→B**: A1 deve chiudere prima di A2, ma A2 può partire non appena A1 ha il merge su `main`. Il pattern è: A1 committa, A2 si apre non appena `git pull --ff` è verde su `main`. NO branch — solo sequenza di commit su main con trailer `Co-authored-by:`.

5. **Memory-footprint di A3**: la shape v2 con 8 campi aumenta le righe YAML parsing. Su cataloghi >1000 stili, il load-time può aumentare. Forward-pointer: misurare con `time.Now()` wrapper in `LoadFromYAML` test, tenere sotto 100ms.

6. **Dedupe routing B7**: richiede consenso su tie-breaker policy (territorio precedence vs score-merge). Default proposto: `generated > retrieved > uploaded`. Da confermare in CAP utente se la policy è diversa.

---

## Wave-tracker forward-pointer

Questo action plan atterra come:
1. `docs(arch-plan): images AI vs normal action plan (2026-07-04)` → questo file
2. `chore(architecture): PR-IMAGES-AI-VS-NORMAL-PLAN wave-tracker entry` → `architecture/current.yaml`

Entry wave-tracker proposta:
```yaml
- id: PR-IMAGES-AI-VS-NORMAL-PLAN
  name: "Images AI vs Normal — Refactor & Stabilization Wave"
  status: active
  owner: architecture
  deadline: 2026-08-25
  exit_gate: false
  block_on:
    - PR-GODOBJ-3-IMAGES-GENERATION  # decomposition already in flight
  linked_issues:
    - id: A1
      label: "StyleID typed + StyleDefinition value-object"
      deadline: 2026-08-04
    - id: A2
      label: "StyleResolver fail-closed + ErrUnknownStyle sentinel"
      deadline: 2026-08-11
    - id: A3
      label: "GenerationStyle shape v2 (YML v2)"
      deadline: 2026-08-18
    - id: B4
      label: "pkg/httpjson.GetJSON[T] helper"
      deadline: 2026-08-11
    - id: B5
      label: "errgroup fan-out in searchAndDownloadInner"
      deadline: 2026-08-18
    - id: B6
      label: "DRY scanImageAsset (duplicati byte-equivalent)"
      deadline: 2026-08-04
    - id: B7
      label: "Dedupe SearchAll across territories"
      deadline: 2026-08-25
    - id: C8
      label: "Remove extra interface{} from ImageGenService"
      deadline: 2026-07-25
    - id: C9
      label: "Resolve or remove extractSubjectAndTags stub"
      deadline: 2026-07-25
    - id: C10
      label: "Remove pythonEmbeddingAdapter zombie"
      deadline: 2026-07-25
  exit_signal_criteria:
    - go test ./... -count=1 → green
    - bash scripts/ci-architectural-checks.sh → zero new violations
    - git grep "style string" internal/ -P '[^t]' --non-matching  → 0 magic literals
    - rg "extra interface{}" internal/ --glob '!**/*_test.go' → 0
    - rg "pythonEmbeddingAdapter" internal/ --glob '!**/*_test.go' → 0
```

---

## Related canonical surfaces (forward-pointer)

- `architecture/action-plans/2026-07-02-unified-semantic-multimodal-search.md` — semantic search unification (rotating-uncle sibling plan)
- `architecture/action-plans/2026-07-03-godobjects-decomposition.md` — God-object decomposition wave (`PR-GODOBJ-3-IMAGES-GENERATION` is the canonical blocker for `internal/application/images/service.go` 46-commits hotspot)
- `docs/plans/image-territories-audit.md` — FASE 0 audit (baseline evidence di tutti gli items in questo action plan)
- `architecture/image-territories-cutover-report.md` — meta-stop cutover decisions
- AGENTS.md Pattern 0 (Port abstraction), Pattern 5 (Split package), Pattern 8 (API package: thin transport only), godlike/06 (one-owner-per-fact), godlike/07 (typed-error / no-fake-availability)

---

## Cross-reference forward-pointer (post-wave audit)

`PR-IMAGES-AI-VS-NORMAL-CROSSREF-2026-08-15` (deadline 2026-08-15): riapre la stessa `git log --since=90.days --pretty=format: --name-only` post-chiusura delle sotto-azioni e aggiorna la matrice. Se nuovi hotspots emergono fuori da questo action plan (es. `sync_generation.go`, `generation_usecase.go` se la decomposizione PR-GODOBJ-3 sposta complessità lì), aggiungere nuove under-actions alla wave corrente.

Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>
