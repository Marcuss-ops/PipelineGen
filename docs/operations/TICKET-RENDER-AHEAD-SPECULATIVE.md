# TICKET-RENDER-AHEAD-SPECULATIVE (P3)

**Stato:** PREREQUISITO OSSERVABILITÀ PRESENTE / P3 NON IMPLEMENTATO (25/10/2026). Le trace per-scena e i timestamp di streaming sono implementati e testati localmente; scheduler speculative, commit per hash e overlap live ≥50% non sono dimostrati.
**Origine:** idee di paradigma di `TICKET-OVERLAY-THROUGHPUT-100-MIN.md`,
approvate dall'utente il 24/10/2026.
**Ordine consigliato:** dopo la misura golden live post-P2.

## Stato corrente verificato

- L'osservabilità per-scena esiste nel working tree: `SceneTimeline` registra
  lifecycle/critical path; `Scene` conserva `text_ready_at`, start translation
  e TTS, e la coordinazione per scena avvia translation/TTS prima che la scena
  successiva sia pronta. Test locali: `runner_scene_overlap_timestamp_test.go`
  e `scene_trace_contract_test.go`.
- Questo copre il prerequisito timestamp/streaming di alcune fasi; **non** prova
  che l'overlay venga schedulato prima del piano completo, né che esista il
  commit speculativo per hash.
- Non è stata registrata una trace live che misuri l'overlap generate×render.
  Il gate P3 resta aperto e nessun guadagno wall-time va attribuito a P3.

## Principio

Render-ahead speculativo con commit per hash: non appena un item ha tutti gli
input stabili (testo, asset, lingua), il render parte **in parallelo alla
generazione delle scene successive**; il risultato è committato solo se il
content-hash degli input non è cambiato, altrimenti è scartato senza retry.

## Problema misurato

Nel golden v6 le fasi sono somme di barriere globali: `generate` 136 s,
`scene_analysis` 91 s (work 281 s), `voiceover` 57 s, `audio_compile` 128 s,
overlay 128 s — gran parte del wall è attesa di barriere evitabili: gli item
pronti aspettano l'intero piano prima di partire. La proiezione della
dataflow-streaming (paradigma #4): wall da somma(fasi) a max(fasi)
(~7 min → ~2,5–3 min).

## Cambio

1. Eventi temporali per scena e per item: `ready_at`, `enqueued_at`,
   `started_at`, `completed_at`, predecessori richiesti, work effettivo
   (già richiesto dall'audit end-to-end nel ticket throughput).
2. Scheduler dataflow: un item parte quando i *suoi* predecessori sono pronti,
   non quando l'intero piano è congelato.
3. Commit idempotente per content-hash: artefatto = f(input hash); un input
   speculativo cambiato scarta il risultato e rilancia una sola volta.

## Gate di accettazione

- [ ] Overlap generate×render ≥ 50% su trace live, misurato come intersezione
      degli intervalli attivi. La prova locale dimostra solo overlap
      generazione-scena×translation/TTS, non generate×render.
- [ ] Scheduler speculative e commit per hash: nessun artefatto committato
      da input cambiati; stesso input hash ⇒ stesso output hash. NOT IMPLEMENTED /
      NOT RUN.
- [ ] Critical path calcolato da una trace end-to-end completa, classificando
      ogni intervallo come lavoro utile, coda, polling/sleep, serializzazione,
      upload o lavoro ripetuto. Implementazione della timeline presente;
      criterio end-to-end NOT RUN.

## Dipendenze e rischi

- Prerequisito parzialmente disponibile: timestamp di testo, traduzione,
  NLP, TTS, asset e audio sono rappresentabili/registrati localmente; copertura
  live end-to-end e intervalli specifici di enqueue/start render vanno ancora
  verificati prima di avviare lo scheduler speculative.
- Il commit speculativo non deve sporcare l'ObjectStore di artefatti
  scartati: staging + rename atomico per hash.

### Verifica locale mirata — 25/10/2026

- PASS: `TestSceneTraceContract_RealSeamsFeedTheTimelineAndCriticalPath` e
  `TestSceneTextReady_TranslatedNLPOverlapsTTS` nella selezione mirata
  `go test ./internal/capabilities/scripts -run '^(... )$' -count=1`.
  Questo certifica il contratto locale trace/overlap scene-text, non
  generate×render.
- FAIL separato: la suite larga scripts+overlays+observability fallisce su
  regressioni di fixture/registry non relative al gate P3. Il risultato mirato
  resta PASS limitato ai due test nominati.
- NOT IMPLEMENTED / NOT RUN: nessun scheduler render-ahead speculativo, commit
  idempotente per hash, trace live generate×render ≥50% o critical path
  end-to-end completo è stato aggiunto/eseguito in questo checkpoint.
