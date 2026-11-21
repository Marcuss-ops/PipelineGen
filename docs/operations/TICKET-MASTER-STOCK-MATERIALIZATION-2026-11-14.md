# TICKET — Materializzazione stock e profilo video lato Master

- **Data:** 2026-11-14
- **Stato:** BLOCKED-EXTERNAL (ricontrollato 2026-11-21) — owner Master 51 / stockpipeline; il codice non è in questo workspace. Confermato: il tree `refactored` contiene solo il lato Producer e i suoi test, nessuna materializzazione stock.
- **Contesto:** chiusura del confine Producer/Master. Il Producer non processa più stock: trasporta solo il binding della stockpipeline.
- **Contratto:** [`PRODUCER-MASTER-BOUNDARY.md`](PRODUCER-MASTER-BOUNDARY.md)
- **Storico:** `RenderingGen/docs/tickets/archive/TICKET-PRODUCER-MASTER-BOUNDARY-2026-11-04.md`

## Lato Producer — chiuso 2026-11-16

Il Producer ha chiuso la parte che gli compete: il piano final-job compilato
trasporta la finestra stock per scena (`sceneID -> ms`) e usa quella come
estensione della sorgente, invece di trattare l'intero file come render extent.
La finestra resta Producer-local e non viene inviata come asset al Master.

Evidenza locale (working tree `refactored`, non committato):

- `TestFinalJobCompiledPlanRendersOnlyTheBoundIntroWindow`: asset 60 s, scena
  5 s -> segmento `0 + 5.000.000 US`, 120 frame; il file resta esteso 60 s.
- `TestFinalJobCompiledPlanKeepsAWholeStockFileExact`: file 5 s -> una finestra
  intera esatta.
- `TestFinalJobCompiledPlanRejectsStockWindowPastSavedFile`: una finestra oltre
  i byte salvati fallisce chiusa.
- `go test ./internal/capabilities/scripts/...`, `make verify-agent` e
  `make verify-main`: PASS.

Questi test provano il confine Producer, **non** l'accettazione Master: i sei
criteri sotto restano del proprietario dei byte e dell'executor.

## Perché è separato

Il Producer non può correggere questi punti senza violare il confine: nessun
download/probe/taglio/ricodifica stock, nessun video finale locale. Vanno
risolti dal lato che possiede i byte e l'executor.

## Criteri di accettazione (Master/stockpipeline)

Stato: **tutti aperti**. Il lato Producer è chiuso sopra; questi criteri
richiedono una run cross-repo sul Master.

- [ ] Accettare o ricodificare una finestra stock parziale (r56/r58/r59c), con keyframe iniziale corretto.
- [ ] FINALIZE senza timeout 45 s / root 502 sui piani da 20 minuti (r59c).
- [ ] Restituire nativamente `invalid_compiled_render_plan` per un piano invalido, non `m2m_quota_failure`.
- [ ] Profilo H.264 canonico su muxer, trimmer, segment execution e cache stock (avcC, refs, B-frame, timescale 1/90000).
- [ ] Prefetch/source-host: accettare `source_uri` HTTPS e il mapping Drive senza allowlist implicita.
- [ ] Un job end-to-end reference-only (Garlasco 20 min) produce un MP4 con SHA-256 e decode ffprobe attraverso ogni giunzione.

## Non obiettivi lato Producer

- Selezionare file stock, comporre scene, allineare durate, tagliare o ricodificare stock.
- Produrre il video finale in locale.

## Evidenza

- `ops/jobs/garlasco_editorial_e2e_20261030/r56-...`, `r58-...`, `r59b-...`, `r59c-...`
- `RenderingGen/docs/evidence/architectural-audit-20261110/REPORT.md` (H.264 audit)
- `RenderingGen/docs/tickets/archive/TICKET-PRODUCER-MASTER-BOUNDARY-2026-11-04.md` (dettaglio storico)
