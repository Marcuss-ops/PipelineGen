# Contratto manifest overlay col final worker (P5, cutover manifest puro)

Stato: **cutover ATTIVO in questo checkout** — il percorso manifest+timeline è
l'unico percorso di render per gli overlay ordinarie, e il payload del final
job porta **solo** il wire manifest (1 artefatto certificato per timeline +
manifest tipizzato + righe sostitutive indipendenti per i soli item a percorso
dedicato: mappe e chain item). Il worker finale (Master 51,
`BrainRust/pipelinegen-muscles/src/transform_assemble.rs`) deve ancora
apprendere il manifest: la negoziazione cross-team consiste nell'adottare i
due campi wire qui documentati (`overlay_manifest` nel FINALIZE e i riferimenti
a finestra `timeline_item_id`/`in_us`/`out_us` per i clip di scena). Fino a
quel deploy, le final job con overlay restano bloccate sul lato remoto: è la
conseguenza documentata della scelta "manifest puro", non un difetto locale.

Riferimenti: `docs/operations/TICKET-MANIFEST-IDENTITY.md`,
`docs/operations/TICKET-OVERLAY-THROUGHPUT-100-MIN.md`,
`internal/capabilities/overlays/model/manifest.go`,
`internal/capabilities/scripts/final_job_overlay_manifest.go`.

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
  sovrapposti)); l'ordine di piano sopravvive alla proiezione. Il track è
  **autorevole** perché lo z-order è già cotto nei pixel dell'artefatto.
- `in_us` / `out_us`: posizione sulla timeline con l'orologio canonico a
  microsecondi interi del piano (`in_us` incluso, `out_us` escluso). Sono la
  **finestra letterale renderizzata**: il wire la porta verbatim, perché il
  manifest è anche la ricevuta dell'artefatto e non va mai riscritto.
- `content_hash`: identità content-addressed del contenuto renderizzato della
  voce — il render key dell'item calcolato sul child plan che il percorso
  esegue davvero (64 hex minuscoli). Copre testo, asset, stili, timing
  relativo, canvas; NON copre il posizionamento.
- `placement` (opzionale, §3.2): il rect di proiezione spaziale della voce
  sulla canvas del piano (`x_px`,`y_px`,`w_px`,`h_px`). Assente = full-canvas
  (percorso classico, nessuna migrazione).

L'artefatto è **ancorato a plan t=0** e copre l'intero piano (tutte le scene):
la proiezione §3 non ha offset di origine, e l'ancoraggio a 0 allinea Source/
Background al tempo di piano (il difetto storico "la source riparte dal frame
zero" delle finestre fuse non è raggiungibile). Il documento porta anche
`plan_id`, `video_id` e `timeline_sha256` (identità dell'artefatto certificato).

## 2. Identità e retry

Due identità, distinte per costruzione:

- **Identità di composizione** — `OverlayManifest.Digest()`: SHA-256 sulle voci
  canoniche (item_id + content_hash + placement, ordine canonico) + identità di
  piano. Esclude `timeline_sha256`, quindi è stabile prima e dopo la
  certificazione. È l'indirizzo di contenuto dell'artefatto timeline: la cache
  content-addressed del percorso manifest+timeline è cablata su questo digest
  ed è di **proprietà del livello manifest** (`ManifestOwnedCache`): il
  submitter non crea una seconda riga per lo stesso artefatto.
- **Identità di voce** — `OverlayManifestEntry.RetryKey()`: SHA-256 su
  (item_id + content_hash). La stessa coppia ⇒ la stessa identità di lavoro.

Contratto di retry (gate 2 del ticket):

- Retry dello stesso manifest ⇒ stessa riga di cache ⇒ **nessun lavoro
  ripetuto, nessun artefatto duplicato** (lo Store della cache è idempotente
  sul digest della chiave).
- Una voce cambia contenuto ⇒ cambia `content_hash` ⇒ cambia il digest del
  manifest ⇒ esattamente un nuovo render della composizione.
- Spostare una finestra senza cambiare contenuto non cambia l'identità della
  voce (retry per voce), ma cambia correttamente l'identità di composizione
  (la posizione fa parte dei pixel dell'artefatto).

## 3. Proiezione sui frame (griglia condivisa)

La griglia è quella già usata dall'handoff finale, con
`frame_denominator = 1_000_000 * fps_den`:

```
start_frame = ceil(in_us  * fps_num / frame_denominator)
end_frame   = ceil(out_us * fps_num / frame_denominator)      (esclusivo)
```

Vincoli di proiezione riconosciuti da questo contratto:

- la finestra non può eccedere i frame certificati dell'artefatto
  (**clamp a `artifact.frame_count`** e, in coda, alla base certificata:
  il worker proietta e clampa, il wire resta verbatim — riscrivere `out_us`
  nel payload romperebbe la ricevuta §2);
- non può **iniziare** dopo la fine della base certificata (questo è un errore
  fail-closed in produzione, non un clamp);
- **le finestre possono sovrapporsi**: ogni voce legge i propri frame dallo
  STESSO artefatto, quindi la sostituzione è idempotente e lo z-order è già
  risolto nei pixel. La vecchia regola "nessun frame condiviso (mode=replace)"
  esisteva solo perché ogni voce portava la propria clip: con il cutover è
  superata e le voci vengono emesse al placement renderizzato (nessuna
  rischedulazione a payload time).

### 3.1 Timeline packed (composizione compatta, zero frame vuoti)

Con il P0 (TICKET-MAX-THROUGHPUT-OVERLAY-ARCHITECTURE §3.4) la composizione
di un piano **senza source video e senza background allineato al tempo**
(solo colore, o nessuno) **rinuncia agli span vuoti del piano**: l'artefatto
viene renderizzato su una **canvas packed** — l'unione delle finestre
LITERAL degli overlay, disposte una dietro l'altra — invece di coprire
l'intero asse temporale da t=0 (la run live `gate-100-overlays-only-r2`
aveva codificato 27.864 frame di cui ~80% vuoti).

- **Voci del manifest**: `in_us`/`out_us` restano la finestra di piano
  verbatim (la ricevuta, mai riscritta). Due campi opzionali portano la
  posizione reale sull'artefatto:
  `artifact_in_us` / `artifact_out_us` (esclusivi come gli altri, set
  **sempre insieme**, `artifact_out_us - artifact_in_us ≤ out_us - in_us`).
- **Proiezione**: su una timeline packed la griglia §3 si applica ai campi
  `artifact_*`, non a `in_us`/`out_us`. Un consumer che usa solo §3 su una
  voce packed degrada fail-closed come già previsto in §3/§5 (clamp/errore
  oltre la base certificata) — mai silenziosamente.
- **Identità**: le posizioni packed entrano nel documento (e quindi nel
  digest e nella chiave della cache di composizione) — un artefatto packed
  e uno assoluto sono due composizioni distinte. Spostare le posizioni
  stesse non cambia l'identità di voce (`RetryKey`).
- **Vincoli di ammissione** (`overlay_manifest_packed.go`, fail-closed):
  `source` presente → mai packed; background `image`/`video` → mai packed;
  background `color` o assente → packed consentito; ogni finestra candidata
  deve essere coperta dall'unione (un errore, mai un offset 0 silenzioso).

### 3.2 Placement spaziale (rect) — wire LAND, compositing SPEC

Aggiunto dal cammino A di
[`TICKET-PARADIGM-ALPHA-LAYER-HUD.md`](TICKET-PARADIGM-ALPHA-LAYER-HUD.md)
(A1, 2026-10-28): un'entry può portare il **rect di proiezione spaziale**
(`placement: {x_px, y_px, w_px, h_px}`, px sulla canvas del piano).

- **Semantica:** assente ⇒ la voce si proietta full-canvas (comportamento
  attuale, byte-compatibile); presente ⇒ il contenuto della voce si proietta
  in quel rect. I quattro valori sono un oggetto unico: una coppia parziale
  non è esprimibile sul wire e fallisce la deserializzazione (fail-closed già
  a parse time lato Rust; lato Go i campi mancanti diventano zero e falliscono
  la validazione: `w_px/h_px > 0`, `x_px/y_px ≥ 0`). Il vincolo
  `x_px+w_px ≤ canvas_w` (e analogo per h) è **producer-side fail-closed**: il
  manifest non porta le dimensioni canvas.
- **Identità (invariante §2):** il rect è metadata di posizione come
  `track`/`in_us`/`out_us` — escluso da `RetryKey` (stessa identità di lavoro
  al retry), incluso in `Digest()` (rect diversi ⇒ pixel diversi ⇒
  composizioni distinte, una riga di cache per ciascuna).
- **Stato del consumer:** il compositing a rect nel final worker
  (`transform_assemble`, alpha-blend sulla regione) è la fase **A3** del
  ticket ed è **NON IMPLEMENTATO**: fino ad allora le entry portano
  `placement: null` e il proiettore ignora il campo. Un consumer che proietta
  una entry con rect usando la regola full-canvas la disegna fuori posizione:
  l'attivazione del producer sul rect è quindi bloccata alla chiusura di A3
  (gate di parità visiva), non da oggi.

## 4. Wire del final job: manifest puro

Il PREPARE resta invariato (`scenes[]`, `copy_only`, `delivery_plan`). Il
FINALIZE porta:

- `overlay_manifest`: il documento tipizzato §1 + `asset` (riferimento
  content-addressed nell'Objectstore: `asset_id`, URL firmato HTTPS in
  `source_uri`, `sha256`, `size_bytes`, `frame_count`, `duration_ms`) — il
  worker scarica **1 file** via HTTPS e proietta le voci sui frame della master
  (§3). Il final-job non pubblica artefatti su Drive;
- `overlays`: **solo** le righe sostitutive indipendenti degli item a percorso
  dedicato (mappe, chain item), che mantengono una clip propria certificata e
  le regole di sempre (identità di contenuto unica; finestre disgiunte da
  ogni altra finestra, incluse quelle della composizione);
- i clip di scena "phrase-only" (scene senza video sorgente, la cui animazione
  È la scena) sono **riferimenti a finestra** nell'artefatto timeline
  (`timeline_item_id`, `in_us`, `out_us`) invece di clip standalone.

Decisioni documentate del cutover (cambiamenti rispetto al percorso per-item):

1. **Le voci omesse sono sicure.** Le phrase consumate come clip di scena e le
   card image-style su scene clip-intermediate non vengono emesse nel manifest:
   i loro frame non vengono mai sostituiti e la clip sorgente resta visibile.
   L'artefatto contiene pixel non utilizzati, mai posizionati.
2. **La politica di placement è a monte.** Il vecchio handoff spostava le
   phrase callout prima delle scene clip-intermediate a payload time; con il
   placement congelato nel render, la stessa regola vive in
   `normalizeFinalJobOverlayWindows` (runner, prima del render) ed è
   idempotente. Le immagini-scheda su quelle scene restano omesse (punto 1).
3. **I chart group sono pre-composti per costruzione.** Card + barre vivono
   come voci dello stesso artefatto: il gate "split chart" del vecchio percorso
   («pre-compose the chart into a single certified artifact») è soddisfatto dal
   manifest stesso e viene rimosso.

## 5. Handoff fail-closed

Nessuna invariante dell'handoff finale viene indebolita:

- il publisher HTTPS/objectstore deve essere configurato; non esistono fallback
  Drive nel percorso final-job;
- un artefatto timeline mancante, non presente nell'Objectstore, senza `sha256` canonico o senza
  byte fallisce l'intero handoff (`finalJobManifestWire`);
- il manifest viene ri-validato al build time (`manifest.Validate`: id unici,
  content_hash unici — "one content one overlay", la classe d'incidente
  2026-09-30);
- una voce che **inizia** dopo la fine della base certificata fallisce;
- una riga dedicata senza artefatto certificato, o che condivide byte con la
  timeline o con un'altra riga, fallisce;
- un item ordinario senza voce di manifest e senza artefatto dedicato fallisce
  (nessun fallback silenzioso).

## 6. Wire tipizzato

- Modello: `internal/capabilities/overlays/model/manifest.go`
  (`OverlayManifest`, `OverlayManifestEntry`, `ManifestPlacement`,
  `SchemaVersionManifest`).
- Trasporto render: `RenderReference.Manifest`
  (`internal/capabilities/scripts/model_result.go`).
- Trasporto remoto: `finalize.overlay_manifest` +
  `scene.clip.{timeline_item_id,in_us,out_us}`
  (`internal/capabilities/scripts/final_job_overlay_manifest.go`,
  `internal/capabilities/scripts/final_job_phrase_clip.go`).

## 7. Prova nel repository

- `internal/capabilities/overlays/model/manifest_test.go` — validazione,
  determinismo della serializzazione, identità per voce e di composizione;
  placement: validazione del rect, `RetryKey` insensibile al rect,
  `Digest()` distinto per rect distinti.
- `BrainRust/pipelinegen-muscles/src/protocol.rs` — parità wire Go↔Rust:
  rect validato fail-closed su entrambi i lati, oggetto placement
  all-or-nothing alla deserializzazione.
- `internal/capabilities/scripts/render_queue_batches_test.go` — composizione
  manifest: finestre letterali plan-absolute (inclusi i confi tra scene),
  voce singola ammessa, ammissione caption per disgiunzione temporale,
  partizione mappe/chain ai percorsi dedicati.
- `internal/capabilities/scripts/final_job_overlay_ssot_gate_test.go` — gate
  fail-closed sul nuovo wire: content-hash duplicato, riga che condivide
  l'artefatto della timeline, sovrapposizioni ammesse con track, chart group
  pre-composto, voce oltre la base certificata.
- `internal/capabilities/scripts/final_job_payload_test.go` — phrase-only clip
  come riferimento a finestra, voce consumata omessa, item scoperto rifiutato,
  finestre renderizzate preservate, clamp lato worker.
- `internal/platform/sqlite/overlayrendercache/integration_test.go` — una
  riga di cache per composizione (ownership manifest), retry senza lavoro
  ripetuto, kill switch.
