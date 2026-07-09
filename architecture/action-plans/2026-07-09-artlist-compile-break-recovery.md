# Artlist Compile Break Recovery & Drive Content Completion
## Action Plan — 2026-07-09

### §0 — TL;DR

Due anomalie distinte colpiscono il server PipelineGen:

1. **Compile break Artlist (RISOLTO):** `ArtlistSearchStrategy` duplicato tra
   `ports.go` e `search_strategy.go` → `go build` falliva. Fix in 3 commit su
   `origin/main`: `f442fe200` (nuovo file `search_strategy.go` con tipo canonico
   + `Normalize()` + `IsValid()` + `ResolveSearcherChain()`) + `aeeacf44f`
   (test `TestArtlistSearchStrategyNormalize` + `TestResolveSearcherChainStrategies`)
   + `f45ad09d2` (rimozione tipo duplicato da `ports.go`).

2. **Runtime `context deadline exceeded`:** Errore separato dal compile break.
   Viene dal tentativo di enqueue job asincrono (round 9/10-11/12/post-match).
   Root cause probabile: SQLite lock / broker timeout / DB contention durante
   l'enqueue. NON correlato ad Artlist.

3. **Contenuti Drive incompleti:** Round 1, 2, 5, 7 completati. Round 9,
   10-11, 12, post-match da completare.

### §1 — Stato attuale (post-fix compile break)

| Componente | Stato | Commit SHA |
|---|---|---|
| `search_strategy.go` | ✅ Shipped | `f442fe200` |
| Test strategy | ✅ Shipped | `aeeacf44f` |
| `ports.go` dedup | ✅ Shipped | `f45ad09d2` |
| `go build ./...` | ✅ Compila | — |
| Working tree cleanup | ⚠️ 10 file modificati + ~15 untracked JSON | Da pulire |
| `/ready` health check | ❓ Da verificare | — |
| Drive round 9 | ❌ Mancante | — |
| Drive round 10-11 | ❌ Mancante | — |
| Drive round 12 | ❌ Mancante | — |
| Drive post-match | ❌ Mancante | — |
| `context deadline exceeded` | ❌ Da diagnosticare | — |

### §2 — Sequenza azioni (ordine obbligatorio)

#### Fase 1 — Pulizia working tree (P0, immediato)

Il working tree ha 10 file modificati e ~15 file JSON untracked. Alcuni sono
work-in-progress legittimi (es. PR-TRANSLATE-SCRIPT-SPEC), altri sono residui
di test. Azioni:

1. `git status` → inventory completo
2. Per i file modificati legittimi: commit + push separati per capability
3. Per i file JSON di test: aggiungere a `.gitignore` o rimuovere
4. Per `scripts/bridges/login.py`: valutare se è production o test

#### Fase 2 — Verifica server (P0, immediato dopo pull)

```bash
git pull origin main
go test ./internal/application/assets/providers/artlist -run 'TestArtlistSearchStrategy|TestResolveSearcherChain' -count=1 -v
go run ./cmd/admin list-drive-folder -folder 1J-zIuqroF0rkTrKxU-tmZu9e5rN20ggV -sync-db=false
curl -sS -m 5 http://127.0.0.1:8000/ready | jq
```

#### Fase 3 — Diagnostica `context deadline exceeded` (P1)

Se `/ready` mostra il broker/jobs ok, investigare:
- `data/media/media.db.sqlite` dimensione e integrità
- WAL mode e `busy_timeout`
- Log broker per timeout durante enqueue
- Pool di connessioni SQLite esaurito?

#### Fase 4 — Completamento Drive content (P1)

Completare i 4 round mancanti (9, 10-11, 12, post-match) usando il workflow
stock pipeline standard, verificando che `/api/stock-pipeline/search-and-run`
funzioni correttamente per ogni batch.

#### Fase 5 — Hardening (P2)

- Aggiungere Check 65 in `scripts/ci-architectural-checks.sh` per prevenire
  future ridefinizioni duplicate di tipi canonici
- Aggiungere test di compilazione che fallisce su tipi duplicati

### §3 — Per-PR execution checklist (godlike/07 EXPAND→CONTRACT)

Ogni PR atterra **direttamente su `main`** (AGENTS.md Git-Lesson-2).
Nessun branch, nessun `--no-ff`, nessun `--force`.

| # | PR ID | Azione | Priorità | Deadline |
|---|-------|--------|----------|----------|
| 1 | `PR-ARTLIST-COMPILE-FIX` | Commit `ports.go` dedup | P0 | ✅ Shipped `f45ad09d2` |
| 2 | `PR-WORKING-TREE-CLEANUP` | Pulizia 10 file modificati + JSON untracked | P0 | 2026-07-09 |
| 3 | `PR-SERVER-HEALTH-VERIFY` | `/ready` check + `list-drive-folder` test | P0 | 2026-07-09 |
| 4 | `PR-CONTEXT-DEADLINE-DIAGNOSE` | Diagnosticare `context deadline exceeded` | P1 | 2026-07-16 |
| 5 | `PR-DRIVE-ROUND-9` | Completare round 9 Drive | P1 | 2026-07-16 |
| 6 | `PR-DRIVE-ROUND-10-11` | Completare round 10-11 Drive | P1 | 2026-07-16 |
| 7 | `PR-DRIVE-ROUND-12` | Completare round 12 Drive | P1 | 2026-07-16 |
| 8 | `PR-DRIVE-POST-MATCH` | Completare post-match Drive | P1 | 2026-07-16 |
| 9 | `PR-CHECK-65-DUPLICATE-TYPE` | Forward-prevention gate per tipi duplicati | P2 | 2026-07-23 |

### §4 — Verification gates

- [x] `go build ./internal/application/assets/providers/artlist/...` ✅
- [x] `go build ./internal/app/...` ✅
- [x] `go test -run TestArtlistSearchStrategy ./internal/application/assets/providers/artlist/` ✅
- [ ] `go run ./cmd/admin list-drive-folder ...` (da eseguire su VPS)
- [ ] `curl /ready | jq` (da eseguire su VPS)
- [ ] `go test -short ./...` (post cleanup working tree)

### §5 — Honest scope-lock (godlike/07)

- Il compile break Artlist è RISOLTO (3 commit su origin/main)
- Il `context deadline exceeded` è un problema SEPARATO (job broker/SQLite, NON Artlist)
- I contenuti Drive mancanti sono un problema SEPARATO (stock pipeline, NON Artlist)
- `RemoteDisconnected` è un terzo problema (request path lungo, NON Artlist)
- Non confondere i tre problemi: ognuno ha la propria root cause e fix surface

### §6 — Cross-references (godlike/06 umbrella)

- `architecture/current.yaml#ART-002` — Artlist wave tracker
- `architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04` — carry-forward
- `internal/application/assets/providers/artlist/search_strategy.go` — canonical SSOT
- `internal/app/build_bundles_artlist.go:302` — wiring site

### §7 — Lifecycle audit-trail

| Data | Evento | Commit SHA |
|------|--------|------------|
| 2026-07-08 | Aggiunto `search_strategy.go` | `f442fe200` |
| 2026-07-08 | Aggiunti test | `aeeacf44f` |
| 2026-07-09 | Rimosso duplicato da `ports.go` | `f45ad09d2` |
| 2026-07-09 | Action plan creato | questo commit |

### §8 — git working tree snapshot (pre-cleanup)

**File modificati (non committati):**
- `internal/application/assets/providers/artlist/ports.go` ✅ COMMITTED `f45ad09d2`
- `internal/application/images/storage_ingest_direct.go`
- `internal/application/images/storage_service.go`
- `internal/application/scripts/adapters/postprocessor_composite_merge.go`
- `internal/application/scripts/adapters/postprocessor_document.go`
- `internal/application/scripts/adapters/processor_translation.go`
- `internal/application/scripts/usecase/generation_plan_builder.go`
- `internal/application/scripts/usecase/generation_plan_builder_test.go`
- `internal/domain/script/output_spec.go`
- `internal/domain/script/resolved_plan.go`
- `scripts/bridges/slide_worker.py`

**File untracked:**
- `internal/application/scripts/adapters/processor_translation_pr5_pr6_test.go`
- `scripts/bridges/login.py`
- `test1_response.json`, `test1_response_zoom.json`
- `test_script_with_images_res10.json`, `test_script_with_images_res11.json`
- `whiteboard_img_only_response*.json` (11 file)
- `whiteboard_response.json`

Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>
