# TICKET — Materializzazione stock e profilo video lato Master

- **Data:** 2026-11-14
- **Stato:** BLOCKED-EXTERNAL — owner Master 51 / stockpipeline; il codice non è in questo workspace.
- **Contesto:** chiusura del confine Producer/Master. Il Producer non processa più stock: trasporta solo il binding della stockpipeline.
- **Contratto:** [`PRODUCER-MASTER-BOUNDARY.md`](PRODUCER-MASTER-BOUNDARY.md)
- **Storico:** `RenderingGen/docs/tickets/archive/TICKET-PRODUCER-MASTER-BOUNDARY-2026-11-04.md`

## Perché è separato

Il Producer non può correggere questi punti senza violare il confine: nessun
download/probe/taglio/ricodifica stock, nessun video finale locale. Vanno
risolti dal lato che possiede i byte e l'executor.

## Criteri di accettazione (Master/stockpipeline)

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
