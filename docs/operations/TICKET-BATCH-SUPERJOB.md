# TICKET-BATCH-SUPERJOB (P4)

**Stato:** APPROVATO 24/10/2026 — da implementare.
**Origine:** idee di paradigma di `TICKET-OVERLAY-THROUGHPUT-100-MIN.md`
(paradigma #1 «un piano = una sessione render»), approvate dall'utente il
24/10/2026. Complementare a `TICKET-RESIDENT-RENDER-ENGINE` (P2).
**Ordine consigliato:** dopo P3; in alternativa, è la leva che chiude il
gate 100/min sulle sole 2 lane certificate se P2 da solo non basta.

## Principio

Un piano = una sessione render: gli item di un piano (o di una scena) sono
**segmenti di una sola sessione** — uno startup, una sessione encoder, un
ciclo di receipt per batch — invece di un job per item. Il costo fisso è
pagato una volta per batch, non una volta per artefatto.

## Problema misurato

- Servizio medio per item v17: **2,509 s** (p50 2,309, max 5,362) contro il
  budget di gate ≤ 1,2 s (60 s × 2 lane / 100 item). Manca un fattore ~2,1.
- Overhead fisso per item ~1,3 s (startup 186 + prepare non-asset + encoder
  open/close 259 + receipt/publish): su clip brevi è la maggioranza del
  servizio.
- Proiezione batch: servizio/item → render loop + marginali (~1,1 s) ⇒
  **~110 item/min con le sole 2 lane già certificate**.

## Cambio

1. Il worker RenderingGen presenta N item come un piano-batch; il daemon
   Chronon apre una sessione (device, pipeline, encoder) e produce N
   segmenti MP4 (uno per item, contratto preservato).
2. Un ciclo di verifica per batch: probe/digest per segmento, receipt per
   batch + voci per segmento.
3. Retry idempotente per segmento: un segmento fallito non invalida la
   sessione né gli altri segmenti (checkpoint per segmento).

## Gate di accettazione

- [ ] Costo/video su batch da 10 ≤ 40% del costo del video singolo
      (servizio totale batch / 10 vs servizio item singolo, stesso piano).
- [ ] Servizio medio per item ≤ 1,1 s su piano golden (mix dichiarato).
- [ ] Parità pixel/audio per segmento contro gli output delle run di
      riferimento; nessun cambio di ordine/contenuto/layout
      (`TestSeparateOverlayItemsPreservePlanOrderAndIndices`).
- [ ] Retry per segmento: segmento fallito → rilancio singolo senza
      ripetere gli altri.

## Dipendenze e rischi

- Il contratto MP4-per-item con il final worker è preservato (ogni segmento
  resta un artefatto proprio); il cambiamento è solo nel ciclo di vita della
  sessione render.
- La certificazione VRAM copre oggi 2 daemon: il batching riduce la
  concorrenza richiesta, non la aumenta.
- `drive_publish` per item resta (2,4–3,0 s/item asincroni, fuori finestra):
  è il bersaglio di P5, non di questo ticket.
