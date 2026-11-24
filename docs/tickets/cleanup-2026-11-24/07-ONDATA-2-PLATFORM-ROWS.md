# CLEANUP-07 — Ondata 2C: 2 `ollama` + 9 `sqlite` con row types nel kernel

**Data:** 2026-11-24
**Priorità:** P1 (chiude le 24 entry platform→scripts)
**Stato:** OPEN
**Owner:** `ollama-adapters` + `sqlite-*` + `kernel`
**Origine:** radiografia 2026-11-24, Ondata 2; inventario RA-01
**Base:** `refactored` @ `3e0a9e3ae`

## 1. Problema

Oltre ai 13 `rustexec` di CLEANUP-05, l'inventario
`platformCapabilityScriptsInventory` contiene **11 file** che importano
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

Dopo CLEANUP-05 (13 file) e questo ticket (11 file), l'inventario
`platformCapabilityScriptsInventory` deve essere **vuoto**.

## 2. Soluzione

1. **Censimento per file** dei simboli `scriptgen.*` (o import non
   qualificato). Separare i DTO di riga dai tipi di dominio già nel kernel.
2. **Spostare i row type** nel kernel più vicino al dominio:
   - `internal/kernel/script` per run/step/checkpoint, history, cache key;
   - `internal/kernel/media` per render metrics/artifact;
   - un package kernel dedicato solo se il tipo è davvero condiviso.
   Regola: il kernel non importa `internal/capabilities/**` né
   `internal/platform/**`.
3. **Alias temporanei** in `internal/capabilities/scripts` per i chiamanti
   ancora nel root; rimuoverli quando gli adapter importano il kernel.
4. **Aggiornare gli adapter** `sqlite/*` e `ollama/adapters/*` per importare
   solo il kernel. Un adapter non deve importare una capability per un DTO.
5. **Cancellare le entry** `platformCapabilityScriptsInventory` nello stesso
   cambio che migra ciascun file; il gate `stale` lo impone.
6. **Non allargare** le eccezioni del gate: resta solo
   `internal/capabilities/scripts/ports`.

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

# 2. nessun import business negli 11 adapter
rg -n 'internal/capabilities/scripts' \
  internal/platform/ollama/adapters/metadata_generator.go \
  internal/platform/ollama/adapters/person_image_scorer.go \
  internal/platform/sqlite/history/reader.go \
  internal/platform/sqlite/jobregistry/execution_recorder.go \
  internal/platform/sqlite/overlayrendercache/cache.go \
  internal/platform/sqlite/performance/source.go \
  internal/platform/sqlite/rendermetrics/registry.go \
  internal/platform/sqlite/scripts/artifact_checkpoint_repository.go \
  internal/platform/sqlite/scripts/run_reconciliation.go \
  internal/platform/sqlite/scripts/run_repository.go \
  internal/platform/sqlite/scripts/semantic_plan_cache_adapter.go
# atteso: nessun output

# 3. kernel foglia e gate
go list -deps ./internal/kernel/... | grep -E 'internal/(capabilities|platform)'
# atteso: nessun output
go run ./cmd/archcheck --strict   # exit 0, has_hard_gate_hits: false

# 4. suite
go build ./...
go vet ./internal/platform/ollama/... ./internal/platform/sqlite/... ./internal/kernel/...
go test ./internal/platform/ollama/... ./internal/platform/sqlite/... \
  ./internal/kernel/... ./internal/capabilities/scripts/... -count=1
```

## 4. Rischi e rollback

- **Rischio:** un row type ha tag JSON/SQL accoppiati allo schema. Mitigazione:
  spostare il tipo verbatim e lasciare le query nell'adapter; test di
  round-trip e scan.
- **Rischio:** un adapter usa una funzione, non un tipo. Mitigazione: la
  funzione pura va nel kernel; quella con I/O resta dietro una porta.
- **Rollback:** cambiamento di import/package, nessuna migrazione dati.
- **Regola di chiusura:** CLEANUP-07 si chiude solo con l'inventario a zero
  insieme a CLEANUP-05; nessuna chiusura parziale.
