# Piano d'Azione — Sottosistema Script (PipelineGen)

> **Data**: 2026-07-04
> **Analisi basata su**: AGENTS.md methodology (godlike/06 SSOT, godlike/07 no-fake-availability, Pattern 0-11)
> **Metodologia**: Checklist code smell × 4 aree strutturali (Fragilità Architetturale, Complessità, Prestazioni, Dead Code)
> **Regola**: NO BRANCHES, ONLY MAIN, commit + push diretto su `origin/main`, trailer `Co-authored-by:`

---

## Riepilogo Architetturale

Il sottosistema script (~156 file Go, 6 strati) segue un'architettura Clean/Hexagonal con Pattern 0 port abstraction impeccabile. La pipeline unificata a 7 fasi (`Normalize → Validate → Resolve Source → Build Plan → Generate → Postprocess → Result`) ha sostituito 5+ endpoint legacy in un unico `GenerationEnvelopeV2`.

### Flusso Canonico

```
POST /api/script/generate → GenerationEnvelopeV2
  → 1. NormalizeItem (preset > config > safety default)
  → 2. ValidateItem (semantic checks)
  → 3. SourceRegistry.Resolve (text/clips/catalog/search/curate)
  → 4. BuildPlan → ResolvedGenerationPlan
  → 5. Engine.Generate → Ollama LLM (con memory gate cache)
  → 6. PostProcessorRegistry.Run (8 processor in ordine)
  → 7. buildGenerationResult → GenerationResult tipizzato
```

---

## Priorità d'Intervento (ordinata per impatto)

### 🔥 AZIONE 1 — Splittare `ScriptFlowHandler` (22 dipendenze → handler per capability)

**File**: `internal/api/script/handler_flow.go`
**Problema**: God Object con 22 campi iniettati. L'handler funge da "composition root" de facto.
**Classe di bug**: Aggiungere una dipendenza richiede modificare 3+ file (handler struct, ScriptFlowDeps, module.Dependencies, wire_script.go).
**Soluzione proposta**: Estrarre handler separati per capability:
- `HandlerGenerate` — solo `POST /generate` (usa Engine + Jobs)
- `HandlerCurate` — solo `POST /curate` (usa MediaCurator + ClipSourceBuilder)
- `HandlerSearch` — solo `GET /clips/search` (usa ClipsSearcher)
- `HandlerFlow` — operazioni flow (`/regenerate`, `/cache/evict`, `/jobs/:id`)
- `HandlerLegacy` — adapter deprecati (in attesa di CUTOVER)

Ogni handler riceve SOLO le dipendenze che gli servono veramente.
**Deadline**: 2026-07-25
**Stima**: ~2-3 commit incrementali (un handler per commit)

---

### 🔥 AZIONE 2 — Estrarre factory dal `wireScriptFlow` orchestrator

**File**: `internal/app/wire_script.go` (~400+ linee)
**Problema**: Feature Envy — il composition root conosce troppi dettagli di costruzione di package diversi.
**Soluzione proposta**: Estrarre factory file separati:
- `wire_script_usecases.go` — costruzione di `oneUC`, `manyUC`, `sectionRegen`, `cacheEvictionUC`
- `wire_script_resolvers.go` — costruzione di `sourceReg` + 5 resolvers
- `wire_script_adapters.go` (esistente, espandere) — drive folder, document creator, admin token

Il file `wire_script.go` diventa puro orchestrator (~100 linee).
**Deadline**: 2026-07-25
**Stima**: 1 commit (estrazione meccanica)

---

### ⚡ AZIONE 3 — Sostituire nomi string-based dei postprocessors con typed constants

**File**: `internal/application/scripts/usecase/generation_plan_builder.go`, `internal/application/scripts/adapters/postprocessor_registry.go`
**Problema**: Primitive Obsession — i nomi dei processor sono stringhe (`"entities"`, `"metadata"`, etc.). Un typo viene scoperto solo a runtime.
**Soluzione proposta**: Aggiungere typed constants in `postprocessor_registry.go`:
```go
type ProcessorName string
const (
    ProcEntities          ProcessorName = "entities"
    ProcMetadata          ProcessorName = "metadata"
    ProcClipBindings      ProcessorName = "clip_bindings"
    ProcStockAssociation  ProcessorName = "stock_association"
    ProcVoiceover         ProcessorName = "voiceover"
    ProcImages            ProcessorName = "images"
    ProcDocument          ProcessorName = "document"
    ProcPersistence       ProcessorName = "persistence"
)
```
Aggiornare `buildPostprocessorList` e il registry per usare i typed constants. La `CanonicalProcessorNames()` closed-set function blocca drift futuri.
**Deadline**: 2026-07-25
**Stima**: 1 commit

---

### 📋 AZIONE 4 — Rimuovere `interface{}` fields dall'Engine

**File**: `internal/application/scripts/usecase/engine.go`
**Problema**: I campi `ollamaGen interface{}` e `memorySvc interface{}` sono fake abstractions — ognuno ha un'interfaccia narrow già definita (`scriptOllamaGenerator`, `memoryGateChecker`).
**Soluzione proposta**: Sostituire con i tipi narrow direttamente:
```go
type Engine struct {
    ollamaGen scriptOllamaGenerator
    memorySvc memoryGateChecker  // ora sempre nil post-Commit H Phase 2
    log       *zap.Logger
}
```
Rimuovere i type assertion a runtime.
**Deadline**: 2026-08-01
**Stima**: 1 commit

---

### 📋 AZIONE 5 — Accelerare CUTOVER dei legacy adapter

**File**: `internal/api/script/handler_legacy_adapters.go` (~600+ LOC), `handler_legacy_adapters_test.go` (~900+ LOC)
**Problema**: Dead code con date di rimozione pianificate (2026-09-30, 2026-12-31). Aggiunge ~1500 LOC di superficie di test e confusione su quale sia il "vero" entry point.
**Soluzione proposta**: Verificare se le date di rimozione possono essere anticipate. Se i client sono già migrati a `POST /api/script/generate`, procedere con CUTOVER immediato. Altrimenti, aggiungere un contatore Prometheus per tracciare l'uso residuo.
**Deadline**: 2026-08-15
**Stima**: 1 commit (rimozione) o 1 commit (telemetria)

---

### 📋 AZIONE 6 — Verificare e chiudere `scripts/_tmp_verify_503.py`

**File**: `scripts/_tmp_verify_503.py` (untracked)
**Problema**: File temporaneo non tracciato. Potrebbe essere un test di diagnostica utile o un artefatto da pulire.
**Soluzione proposta**: Ispezionare il file. Se è un test utile, documentarlo e aggiungerlo al repo. Se è un artefatto temporaneo, rimuoverlo.
**Deadline**: 2026-07-10
**Stima**: 1 commit (ispezione + cleanup)

---

## Riepilogo per Categoria Code Smell

| Categoria | Stato | Azioni |
|-----------|-------|--------|
| **Dipendenze Cicliche** | ✅ Nessuna | — |
| **Feature Envy** | ⚠️ wire_script.go | Azione 2 |
| **I/O Binder** | ✅ Pattern 0 ovunque | — |
| **God Object** | ⚠️ ScriptFlowHandler (22 campi) | Azione 1 |
| **Global Mutable State** | ✅ Nessuno | — |
| **Primitive Obsession** | ⚠️ Nomi processor string-based | Azione 3 |
| **Fake Abstraction** | ⚠️ interface{} nell'Engine | Azione 4 |
| **Dead Code** | ⚠️ Legacy adapter (~1500 LOC), _tmp script | Azione 5, 6 |
| **YAGNI Violations** | ✅ Pipeline minimalista | — |

---

## Regole di Esecuzione

1. **NO BRANCHES** — ogni commit atterra direttamente su `origin/main`
2. **Commit auto-sufficienti** — ogni azione è un commit atomico con `gofmt + go vet + go build` verde sul subtree target
3. **Trailer obbligatorio**: `Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>`
4. **Push incrementale** dopo ogni commit: `git fetch origin && git rebase origin/main && git push origin main`
5. **Race recovery**: se push rejected, diagnosticare con `git log --oneline HEAD..@{u}` e applicare Git-Lesson-4/5
6. **Nessun `--force`** su `origin/main` — il fast-forward exit è sempre possibile
