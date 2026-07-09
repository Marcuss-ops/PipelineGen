# Static Audit Action Plan — 2026-07-09

> **Fonte:** Audit statico sul repo, ultimo commit `2ccd858c...` del 2026-07-09.
> **Regola:** NO BRANCHES, ONLY MAIN, PUSH E COMMITTA MAIN FREQUENTEMENTE.
> **Ogni PR atterra direttamente su `main`** per AGENTS.md Git-Lesson-2.

---

## Priority Bands

| Band | # Finding | Descrizione | Deadline |
|------|-----------|-------------|----------|
| **P0** | 1 | 5 job family vs 4 in startup validation | 2026-07-12 |
| **P0** | 2 | Doppia SSOT job type (`domain/job` vs `application/jobs`) | 2026-07-14 |
| **P1** | 3 | Due registry job con superfici vicine | 2026-07-18 |
| **P1** | 4 | Legacy image route ancora operativa | 2026-07-16 |
| **P1** | 6 | Fallback Ollama entity extraction silenzioso | 2026-07-20 |
| **P1** | 7 | Fallback Artlist Pexels/Pixabay impliciti | 2026-07-22 |
| **P2** | 5 | Legacy script route 410 (sane, non toccare) | 2026-09-01 |
| **P2** | 8 | Spec aliases immagini | 2026-08-01 |
| **P2** | 9 | Node scraper shim/commento | 2026-08-01 |

---

## Per-PR Execution Plan

### PR-AUDIT-1 — P0: Aggiungere `TypeClipRegister` a `workflowRefs` (5 → 5)

**File:** `internal/app/registry.go`
**Cambio:** In `c3ValidateRuntimeGraph()`, aggiungere `jobpkg.TypeClipRegister` allo slice `workflowRefs`. Aggiornare i commenti da "4 canonical" a "5 canonical".
**Verifica:** `go vet ./internal/app/...` + `go build ./internal/app/...` + `go test -short -count=1 ./internal/app/...`

### PR-AUDIT-2 — P0: Unificare job type SSOT in `domain/job`

**File coinvolti:**
- `internal/domain/job/job.go` — dichiarare TUTTI i `Type*` come SSOT unico
- `internal/application/jobs/registry_types.go` — convertire i literal in alias (`= job.TypeXxx`) verso `domain/job`
- Correggere typo `TypeBulUploadYouTubeClips` → alias compatibile temporaneo `TypeBulUploadYouTubeClips = job.TypeBulkUploadYouTubeClips`

**Verifica:** `gofmt -l` + `go vet` + `go build` + `rg 'TypeBul' internal/` deve mostrare solo l'alias

### PR-AUDIT-3 — P1: Rotte legacy immagini → 410 o feature flag

**File:** `internal/api/images/legacy_generate_handler.go`
**Opzioni (da decidere con l'utente):**
- **Opzione A:** Flip a 410 Gone come le legacy script route
- **Opzione B:** Feature flag `cfg.Features.LegacyImagesGenerateEnabled` (default `false`)

### PR-AUDIT-4 — P1: Ollama fallback entity extraction → opt-in + provenance

**File:** `internal/application/semantic/` (o dove risiede `ExtractEntitiesFromScriptWithModel`)
**Azione:** Aggiungere flag `EntityExtractionFallbackMode` e marcare i risultati come `source: "heuristic_fallback"`

### PR-AUDIT-5 — P1: Artlist Pexels/Pixabay fallback → strategia esplicita

**File:** `internal/app/build_bundles_artlist.go`
**Azione:** Spostare la selezione Pexels/Pixabay in un resolver comune con strategia esplicita (`artlist_only`, `artlist_then_public_fallback`, `public_only_for_dev`)

### PR-AUDIT-6 — P2: Node scraper shim — aggiornare commento o creare `bin/artlist-search.js`

**File:** `node-scraper/artlist_search.js`
**Opzioni:**
- Creare `node-scraper/bin/artlist-search.js` come CLI canonica
- O aggiornare il commento dichiarando `artlist_search.js` come shim ufficiale fino a retirement

### PR-AUDIT-7 — P1: Unire i due registry job (`application/jobs` + `domain/job`)

**File:** `internal/application/jobs/registry.go` + `internal/domain/job/canonical_definitions.go`
**Azione:** Portare il wiring handler dentro `CapabilityDeps.Jobs` o rimuovere la superficie forward-only `capability_registry.go`

### PR-AUDIT-8 — P2: Test architetturale anti-drift `spec_aliases.go`

**File:** Nuovo test in `cmd/archcheck/scan/` o `internal/application/cyclicdeps/`
**Azione:** Bloccare nuovi `spec_aliases.go` fuori dai territori approvati (generated, retrieved)

### PR-AUDIT-9 — P2: Legacy script route — verifica contatori e cleanup deadline

**File:** `internal/api/script/handler_legacy_deprecation.go`
**Azione:** Verificare che i counter `legacy_generate_*_total` siano attivi e che la deadline di retirement 2026-12-31 sia documentata

---

## Execution Order (per priorità)

1. **PR-AUDIT-1** (P0, 5→5 job families) — più urgente, fix immediato
2. **PR-AUDIT-2** (P0, SSOT job type) — richiede più attenzione, 3+ file
3. **PR-AUDIT-3** (P1, legacy images) — decisione utente richiesta
4. **PR-AUDIT-4** (P1, Ollama fallback) 
5. **PR-AUDIT-5** (P1, Pexels/Pixabay)
6. **PR-AUDIT-7** (P1, registry unification)
7. **PR-AUDIT-6** (P2, Node scraper)
8. **PR-AUDIT-8** (P2, spec_aliases test)
9. **PR-AUDIT-9** (P2, legacy script verify)

---

## Verification Gates (per ogni PR)

- `gofmt -l` clean sui file toccati
- `go vet ./<subtree>/...` exit 0
- `go build ./<subtree>/...` exit 0
- `go test -short -count=1 ./<subtree>/...` PASS
- `git commit` con `Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>`
- `git fetch origin && git log --oneline HEAD..@{u}` vuoto → `git push origin main`

---

## Cross-references

- `CANONICAL.md` §1 — authoritative doc resolution
- `AGENTS.md` Git-Lesson-2 — direct-to-main workflow
- `AGENTS.md` Git-Lesson-3 — Co-authored-by trailer
- `AGENTS.md` Git-Lesson-4 — race-protect pre-push
- `architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04` — carry-forward unchanged

---

## Lifecycle Audit-Trail

| Data | Evento |
|------|--------|
| 2026-07-09 | Action plan creato da audit statico |
| TBD | PR-AUDIT-1 shipped |
| TBD | PR-AUDIT-2 shipped |
| TBD | ... |

---

**Co-authored-by:** PipelineGen Agent <agent@pipelinegen.local>
