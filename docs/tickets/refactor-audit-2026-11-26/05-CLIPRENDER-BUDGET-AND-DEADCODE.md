# 05 — Budget del package cliprender e sweep del dead code

**Data:** 2026-11-27
**Priorità:** P2
**Stato:** **DONE** (due gate esterni riportati in §5)
**Area:** `internal/capabilities/cliprender`,
`internal/capabilities/voiceover/verbatimcheck`, `cmd/gen_baseline`,
`internal/kernel/script`

## 1. Problema misurato

`go run ./cmd/archcheck --strict` usciva 1 su `pkg_size`:

```text
package internal/capabilities/cliprender
  actual_lines: 11600   max_lines: 10629
  owner: "cliprender capability"  deadline: 2027-03-31
```

Il budget non è stato alzato: ~1.000 righe sono state spostate in due
sottopackage foglia. In parallelo il dead-code guard era a **224** funzioni
irraggiungibili contro il massimo **219** (`MAX`), quindi il ratchet era rosso
anche lì.

## 2. Split di cliprender

Due sottopackage, scelti per cluster foglia (nessuna dipendenza dal package
padre, quindi nessun ciclo):

| Sottopackage | File spostati | Righe |
| --- | --- | ---: |
| `cliprender/telemetry` | `metrics.go` (invariato) | 455 |
| | `chronon_sidecar.go` → `sidecar.go` | 150 |
| | `chronon_metrics_adapter.go` → `metrics_adapter.go` | 244 |
| | `stage_timing.go` → `stages.go` | 56 |
| `cliprender/batch` | `aggregator.go` | 183 |
| | `aggregator_test.go`, `aggregator_finalize_test.go`, `aggregator_testhelpers_test.go` | — |

`metrics`, `sidecar`, `metrics_adapter` e `stages` sono infrastruttura senza
stato del worker. L'aggregator possiede la macchina a stati della continuation
del padre: i suoi test usano il campo non esportato `agg.interval`, quindi sono
stati spostati con il codice invece di essere riscritti attraverso una API di
test.

**API invariata.** `aliases_subpackages.go` nel package padre ri-esporta i
simboli pubblici (`RenderMetricsV2`, `NewRenderMetricsV2`, i tipi del sidecar e
delle metriche); `ParentStateWaitingChildren` è ridefinito in `batch` e
ri-esportato dal padre; `job.TypeClipRender` sostituisce la costante locale in
`batch`; `RenderBackend` è un alias al tipo di `kernel/media` in `telemetry`.

**Risultato:** il package padre scende a **10.583** righe non-test su **47**
file, contro il budget **10.629** (headroom 46). Il totale dei sottopackage è
905 + 183 righe di produzione, più i test dell'aggregator.

## 3. Verifiche del split

| Comando | Esito |
| --- | --- |
| `go build ./...` | exit 0 |
| `go test ./internal/capabilities/cliprender/...` | ok (padre, `adapters`, `batch`) |
| `go run ./cmd/archcheck --strict` (subito dopo lo split) | **exit 0**, `passed=true`, `has_hard_gate_hits=false` |
| `make verify-architecture` | exit 0 |
| `go run ./cmd/cyclo-budget -check` | exit 0 (37 funzioni > 40, 39 entry di baseline) |

L'unico warning residuo di `--strict` è `max_lines_per_file_strict` su
`internal/capabilities/scripts/geography/enrichment.go` (811/800), fuori dal
perimetro di questa scheda e non fatale.

## 4. Sweep del dead code

Prima: **224** funzioni irraggiungibili. Dopo: **214**.

| Rimosso | Funzioni | Prova |
| --- | ---: | --- |
| package `internal/capabilities/voiceover/verbatimcheck` (codice + test) | 8 | `Report.OK`, `Report.Summary`, `ParseVTT`, `parseVTTTime`, `Check`, `firstMarkdownMarker`, `round2`, `clamp`; `rg "verbatimcheck"` non trova importatori fuori dal package; nessuna voce in `architecture/capability_inventory.yaml` |
| `objectKey` (`cmd/gen_baseline/schema.go`) | 1 | `rg -w --count-matches objectKey` = 1 (solo la definizione) |
| `SupportedEnvelopeVersions` (`internal/kernel/script/contract_version.go`) | 1 | nessun chiamante; il gate usato resta `IsSupportedEnvelopeVersion` |

`MAX` è stato abbassato **219 → 214** nello stesso cambio, come richiede la
policy del guard ("it may only go DOWN"), e il commento di testa del guard
registra lo sweep. Nessuna soglia alzata.

Le altre funzioni irraggiungibili con pochi riferimenti non sono state toccate:
`SeededIndexFromText` vive in un file non tracciato di un altro attore (il
guard non lo conta), mentre `SettleDeferralStats`/`WithSettleRetry` sono
esercitati da `settle_deferral_test.go` e toglierli richiederebbe di decidere
se l'opzione di retry è ancora parte del contratto del worker.

## 5. Residui esterni osservati alla chiusura

1. `archcheck --strict` è di nuovo rosso **solo** su
   `internal/capabilities/scripts`: 63 file contro 62 e 18.786 righe contro
   18.696. La crescita è il file non tracciato
   `internal/capabilities/scripts/runner_clip_audio_latch.go` di un altro
   attore; `cliprender` non compare più tra le violazioni.
2. `python3 scripts/regen_hotspots.py --check` è rosso per la stessa ragione
   (`internal/capabilities/scripts`: 62→63 file).
3. Due test di cache hit di `cliprender` (`TestWorker_CacheHitReusesCertifiedRenderWithoutPublishing`,
   `TestWorker_CacheHitSkipsDestinationResolution`) falliscono per la modifica
   non committata di un altro attore a `cliprender/input_digest.go`: un record
   senza `InputSHA256` ora è un miss fail-closed, mentre il test ne semina uno
   legacy. Su HEAD pulito (worktree detached) i due test passano; il resto
   della suite e i package spostati sono verdi.
4. `make verify-main` resta bloccato dal WIP overlay-plan (format di
   `internal/kernel/overlayplan/v2.go` e 15 test `TestCompileOverlayPlan*`),
   come già riportato in [04](04-FINALJOB-TYPED-ENVELOPE.md) §5.

## 6. Acceptance

1. [x] `cliprender` sotto il budget `pkg_size` senza alzarlo (10.583 / 10.629).
2. [x] API pubblica del package padre invariata tramite alias.
3. [x] Dead-code guard verde: 214 / 214.
4. [x] `MAX` abbassato nella stessa modifica, nessuna soglia alzata.
5. [x] Build, test dei package toccati, vet e `cyclo-budget` verdi.
