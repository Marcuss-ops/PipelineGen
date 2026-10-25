# TICKET-MANIFEST-IDENTITY (P5)

**Stato:** IMPLEMENTAZIONE LOCALE PARZIALE / PRODUZIONE OFF (25/10/2026) — modello, retry idempotente, percorso manifest+timeline dietro flag default-off, fixture A/B e wire contract sono presenti nel working tree. A/B live su golden e negoziazione col final worker non eseguite; MP4-per-item resta il default e non è stato demolito.
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

Nel percorso produttivo corrente ogni item mantiene MP4, receipt, hash e link
Drive proprio (le misure storiche `drive_publish` sono 2,4–3,0 s/item
asincroni, 36–57 upload/run nelle run citate sotto). Il percorso P5 proposto
potrebbe ridurre tali costi, ma la misura e la parità live non sono ancora
state eseguite; non attribuire oggi un risparmio al manifest.

## Cambio (fasi)

1. **A/B sul payload immutabile:** stesso piano materializzato renderizzato
   (a) col percorso MP4-per-item corrente e (b) come manifest + timeline
   unica. Confronto: parità pixel/audio, ordine item, retry idempotenti.
2. Solo dopo parità: negoziare col final worker (oggi remoto, packet-copy)
   il contratto manifest e il requisito di link Drive per item.
3. Demolizione del percorso MP4-per-item (mux/receipt/publish/stitch per
   item) a parità certificata.

## Gate di accettazione

- [ ] A/B live: parità pixel/audio tra MP4-per-item e manifest+timeline sul
      piano golden. Le fixture locali verificano solo la proiezione/ordine,
      non la parità dei due render reali.
- [x] Retry idempotente locale: stessa entry/hash riusa l'artefatto
      content-addressed; test unitari su voce e digest del manifest PASS.
- [x] Contratto scritto: `OVERLAY-MANIFEST-CONTRACT.md` mantiene Drive-per-item
      fino alla rinegoziazione. Nessuna demolizione autorizzata né eseguita.
- [ ] Negoziazione effettiva col final worker e scelta esplicita del requisito
      link/receipt per-item prima di cambiare il default.

## Dipendenze e rischi

- Contratto esterno: il final worker è remoto e fa packet-copy stitching;
  il cambio di formato è cross-team.
- P1 (durata invariant) e P4 (batch) diventano naturali su questo modello:
  una card statica è una riga di manifest con una texture.
- Non partire con la demolizione prima che l'A/B sia verde: è la ragione
  per cui la voce è P5 e non P1.

## Stato implementazione — checkout di sviluppo (evidenza 24/10/2026)

Fatto (lato PipelineGen, dietro flag di rollback):

- **Modello manifest** `pipelinegen.overlay-manifest.v1` —
  `internal/capabilities/overlays/model/manifest.go`: `item_id → (track,
  in_us, out_us, content_hash)`, validazione (un contenuto = un overlay,
  finestre positive, hash canonici), serializzazione deterministica,
  `Entry.RetryKey()` (item_id + content_hash) e `Manifest.Digest()`
  (identità di composizione, esclude `timeline_sha256`). Test:
  `manifest_test.go`.
- **Retry idempotente per voce** —
  `internal/capabilities/scripts/overlay_manifest_identity.go`: classificazione
  per voce sulla cache content-addressed (stesso item_id + content_hash ⇒
  clip certificata riusata, nessun secondo artefatto; guasto cache ⇒ degrado
  a pending). Test: `overlay_manifest_identity_test.go`.
- **Percorso manifest+timeline (opzione B)** —
  `internal/capabilities/scripts/overlay_manifest_timeline.go`: un artefatto
  certificato per timeline + manifest (single compositing, single encode),
  ordine/z-order preservati, retry per digest del manifest (nessun lavoro
  ripetuto), flag `VELOX_SCRIPTS_OVERLAY_MANIFEST_TIMELINE` **default off**
  (il percorso per-item resta il default produttivo), fallback fail-closed al
  percorso per-item per i piani non eleggibili (final-job, source, mappe,
  chain, multi-scena, oltre un caption-bearing). Test:
  `overlay_manifest_timeline_test.go`.
- **Harness A/B** — `internal/capabilities/scripts/overlay_manifest_ab_test.go`:
  parità pixel/audio su fixture reali ffmpeg a finestre letterali (round trip
  lossless), parità z-order in sovrapposizione, accordo ordine/fenestra tra i
  due percorsi sullo stesso piano.
- **Contratto scritto, non negoziato col worker remoto** —
  `docs/operations/OVERLAY-MANIFEST-CONTRACT.md` + wire tipizzato
  (`RenderReference.Manifest`): proiezione sui frame allineata a
  `finalJobOverlayAssetsExcluding`, decisione temporanea **Drive-per-item
  PRESERVATO** fino a negoziazione, handoff fail-closed invariato (test:
  `TestEnqueueChrononPlanFinalJobFailsClosedWithoutPublisher`).

Gate di accettazione — stato:

- [x] Retry idempotente per voce di manifest: **dimostrato** per test su cache
      content-addressed (nessun lavoro ripetuto, nessun artefatto duplicato) e
      a livello di composizione (retry dello stesso manifest ⇒ zero render).
- [~] A/B parità pixel/audio: **parità di proiezione dimostrata** su fixture
      reali (pixel/audio/z-order/ordine). La parità Chronon-vs-Chronon su
      piano golden resta da misurare: vedi blocco qui sotto.
- [x] Contratto preliminare scritto in `OVERLAY-MANIFEST-CONTRACT.md`;
      requisito Drive-per-item mantenuto provvisoriamente.
- [ ] Rinegoziazione effettiva col final worker: NOT DONE (cross-team).

### Verifica test locale mirata — 25/10/2026

- PASS: test manifest identity/content-change/cache-fallback e A/B su fixture
  (`TestBuildOverlayManifestEntries*`, `TestClassifyManifestEntries*`,
  `TestManifestTimelineAB*`, retry dello stesso manifest) inclusi nella
  selezione mirata `go test ./internal/capabilities/scripts -run '^(... )$'
  -count=1`.
- PASS: `go test ./... -count=1` e `go vet ./...` nei moduli RenderingGen e
  queue; il test di package non equivale a un A/B render Chronon live.
- FAIL separato: la suite più ampia scripts+overlays+observability fallisce su
  cinque casi di fixture/registry (builder, editoriale, Artlist, `esri_light`);
  non sono stati corretti o nascosti qui.
- NOT RUN: A/B pixel/audio Chronon-vs-Chronon sul golden, deploy del worker
  con il flag default-off, e rinegoziazione col final worker. Il percorso
  MP4-per-item e il requisito Drive-per-item restano intatti; nessun risparmio
  produttivo è attribuito al manifest.

### Blocco misura live A/B su golden (preciso)

Il confronto render-vs-render su piano golden richiede di far girare QUESTO
working tree dentro il percorso di render: non è eseguibile senza impatto su
produzione da questo checkout, perché:

1. il binario in esecuzione (`bin/pipelinegen`, build 2026-10-24 13:22) è
   **anteriore** a questo lavoro: il percorso manifest+timeline non esiste nel
   processo attivo;
2. eseguirlo richiede `make build && scripts/systemd/pipelinegenctl
   restart-verify` = **deploy/riavvio dei servizi di produzione** (decisione
   dell'operatore, non di un cambio codice);
3. anche un caricamento laterale one-shot sulla coda RenderingGen condivisa
   competerebbe coi daemon live (Chronon3d + pipelinegen-muscles + build C++
   attive sulla macchina) e violerebbe il contratto di misura (README: nessun
   altro carico pesante durante le misure), rendendo le medie a 3 run non
   confrontabili.

Quando l'operatore aprirà una finestra di misura (deploy esplicito + macchina
libera), l'A/B si esegue golden `final_job=false` con e senza
`VELOX_SCRIPTS_OVERLAY_MANIFEST_TIMELINE`, 3 run per percorso, mediana,
secondo il contratto di misura del ticket di throughput.

### Perimetro non demolito (come da portata del ticket)

La demolizione del percorso MP4-per-item è subordinata a (a) parità A/B su
golden certificata e (b) contratto rinegoziato col final worker. Entrambi
restano aperti: il percorso per-item è intatto e resta il default produttivo.

### Evidenza gate repository (24/10/2026)

- `go build ./...`, `gofmt -l`, `go vet ./internal/... ./cmd/...`: **verdi**.
- Test dei package toccati (`internal/capabilities/scripts`,
  `internal/capabilities/overlays/...`): **tutti verdi**, incluse le suite
  manifest/A/B sopra elencate.
- `make verify-agent`: **verify-dev verde** (foundation: gitleaks 19,4 GB
  senza leak, format, tidy, hook, submodule; static: facade alias in sync,
  vet, build). `verify-changed`: 21 componenti — **19 PASS/CACHED_PASS incluso
  il componente `script`** di questo lavoro; **2 FAIL (`timeline`, `rust`)**
  entrambi sullo stesso test `TestSemanticEngineRealBinaryPlansPlannerVolumes`
  in `internal/platform/media/rustexec` («candidate \"vc_0003\" query \"Alberto
  Stasi\" is not grounded»), sui file `semantic_engine*.go` modificati e non
  committati di un altro lavoro su questo working tree — non toccato da P5.
  Report: `artifacts/verify/changed-components.json`.
- `go run ./cmd/archcheck --strict`: **rosso per debito NON attribuibile a
  questo lavoro** — nessuna violation riguarda i file P5 (i nuovi file sono
  ben sotto il cap di 600). Dettaglio con attribuzione:

  | Violation | File | Righe HEAD → ora | Attribuzione |
  | --- | --- | --- | --- |
  | max_lines_per_file_strict | `scripts/model_render.go` | 606 → 606 | preesistente a HEAD (fuori dal census allowlist del 2026-09-18) |
  | max_lines_per_file_strict | `scripts/overlay_entity_cards.go` | 565 → 604 | lavoro non committato parallelo in corso |
  | max_lines_per_file_strict | `scripts/render_queue.go` | 602 → 620 | preesistente a HEAD + lavoro parallelo + dispatch P5 (~6 righe) |
  | max_lines_per_file_strict | `scripts/render_queue_map.go` | 592 → 692 | lavoro non committato parallelo in corso |
  | max_lines_per_file_strict | `scripts/render_queue_map_composition.go` | 600 → 638 | lavoro non committato parallelo in corso |
  | pkg_size (package_hotspot_growth) | `internal/capabilities/scripts` | 112 → 114 (budget 110) | già rosso prima di questo lavoro; +2 file manifest di questa sessione |
  | warning_budget | globale | 84 residui vs budget 77 | residui comment-only preesistenti (es. `kernel/asset/rights_state.go`) |
  | thin_command (warn) | `cmd/goldensummary/main.go` | 135 → 208 (cap 200) | lavoro non committato parallelo |

  Le voci «lavoro parallelo» appartengono a modifiche non committate di
  altri file attivamente in scrittura su questo working tree durante la
  sessione: non sono state toccate per non distruggere lavoro in corso.
  Percorsi sanzionati dalla governance per la chiusura: split dei file
  («split proactively», `docs/migrations/max-lines-strict-allowlist.txt` come
  escape hatch temporaneo con owner+deadline), riduzione del residuo warning,
  e aggiornamento della registrazione hotspot per il budget file del package
  (`architecture/policy.yaml`).
