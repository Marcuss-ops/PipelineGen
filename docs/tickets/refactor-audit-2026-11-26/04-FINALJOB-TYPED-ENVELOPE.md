# 04 — DEMOL-08: envelope FinalJob tipizzato

**Data:** 2026-11-27
**Priorità:** P1
**Stato:** **DONE**
**Area:** `internal/capabilities/scripts/finaljob`,
`internal/kernel/script/finaljob`

**Nota di governance.** `00-INDEX` §10.3 e la scheda 03 rimandavano a
`DEMOL-08`, ma nessun file di quel ticket è mai stato nell'albero: il
riferimento era pendente. Questa scheda diventa l'owner dell'accettazione e
chiude il residuo; la campagna `demolizione-architetturale-2026-11-26` non ha
altre schede nel repository e non viene inventata qui.

## 1. Problema misurato

`finalJobFinalizePayload` costruiva a mano `map[string]any` per le due
envelope. Le chiavi stabili erano stringhe letterali in un corpo di ~120 righe:

```text
PREPARE   idempotency_key, job_type, render_only, video_name, script_text,
          compiled_render_plan_json, compiled_render_plan_sha256
FINALIZE  idempotency_key, runtime_assets, runtime_payload
PAYLOAD   video_name, script_text, overlays, overlay_manifest, runtime_audio
```

Un rename o un refuso era un errore a runtime (422 del Master), non di
compilazione. Il contratto tipizzato esisteva già
(`kernel/script/finaljob/contract.go`) ma veniva usato solo per *rileggere* la
mappa, non per costruirla.

## 2. Cosa è cambiato

| Prima | Ora |
| --- | --- |
| `finalJobFinalizePayload` → `(map, map, error)` | `finalJobFinalizeEnvelope` → `(PrepareV1, FinalizeEnvelopeV1, error)` |
| 7 chiavi PREPARE scritte a mano | `finaljob.PrepareV1` |
| `runtime_payload` come mappa annidata | `finaljob.RuntimePayloadV1` + `RuntimeAudioV1` |
| `finalize["runtime_assets"]` come lista dinamica | invariata, ma dentro `FinalizeEnvelopeV1` |
| conversione mappa→DTO dentro `BuildEnvelope` | `MasterPayloadsOf` è l'unico punto mappa |

`FinalizeEnvelopeV1` è la shell tipizzata del FINALIZE. Differisce da
`FinalizeV1` in un solo campo: `RuntimeAssets []any`. La lista resta il
documento di trasporto perché ogni entry porta metadati
Master-forward-compatible (`RuntimeAssetV1.Additional`); tipizzarla
drops le chiavi opzionali vuote (`kind`, `url`, `source_uri`,
`drive_file_id`) e cambierebbe il wire. La vista tipizzata di ogni entry è
comunque prodotta da `BuildEnvelope`/`Validate` al confine.

`MasterPayloadsOf` è l'unico adattatore verso la mappa del Master deployed:
proietta i campi scalari tipizzati e passa per riferimento la lista asset e i
due documenti opachi (`overlays`, `overlay_manifest`). Nessun altro punto del
builder costruisce chiavi stabili.

Il percorso di produzione (`BuildFinalJobEnvelopeV1`) passa le envelope
tipizzate direttamente a `BuildEnvelope`: una sola serializzazione, come prima.
Il percorso split (`BuildFinalJobSourceHandoff`) continua a ricevere mappe e
paga la proiezione, che è il prezzo dichiarato dell'adapter.

## 3. Prova che il wire non si è mosso

Il golden è stato catturato **prima** del refactor, dal builder a mappe:

`internal/capabilities/scripts/testdata/final_job_master_wire.golden.json`

Il test `TestFinalJobMasterWireMatchesGolden` confronta i byte correnti con
quelli committati; `TestFinalJobEnvelopeV1PreservesCommittedMasterWireBytes`
confronta la proiezione tipizzata (`envelope.MasterPayloads()`) con lo stesso
golden. La vecchia parity test confrontava due chiamate dello stesso builder e
sarebbe diventata tautologica: ora il riferimento è esterno al codice.

## 4. Costo misurato

`BenchmarkFinalJobDemolition/PayloadBuild` (stesso host, `-benchtime=10x
-count=3 -benchmem`):

| Metrica | Baseline wave 2026-11-26 | Dopo DEMOL-08 | Delta |
| --- | ---: | ---: | ---: |
| allocs/op | 2.286 | **2.323** | +37 (**+1,6%**) |
| B/op | 193.417 | **226.150** | +32.733 (**+16,9%**) |
| ns/op | 1.526.541 (misura 2026-11-27, 10x) | 1.423.055 / 1.445.985 / 1.751.821 (10x) | nel rumore |

I byte extra sono la copia della stringa `compiled_render_plan_json` nella
proiezione PREPARE. Il percorso di produzione non la paga: `BuildEnvelope`
serializza la struct tipizzata una volta, come prima. Non è stato toccato
nessun loop e nessun budget CI.

## 5. Check eseguiti

```text
gofmt -l <file toccati>                                            → nessun output
go build ./internal/...                                            → exit 0
go test ./internal/kernel/script/finaljob ./internal/capabilities/scripts/finaljob \
        ./internal/app/modules/script ./internal/platform/media/finaljob -count=1 → ok
go test ./internal/capabilities/scripts -run 'FinalJob|MasterWire|EnvelopeV1' -count=1 → ok
make verify-architecture                                          → exit 0, has_hard_gate_hits=false
go run ./cmd/cyclo-budget -check                                   → 39/39
python3 scripts/regen_hotspots.py --check                          → fresh
go mod tidy -diff                                                  → exit 0 (go.mod/go.sum invariati)
```

`verify-main` **non** è verde su questo albero, per due cause fuori da questa
scheda (misurate il 2026-11-27): `verify-format` elenca
`internal/kernel/overlayplan/v2.go` non formattato (WIP non committato di un
altro attore) e la suite `internal/capabilities/scripts` fallisce sui 15 test
`TestCompileOverlayPlan*` / `TestOverlayPlanFixturesMatchRealRustCompiler`
dello stesso WIP. I test del final job sono verdi. Non è stato usato
`SKIP_FORMAT=1`: il gate resta rosso e viene riportato, non aggirato.

## 6. Acceptance

1. [x] Il builder non costruisce più chiavi stabili con `map[string]any`.
2. [x] Le chiavi stabili sono verificate dal compilatore
       (`PrepareV1`, `RuntimePayloadV1`, `RuntimeAudioV1`).
3. [x] Esiste un solo punto di conversione verso la mappa del Master
       (`MasterPayloadsOf`).
4. [x] Il wire PREPARE/FINALIZE è byte-identico al golden pre-refactor.
5. [x] Le chiavi sconosciute continuano a fallire closed
       (`DisallowUnknownFields` in `BuildEnvelope`).
6. [x] La lista `runtime_assets` resta dinamica con una motivazione scritta
       (metadati forward-compatible + split handoff).
7. [x] Nessuna soglia o baseline alzata.

## 7. Residui repo-wide osservati alla chiusura (fuori perimetro)

1. ~~`go run ./cmd/archcheck --strict` per `pkg_size` di `cliprender`~~:
   **risolto** il 2026-11-27 spostando ~1.000 righe in `cliprender/telemetry`
   e `cliprender/batch`; il package padre scende a **10.583** righe sul budget
   **10.629**. Evidenza in [05](05-CLIPRENDER-BUDGET-AND-DEADCODE.md).
2. ~~Dead code guard a 224/219~~: **risolto** il 2026-11-27 con lo sweep
   dell'orphan package `voiceover/verbatimcheck` e di due helper senza
   chiamanti. Misura: **214**; `MAX` abbassato 219 → 214, nessuna soglia
   alzata. Evidenza in [05](05-CLIPRENDER-BUDGET-AND-DEADCODE.md) §4.
3. Il WIP overlay-plan non committato descritto in §5.
