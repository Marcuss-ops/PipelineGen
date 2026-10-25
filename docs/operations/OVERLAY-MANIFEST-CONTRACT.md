# Contratto manifest overlay col final worker (P5, Opzione B)

Stato: **proposta scritta, in attesa di negoziazione cross-team** (con il worker
finale / Master 51, `BrainRust/pipelinegen-muscles/src/transform_assemble.rs`).
Fino a quando la negoziazione non è esplicitamente conclusa, il perimetro
operativo del final job resta quello di oggi: vedi §4.

Riferimenti: `docs/operations/TICKET-MANIFEST-IDENTITY.md`,
`docs/operations/TICKET-OVERLAY-THROUGHPUT-100-MIN.md`,
`internal/capabilities/overlays/model/manifest.go`.

## 1. Il modello: una voce di manifest, non un MP4 per item

Un overlay non è un file proprio: è una **voce di manifest** dentro un unico
artefatto certificato per timeline.

Schema wire `pipelinegen.overlay-manifest.v1` (tipizzato in
`internal/capabilities/overlays/model/manifest.go`, trasportato su
`RenderReference.Manifest`):

```
item_id   →  ( track, in_us, out_us, content_hash )
```

- `item_id`: identità semantica stabile dell'overlay (la stessa dell'item di
  piano, invariata tra rigenerazioni dello stesso slot).
- `track`: layer di compositing (z-order). Un item disegna **sopra ogni item
  precedente con cui si sovrappone** (track = 1 + max(track dei precedenti
  sovrapposti)); l'ordine di piano sopravvive alla proiezione.
- `in_us` / `out_us`: posizione sulla timeline con l'orologio canonico a
  microsecondi interi del piano (`in_us` incluso, `out_us` escluso,
  `out_us = in_us + duration_us` dell'item). Sono lo stesso segmento che
  l'handoff finale proietta sui frame (§3).
- `content_hash`: identità content-addressed del contenuto renderizzato della
  voce — il render key dell'item calcolato sul child plan che il percorso
  esegue davvero (64 hex minuscoli). Copre testo, asset, stili, timing
  relativo, canvas; NON copre il posizionamento (spostare una finestra è
  metadata, non contenuto nuovo).

Il documento porta anche `plan_id`, `video_id` e `timeline_sha256` (identità
dell'artefatto certificato, valorizzata a certificazione avvenuta).

## 2. Identità e retry

Due identità, distinte per costruzione:

- **Identità di composizione** — `OverlayManifest.Digest()`: SHA-256 sulle voci
  canoniche (item_id + content_hash + placement, ordine canonico) + identità di
  piano. Esclude `timeline_sha256`, quindi è stabile prima e dopo la
  certificazione. È l'indirizzo di contenuto dell'artefatto timeline: la cache
  content-addressed del percorso manifest+timeline è cablata su questo digest.
- **Identità di voce** — `OverlayManifestEntry.RetryKey()`: SHA-256 su
  (item_id + content_hash). La stessa coppia ⇒ la stessa identità di lavoro.

Contratto di retry (gate 2 del ticket):

- Retry dello stesso manifest ⇒ stessa riga di cache ⇒ **nessun lavoro
  ripetuto, nessun artefatto duplicato** (lo Store della cache è idempotente
  sul digest della chiave).
- Una voce cambia contenuto ⇒ cambia `content_hash` ⇒ cambia il digest del
  manifest ⇒ esattamente un nuovo render.
- Spostare una finestra senza cambiare contenuto non cambia l'identità della
  voce (retry per voce), ma cambia correttamente l'identità di composizione
  (la posizione fa parte dei pixel dell'artefatto).
- Sul percorso per-item, la classificazione per voce
  (`classifyManifestEntries`) riusa le righe content-addressed esistenti
  (stesso item_id + stesso content_hash ⇒ clip certificata riusata, nessun
  secondo artefatto).

## 3. Proiezione sui frame (griglia condivisa)

La griglia è quella già usata dall'handoff finale
(`finalJobOverlayAssetsExcluding` in `internal/capabilities/scripts/overlay_publication.go`),
con `frame_denominator = 1_000_000 * fps_den`:

```
start_frame = ceil(in_us  * fps_num / frame_denominator)
end_frame   = ceil(out_us * fps_num / frame_denominator)      (esclusivo)
```

Vincoli già sanzionati dall'ammissione finale e riconosciuti da questo
contratto: la finestra non può eccedere i frame certificati dell'artefatto
(clamp a `artifact.frame_count`), non può iniziare dopo la fine della base
certificata, e due finestre final-job non possono condividere nemmeno un frame
(mode=replace). Il manifest conserva il segmento in µs; la proiezione è
l'unica mappa sui frame.

## 4. Decisione documentata: requisito Drive-per-item

**Il requisito Drive-per-item è PRESERVATO.**

Il final worker di oggi consuma un artefatto Drive per overlay (i Drive ID sono
embebdi nel PREPARE). Il percorso manifest+timeline produce un artefatto
certificato + manifest e **non** sostituisce quell'handoff: i piani final-job
(`require_drive_before_return`, `source`) non eleggono mai il percorso manifest
(`manifestTimelineEligible`) e restano sul percorso per-item, che continua a
pubblicare per item.

La sostituzione (un artefatto per timeline + manifest al posto di N artefatti
Drive) richiede la rinegoziazione esplicita di questo contratto col worker
finale: è la fase 2 del ticket P5 e NON è conclusa. La demolizione del percorso
per-item è subordinata a (a) parità A/B pixel/audio certificata su piano golden
e (b) questo contratto rinegoziato — in questo checkout resta quindi intatta,
come da portata del ticket.

## 5. Handoff fail-closed invariato

Nessuna invariante dell'handoff finale cambia con questo contratto:

- un piano che richiede consegna Drive senza publisher configurato fallisce
  (mai silenzioso): `render plan requires Drive delivery but no overlay
  publisher is configured`;
- un artefatto mancante, non Drive, senza spazio locale o senza frame
  certificati fallisce l'intero handoff (`finalJobOverlayAssetsExcluding`);
- il percorso manifest+timeline non tocca il final job: la sua eleggibilità
  esclude esplicitamente `require_drive_before_return` e i compositi con
  `source`.

## 6. Wire tipizzato

- Modello: `internal/capabilities/overlays/model/manifest.go`
  (`OverlayManifest`, `OverlayManifestEntry`, `SchemaVersionManifest`).
- Trasporto: `RenderReference.Manifest`
  (`internal/capabilities/scripts/model_result.go`), valorizzato solo dal
  percorso manifest+timeline; le righe `Items`/`Batches` dello stesso
  reference si legano al digest del manifest, che funge da receipt.

## 7. Prova nel repository

- `internal/capabilities/overlays/model/manifest_test.go` — validazione,
  determinismo della serializzazione, identità per voce e di composizione.
- `internal/capabilities/scripts/overlay_manifest_identity_test.go` — retry
  per voce su cache content-addressed (nessun lavoro ripetuto, nessun
  artefatto duplicato, degrado a pending su guasto cache).
- `internal/capabilities/scripts/overlay_manifest_timeline_test.go` — un
  artefatto + manifest, ordine/z-order preservati, retry per digest, flag di
  rollback e fallback fail-closed al percorso per-item.
- `internal/capabilities/scripts/overlay_manifest_ab_test.go` — harness A/B:
  parità pixel/audio su fixture reali ffmpeg a finestre letterali, parità
  z-order in sovrapposizione, accordo ordine/fenestra tra i due percorsi.
