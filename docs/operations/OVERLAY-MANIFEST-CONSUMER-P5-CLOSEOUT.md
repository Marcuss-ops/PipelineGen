# Consumer Rust overlay-manifest (P5) — CHIUSURA 2026-10-28

Chiusura del lavoro P5 negoziato col final worker. Le due adozioni wire
descritte in `OVERLAY-MANIFEST-CONTRACT.md` §3/§4 sono implementate e
verificate lato worker (`BrainRust/pipelinegen-muscles`). Nessun deploy eseguito
in questa sessione (Master 51 resta al proprietario).

## Cosa è implementato

Due nuove operazioni wire nel crate `pipelinegen-muscles`, lato worker finale:

- **`project_overlay_manifest`** — proiezione §3: il master video riceve la
  sostituzione di frame per ogni finestra del manifest, letta dalla stessa
  finestra dell'artefatto certificato. Posizioni packed
  (`artifact_in_us/artifact_out_us`) quando presenti, altrimenti posizione di
  piano. Sostituzione pura di frame, nessuna composizione (le finestre
  sovrapposte sono idempotenti per costruzione). `source_path` = master,
  `input_paths[0]` = artefatto.
- **`extract_timeline_window`** — wire `scene.clip.{timeline_item_id,in_us,
  out_us}`: gli scene clip phrase-only sono finestre dello STESSO artefatto,
  estratte per entry id invece di materializzarsi come clip Drive. È il gemello
  worker della modalità `manifest` di `render_clip_exec`. `source_path` =
  artefatto, `timeline_window` = riferimento entry (+ eventuale sottofinestra
  di piano che preserva l'offset packed).

Rifiuto fail-closed su: manifest malformato (validazione tipizzata già
presente), master/artefatto su griglie di frame diverse, mappatura in
disaccordo tra finestre sovrapposte, inizio oltre i bordi artefatto/master,
`timeline_window` che cita entry inesistente o fuori dalla finestra entry.

## File toccati

- `src/manifest_projection.rs` (nuovo): matematica §3 pura — griglia floor/ceil,
  risoluzione finestra per entry con clamp, merge run con validazione
  idempotenza, risoluzione sottofinestra. 10 test unitari.
- `src/transform_manifest.rs` (nuovo): le due operazioni, filtergraph splice
  frame-accurate (`trim`+`setpts`, nessun re-time `fps`), audio master copiato
  verbatim quando presente.
- `src/protocol.rs`: varianti `ProjectOverlayManifest`/`ExtractTimelineWindow`,
  tipo `TimelineWindowRef`, validazione richiesta per entrambe.
- `src/transform.rs`, `src/dispatcher.rs`, `src/lib.rs`: wiring e dispatch.

## Verifica (2026-10-28)

- `cargo test` su `pipelinegen-muscles`: **143 test verdi, 0 falliti**, di cui
  i 10 nuovi di `manifest_projection` (griglia, packed/aligned, clamp,
  fail-closed, idempotenza sovrapposte, sottofinestra).
- Smoke e2e su frame sintetici (master rosso + artefatto blu, 30 fps, 60
  frame) attraverso il dispatcher di produzione:
  - `project_overlay_manifest`: output **60 frame esatti**; finestre sostituite
    dall'artefatto (blu) ai tempi di piano corretti, master preservato (rosso)
    fuori finestra.
  - `extract_timeline_window`: **12 frame** = artefatto 0.1–0.5 s esatto
    (finestra packed `artifact_in_us/out_us`).

## Bug trovato e chiuso durante la verifica

Il filtergraph iniziale re-impostava il frame rate (`fps=`) dopo `trim`. Il
filtro `fps` duplica un frame a fine segmento per riempire la finestra di
output: sommandosi su più segmenti in `concat` portava 60 → 75 frame e
spostava la mappatura §3 (il tail del master usciva dalla finestra sbagliata).
Corretto rimuovendo il re-time: master e artefatto sono già forzati sulla
stessa griglia (`same_frame_grid` fail-closed), quindi `trim`+`setpts`
preserva frame e tempi esatti. Riprovato: 60 frame, pixel corretti.

## Resta (fuori da questa chiusura)

- Deploy/rebuild del binario worker e cutover Master 51 (proprietario).
- Gli altri TODO dello stato (gate 100/min, mix golden, dead code residui)
  restano indipendenti da questa chiusura.
