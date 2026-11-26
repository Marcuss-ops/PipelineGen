# 03 — FinalJob envelope: stato del tipizzato

**Data:** 2026-11-26
**Priorità:** P1 (verifica, nessuna modifica)
**Stato:** **VERIFICATO** — residuo tracciato in `DEMOL-08`
**Area:** `internal/capabilities/scripts/finaljob`, `internal/kernel/script/finaljob`

## 1. Cosa è tipizzato (verificato nel codice)

Il percorso di produzione è:

```text
internal/app/modules/script/remote_final_job.go
  → scriptgen.BuildFinalJobEnvelopeV1
      → buildFinalJobPayloads            (map[string]any)
      → finaljob.BuildEnvelope           (DecodeDTO con DisallowUnknownFields)
  → EnvelopeV1{Prepare, Finalize, Plan}
  → envelope.MasterPayloads()            (UNICO punto di serializzazione)
```

`internal/kernel/script/finaljob/contract.go` è l'owner del contratto e contiene
DTO tipizzati per le parti stabili:

- `PrepareV1` (idempotency_key, job_type, render_only, video_name, script_text,
  compiled_render_plan_json/sha256, placement pin);
- `FinalizeV1` + `RuntimePayloadV1` + `RuntimeAudioV1`;
- `CompiledPlanV2` con `PlanOutput`, `PlanAudio`, `PlanVideoTrack`,
  `PlanSegment`, `PlanAsset`, `PlanStockPool`.

`BuildEnvelope` decodifica con `DisallowUnknownFields` e `UseNumber`, poi
`Validate()` verifica schema, chiavi richieste, uguaglianza byte-per-byte del
plan con il PREPARE e il digest SHA-256. Una chiave sbagliata o mancante non può
attraversare il confine in silenzio: fallisce.

## 2. Cosa non è tipizzato

`finalJobFinalizePayload` costruisce ancora `map[string]any` per le due
envelope. È il residuo che `DEMOL-08` ("Final job 100% tipizzato su
`*CompiledPlanV2` con una sola serializzazione JSON") tiene aperto come
`PARTIAL`.

La parte dinamica (`overlays`, `overlay_manifest`, `runtime_payload`,
`RuntimeAssetV1.Additional`) resta legittimamente dinamica: è esattamente ciò
che l'audit concede ("lasciando l'eventuale conversione dinamica esclusivamente
al confine di serializzazione"). Il problema residuo è la costruzione delle
chiavi stabili via mappa, non la presenza di campi dinamici.

## 3. Perché non è stato riscritto qui

Riscrivere il builder per costruire direttamente `PrepareV1`/`FinalizeV1`
significa:

- cambiare la firma di `finalJobFinalizePayload`, di `buildFinalJobPayloads` e
  dell'API di test `BuildFinalJobPayloads`;
- adattare `final_job_split_handoff.go` e `remote_final_job_split.go`, che
  leggono `prepare["compiled_render_plan_sha256"]`;
- riscrivere le asserzioni di ~25 test che oggi leggono le mappe.

È un cambio sul critical path di consegna del final job, con un rischio
superiore al beneficio finché `DecodeDTO` + `Validate` fanno già fallire closed
le deriva. La decisione è quindi: il residuo resta di `DEMOL-08`, che ha il
perimetro e l'accettazione; questa scheda ne fissa lo stato misurato.

## 4. Acceptance

1. [x] Il contratto wire ha un owner tipizzato unico
       (`kernel/script/finaljob/contract.go`).
2. [x] Il builder non può emettere chiavi sconosciute o incomplete senza un
       errore (`DisallowUnknownFields` + `Validate`).
3. [x] Esiste un solo punto di conversione verso il Master
       (`EnvelopeV1.MasterPayloads`).
4. [x] Il residuo `map[string]any` nella costruzione è tracciato in `DEMOL-08`
       e non viene dichiarato chiuso.
