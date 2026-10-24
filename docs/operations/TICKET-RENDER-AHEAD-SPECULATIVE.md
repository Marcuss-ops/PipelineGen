# TICKET-RENDER-AHEAD-SPECULATIVE (P3)

**Stato:** APPROVATO 24/10/2026 — da implementare (dipende da prerequisito).
**Origine:** idee di paradigma di `TICKET-OVERLAY-THROUGHPUT-100-MIN.md`,
approvate dall'utente il 24/10/2026.
**Ordine consigliato:** dopo la misura golden live post-P2.

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

- [ ] Overlap generate×render ≥ 50% nelle trace per-scena (misurato come
      intersezione degli intervalli attivi, non somma di durate).
- [ ] Nessun artefatto committato da input cambiati (idempotenza per hash
      verificata: stesso input hash ⇒ stesso output hash).
- [ ] Critical path calcolato dalla traccia: ogni secondo classificato come
      lavoro utile, coda, polling/sleep, serializzazione, upload o lavoro
      ripetuto.

## Dipendenze e rischi

- Prerequisito: gli eventi per-scena (`ready_at/started_at/completed_at`).
- Il commit speculativo non deve sporcare l'ObjectStore di artefatti
  scartati: staging + rename atomico per hash.
