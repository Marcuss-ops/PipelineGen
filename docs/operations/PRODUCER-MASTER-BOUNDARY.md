# Producer / Master boundary — contratto corrente

- **Stato:** ATTIVO dal 2026-11-14.
- **Sostituisce:** `RenderingGen/docs/tickets/archive/TICKET-PRODUCER-MASTER-BOUNDARY-2026-11-04.md` (storico).
- **Owner del contratto:** PipelineGen (Producer 77). Master 51 e stockpipeline sono owner dei rispettivi lati.

## Regola

Il video finale è **sempre remoto**. Il Producer costruisce il piano immutabile e
trasporta i riferimenti; il Master prepara e assembla. **Lo stock non è del
Producer.** La stockpipeline seleziona, certifica e salva il file; il Producer
trasporta solo il binding (`asset_id`/`drive_link` + finestra
`start_ms`/`duration_ms`).

## Proprietà

| Area | Owner | Cosa possiede |
|---|---|---|
| Script, TTS, audio finale, overlay render, piano immutabile | PipelineGen (Producer 77) | Contenuto, piano, riferimenti |
| Selezione/acquisizione stock e metadata | stockpipeline | File Drive, durata, SHA, finestra |
| Download, probe, taglio, ricodifica, keyframe, prefetch, assembly | Master 51 / worker | Byte stock e film finale |
| Compilazione semantica overlay e render | RenderingGen + Chronon3d | Solo overlay; non partecipa all'assembly |

## Il Producer DEVE

- inviare un piano immutabile con riferimenti Drive (stock) e HTTPS/Objectstore
  (clip, overlay, audio);
- usare il binding stockpipeline così com'è, senza modificarlo;
- fallire subito se il binding è incompleto (solo cartella, senza asset concreto);
- validare solo la forma del riferimento e la finestra rispetto ai metadata
  salvati.

## Il Producer NON DEVE

- listare cartelle stock, scegliere file, comporre scene con file interi o
  parziali;
- scaricare, sondare, hashare, tagliare, ricodificare o caricare byte stock;
- allineare le durate delle scene al file stock;
- creare o concatenare il video finale in locale;
- reintrodurre planner stock, `sceneClips` stock o
  `alignFinalJobStockSceneDurations`.

## Gate nel repository

- `TestFinalJobProducerDoesNotOwnStockPlanning` (source gate);
- `TestBuildFinalJobPayloadsRejectsFolderOnlyStockBinding` (binding incompleto);
- `TestBuildFinalJobPayloadsCarriesStockBindingWindow` (pass-through, anche
  parziale);
- test di confine: nessun video finale locale, nessun path locale nel piano.

## Residui esterni

I difetti di materializzazione stock, keyframe, timeout FINALIZE, codice errore
nativo del Master e profilo H.264 sono tracciati in
[`TICKET-MASTER-STOCK-MATERIALIZATION-2026-11-14.md`](TICKET-MASTER-STOCK-MATERIALIZATION-2026-11-14.md).
Non sono criteri di chiusura del Producer.

## Chiusura

Il lato Producer è chiuso quando i gate sopra sono verdi e un handoff
reference-only arriva al Master senza byte stock locali. Il confine complessivo
si chiude solo quando il ticket Master/stockpipeline è verde su un job
end-to-end con artifact verificato (SHA-256 + decode ffprobe).
