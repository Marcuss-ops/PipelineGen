# CLEANUP-07 — Ondata 2C: platform rows migrate to kernel

**Data:** 2026-11-24
**Priorità:** P1 (chiude le 11 entry platform→scripts)
**Stato:** **CLOSED** — inventario `platformCapabilityScriptsInventory` = **0**;
07A, 07B e 07C completati.
**Owner:** `ollama-adapters` + `sqlite-*` + `kernel`
**Origine:** radiografia 2026-11-24, Ondata 2; inventario RA-01
**Base:** `refactored` @ `3e0a9e3ae`; chiusura misurata sul working tree 2026-11-24

## 1. Problema

Oltre ai 13 `rustexec` di CLEANUP-05, l'inventario
`platformCapabilityScriptsInventory` conteneva **11 file** che importavano
`internal/capabilities/scripts` per tipi di riga o contratti:

| File | Owner / trigger |
| --- | --- |
| `platform/ollama/adapters/metadata_generator.go` | metadata generator port → kernel |
| `platform/ollama/adapters/person_image_scorer.go` | person image scorer port → kernel |
| `platform/sqlite/history/reader.go` | history row types → kernel |
| `platform/sqlite/jobregistry/execution_recorder.go` | run/step types → kernel |
| `platform/sqlite/overlayrendercache/cache.go` | cache key/artifact types → kernel |
| `platform/sqlite/performance/source.go` | metrics types → kernel |
| `platform/sqlite/rendermetrics/registry.go` | metrics types → kernel |
| `platform/sqlite/scripts/artifact_checkpoint_repository.go` | script run/checkpoint types → kernel |
| `platform/sqlite/scripts/run_reconciliation.go` | script run types → kernel |
| `platform/sqlite/scripts/run_repository.go` | script run types → kernel |
| `platform/sqlite/scripts/semantic_plan_cache_adapter.go` | cache types → kernel |

Tre file erano già stati migrati nella sessione precedente (checkpoint,
overlay cache, semantic plan cache). Restavano **8** adapter: i due `ollama` e
sei `sqlite`.

## 2. Soluzione

### 07A — run / step / execution / checkpoint

- `ExecutionContext`, `ExecutionStep`, `ArtifactOperation`,
  `ExecutionRecorder`, `RunStatus`, `Stage` e le costanti di operations sono
  ora in `internal/kernel/script`.
- `capabilities/scripts/ports_execution.go` e `runstate/run.go` li
  ri-esportano; i chiamanti esistenti non cambiano.
- I DTO di persistenza che incorporano `GenerateRequest`/`GenerateResult`
  (`GenerationRun`, `FailRunInput`, `RunRepository`,
  `RunReconciliationRepository`, `StaleSubmissionRun`, `ErrRunNotFound`) sono
  in `internal/capabilities/scripts/ports/run`. È una **porta**, non una nuova
  eccezione del gate: vive dentro `scripts/ports`, già ammesso.
- Migrati `sqlite/history/reader.go`, `sqlite/jobregistry/execution_recorder.go`,
  `sqlite/scripts/run_repository.go`, `sqlite/scripts/run_reconciliation.go`.

### 07B — render metrics / overlay cache / performance

- `RenderAttemptAnalytics`, `RenderAttemptRecorder` e `ContentCounts` sono in
  `internal/kernel/script/renderqueue`; `overlays.ContentCounts` è un alias.
- `ReportSource` è in `internal/kernel/script/performance`.
- `AudioPipelineMetrics` era già in `internal/kernel/audio`.
- Migrati `sqlite/performance/source.go` e `sqlite/rendermetrics/registry.go`
  (overlay cache già migrata).

### 07C — ollama metadata/scorer e semantic plan cache

- `MetadataGenerator` è in `internal/kernel/script`.
- `SemanticCandidateScorer` è in `internal/kernel/script/semantic`.
- `imagegate` è in `internal/kernel/script/imagegate`; il vecchio path
  `capabilities/scripts/adapters/imagegate` è un re-export.
- Policy, verifica byte e controlli di provenienza delle immagini sono in
  `internal/kernel/script/imageverify`; `ports.VerifiedArtifact` è un alias del
  tipo kernel.
- L'adapter Ollama non scarica più con la policy della capability: il
  downloader viene iniettato dal composition root
  (`NewOllamaPersonImageScorerWithDownloader`), mentre l'adapter possiede solo
  il giudizio VLM. Migrati `metadata_generator.go` e `person_image_scorer.go`.

## 3. Criteri di accettazione

```bash
cd refactored
# 1. inventario platform→scripts vuoto
python3 - <<'PY'
import re
s=open('cmd/archcheck/scan/boundaries/percheck_platform_port_boundary.go').read()
m=re.search(r'var platformCapabilityScriptsInventory = map\[string\]string\{(.*?)\n\}', s, re.S)
print(len(re.findall(r'"([^"]+)":', m.group(1))))
PY
# atteso: 0

# 2. nessun import business negli adapter platform
rg -n 'internal/capabilities/scripts"|internal/capabilities/scripts/adapters' \
  internal/platform --glob '!**/*_test.go'
# atteso: nessun import (i commenti non contano)

# 3. kernel foglia e gate
go list -deps ./internal/kernel/... | grep -E 'internal/(capabilities|platform)'
# atteso: nessun output
go run ./cmd/archcheck --strict   # exit 0, has_hard_gate_hits: false

# 4. suite
go build ./...
go vet ./internal/kernel/... ./internal/platform/ollama/... \
  ./internal/platform/sqlite/... ./internal/capabilities/scripts/...
go test ./internal/kernel/... ./internal/platform/ollama/... \
  ./internal/platform/sqlite/... ./internal/capabilities/scripts/... -count=1
```

## 4. Rischi e rollback

- **Rischio:** un row type ha tag JSON/SQL accoppiati allo schema. Mitigazione:
  tipi spostati verbatim, query rimaste negli adapter, test di round-trip e
  scan eseguiti.
- **Rischio:** un adapter usa una funzione, non un tipo. Mitigazione: funzioni
  pure nel kernel; I/O dietro porta o iniettato dal composition root.
- **Rischio:** perdita del downloader nello scorer. Mitigazione: il costruttore
  senza downloader fallisce chiuso (`retryable`), il wiring production usa
  `NewOllamaPersonImageScorerWithDownloader`.
- **Rollback:** cambiamento di import/package, nessuna migrazione dati.

## 5. Esito finale (2026-11-24)

| Check | Comando | Esito |
| --- | --- | --- |
| Inventario platform→scripts | parsing di `platformCapabilityScriptsInventory` | **0 entry** |
| Import business nei platform adapter | `rg` non-test | **NONE** |
| Kernel dipendente da capability/platform | `go list -deps ./internal/kernel/...` | **NONE** |
| Build | `go build ./...` | **rc 0** |
| Vet | `go vet` su kernel/platform/scripts | **rc 0** |
| Test | `go test` su kernel/platform/scripts/wiring/archcheck | **rc 0** |
| Gate architetturale | `go run ./cmd/archcheck --strict` | **rc 0**, `has_hard_gate_hits: false` |
| Formato | `gofmt -l` sui file toccati | nessun output |
| Diff | `git diff --check` | **rc 0** |

Dettaglio test eseguiti: `./internal/kernel/...`,
`./internal/platform/sqlite/...`, `./internal/platform/ollama/...`,
`./internal/capabilities/scripts/...`, `./internal/app/wiring`,
`./cmd/archcheck/...` — tutti PASS. La suite completa `go test ./...` non è
stata usata come gate; i package toccati e i loro consumatori sono verdi.

## 6. Note di chiusura

- Nessuna nuova eccezione al gate: l'unica superficie ammessa resta
  `internal/capabilities/scripts/ports` (incluso `ports/run`).
- `GenerateRequest`/`GenerateResult` restano DTO della capability; spostarli
  nel kernel è il refactor ampio tracciato da BON-01, non necessario per
  azzerare l'inventario platform.
- Nessun commit/push eseguito in questa sessione: il lavoro è nel working tree
  condiviso.

## 7. Cosa resta

Niente di bloccante: 07A/07B/07C chiusi e inventario platform a 0. Follow-up
separato, non necessario per questa chiusura: BON-01 per spostare
`GenerateRequest`/`GenerateResult` nel kernel (oggi vivono nella porta ammessa
`capabilities/scripts/ports/run`). Restano fuori perimetro push e suite completa
(`TestA1_ScaleBacklogInteractiveFirstClaimUnderBudget` flaky sotto carico).
