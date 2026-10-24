# TICKET-MANIFEST-IDENTITY (P5)

**Stato:** APPROVATO 24/10/2026 — primo step = A/B sul payload immutabile;
demolizione del percorso MP4-per-item solo dopo negoziazione del contratto.
**Origine:** idee di paradigma di `TICKET-OVERLAY-THROUGHPUT-100-MIN.md`
(candidato architetturale «overlay senza MP4 per item»), approvate
dall'utente il 24/10/2026. Versione estrema del paradigma #5.
**Ordine consigliato:** ultimo della serie P: cambia il modello produttivo.

## Principio

Identità per manifest, non per file: un overlay non è un MP4 proprio ma una
voce di manifest — `item_id → (track, in, out, content-hash)` — dentro un
unico artefatto certificato per timeline. Retry idempotente per voce di
manifest; la finestra «overlay» come fase separata smette di esistere, resta
solo il render della timeline.

## Problema misurato

Oggi ogni item paga: mux MP4, receipt, hash, link Drive proprio
(**2,4–3,0 s/item di `drive_publish` asincrono**, 36–36 upload per run,
98–139 s somma) più lo stitching nel final worker. Con P5 questi costi
diventano una riga di manifest per item e un solo artefatto certificato.

## Cambio (fasi)

1. **A/B sul payload immutabile:** stesso piano materializzato renderizzato
   (a) col percorso MP4-per-item corrente e (b) come manifest + timeline
   unica. Confronto: parità pixel/audio, ordine item, retry idempotenti.
2. Solo dopo parità: negoziare col final worker (oggi remoto, packet-copy)
   il contratto manifest e il requisito di link Drive per item.
3. Demolizione del percorso MP4-per-item (mux/receipt/publish/stitch per
   item) a parità certificata.

## Gate di accettazione

- [ ] A/B: parità pixel/audio tra i due percorsi su piano golden; nessuna
      differenza accettata senza re-baseline dichiarato.
- [ ] Retry idempotente per voce di manifest: stesso `item_id` + stesso
      content-hash ⇒ nessun lavoro ripetuto, nessun artefatto duplicato.
- [ ] Contratto col final worker rinegoziato e scritto prima della
      demolizione; requisito Drive-per-item esplicitamente sostituito o
      preservato come decisione documentata.

## Dipendenze e rischi

- Contratto esterno: il final worker è remoto e fa packet-copy stitching;
  il cambio di formato è cross-team.
- P1 (durata invariant) e P4 (batch) diventano naturali su questo modello:
  una card statica è una riga di manifest con una texture.
- Non partire con la demolizione prima che l'A/B sia verde: è la ragione
  per cui la voce è P5 e non P1.
