# TICKET-STATIC-OVERLAY-DURATION-INVARIANT (P1)

**Stato:** PARTIAL (25/10/2026) — percorso implementato e costo live promettente; gate pixel fallito, A/B completo da ripetere.
**Origine:** idee di paradigma di `TICKET-OVERLAY-THROUGHPUT-100-MIN.md`
(sezione «Idee di paradigma»), approvate dall'utente il 24/10/2026.
**Ordine consigliato:** dopo B2 (`Chronon3d/docs/tickets/TICKET-100X-PERF-WAVES.md`),
prima di P2.

## Principio

Il costo di render di un overlay statico è O(1) nella durata: se i pixel non
cambiano per l'intero item, il sistema renderizza una volta e replica, senza
pagare raster/composite/encode per ogni frame identico.

## Problema misurato

- Una phrase card statica da 30 s produce 900 frame identici che oggi
  vengono valutati/rasterizzati/compositi/encodati uno per uno: il servizio
  scala linearmente con la durata anche quando `template_reuses = frames - 1`.
- Nel golden v17 `text_phrase` è 20/40 item e **45,2% del servizio render**
  (avg 2,266 s su clip brevi); item più lunghi (card da 20–30 s, mappe,
  schede entità statiche) amplificano il costo lineare.
- La detection degli intervalli statici esiste già: `DirectYuvProgram` cachea
  il `DirectYuvTemplate` su $[cue\_from, cue\_until)$ (wave B1, LAND
  DirectYUV) — ma il loop emette comunque un frame renderizzato+encodato per
  ogni tick.

## Cambio

1. Riconoscere item statici per l'intera durata: composizione con
   `Animation == nil`, text/style/position costanti (stessa detection degli
   intervalli statici già usata da `DirectYuvProgram`).
2. Sul percorso statico: renderizzare il template **una volta**, poi replicare
   il risultato. Due varianti candidate, scelte dalla misura:
   - **(a) segmento encodato + stream-copy:** encodare il primo segmento
     statico (pochi secondi) e replicare i pacchetti con concat stream-copy
     fino alla durata richiesta; costo O(1) nella durata, struttura GOP
     ripetuta.
   - **(b) feed della stessa superficie all'encoder:** nessuna ri-composizione
     per frame, resta un encode per frame (costo lineare ridotto, non O(1)).
3. Parità pixel: contenuto statico ⇒ ogni frame decodificato deve essere
   identico al golden (stesso encoder, stessi parametri); la variante (a)
   cambia la struttura GOP dell'MP4 → **verificare prima il contratto di
   stitching del final worker (packet-copy)**.

## Gate di accettazione

- [ ] Phrase card da 30 s costa ≤ 1,2× la stessa card da 3 s (servizio render
      misurato su `render_attempt_analytics.render_ms`).
- [ ] Parità pixel con golden: frame decodificati confrontati, nessuna
      differenza accettata senza re-baseline dichiarato.
- [ ] Nessuna regressione sui golden test esistenti né sul contratto media
      (dimensions/codec/duration esatti).

## Dipendenze e rischi

- Detection statica: già disponibile (B1).
- Variante (a): dipende dal contratto con il final worker (packet-copy stitch)
  — negoziare prima di adottare.
- Interazione con `TextAnimators`/word-reveal: restano sul percorso per-frame
  (genuinamente animati), come già previsto dalla wave B1.

### Verifica closeout 2026-10-25

- Build e regressione C++ mirata PASS; il test encoder conferma che il riuso
  dopo finalizzazione MP4 resta disabilitato (EOF dal codec).
- A/B live primo paio: 3 s `render_ms=7505`, 30 s `render_ms=4677` (rapporto
  0,623), ma fallisce il contratto pixel: il frame decodificato 1 differisce
  dal frame 0. La prova si è interrotta prima delle tre repliche, quindi il
  rapporto non è un gate statistico superato. Serve chiarire se l'output ha
  animazione implicita oppure se la replica del packet produce drift visivo,
  poi ripetere il gate completo.
