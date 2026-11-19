# TICKET — Catene immagine/entità e backdrop: cosa resta dopo il 2026-11-19

- **Status:** OPEN (2026-11-19) — geometria card (`fit: cover`, larghezza
  dall'aspetto), backdrop per catene/ritratti e lane libx264 sono atterrati nel
  working tree di RenderingGen, ma la validazione end-to-end è incompleta:
  `chain-validation-20261119-r6-phrase` è **FAILED** in 1 s, la catena entità
  con backdrop (`r11`/`r12`) è **FAILED**, `r13` era ancora `RUNNING` alle
  10:43Z e i gate "movimento camera" e "gruppi da 2 immagini" non sono stati
  eseguiti.
- **Severity:** P1 — la catena è la superficie che l'utente usa per mostrare
  più entità di seguito; un FAIL a 1 s in `COMPILING_AUDIO` blocca il job intero,
  non solo la clip.
- **Owner:** overlay/editorial pipeline (BrainRust `overlay-compiler`) +
  media plane (refactored) + lowering (RenderingGen)
- **Source:** sessioni Freebuff del 2026-11-19 (`freebuff-chat-2026-11-19T06-31-44`,
  `06-31-46`, `06-43-58`, `06-50-37`) e evidenze in
  `ops/jobs/chain_validation_20261119/`, `ops/jobs/entity_image_chain_20261119/`,
  `ops/jobs/entity_chain_background_20261119/`, `ops/jobs/image_backdrop_20261119/`,
  `ops/jobs/geo_overlay_suite_20261119_r14/`, `geo_overlay_suite_20261119_r15/`
- **Correlati:** `TICKET-CHAIN-SEGMENT-SSOT-2026-11-18` (contratto segmenti),
  BrainRust TICKET-29/30/31 (numeri e date), `TICKET-MAX-THROUGHPUT-OVERLAY-ARCHITECTURE`
  (gate ≥100/min, qui non toccato)

## Cosa è chiuso (con evidenza)

| Area | Esito | Evidenza |
|---|---|---|
| Catena entità, 10 segmenti, 2,5 s minimi | **PASS** su r9 (`passed: true`, 16 controlli): 3 catene (6/2/3 segmenti), 11 entità con asset+sha, 400 ms di handoff, clip 1920×1080 h264, `rtf 0.95`, 408/408 frame, 5/5 clip unite | `entity_image_chain_20261119/entity-image-chain-20261119-r9-full.verification.json`; REPORT.md §Cause trovate e corrette |
| 6 difetti della catena (sforamento scena, cap 10, doppia run, finestra entità, verifica, naming clip) | corretti in `planner/coalesce.rs` e `planner/media.rs`; 106 unit + 30 integration verdi | stesso REPORT, tabella §Cause |
| Backdrop sfocato su immagini singole via payload | **PASS**: `overlay_style.image.backdrop` (`zoom`, `blur_px`, `grid`) + radius/shadow/border; job r2 a 1920×1080 con tre still, ripetuto in r4 | `image_backdrop_20261119/REPORT.md`; `image-backdrop-20261119-r4-job.done` |
| Handoff catena configurabile | `chain_handoff_ms` nel payload; a `400` i segmenti hanno 400 ms dichiarati, a `0` devono affiancarsi | `chain_validation_20261119/build_payloads.py`; r6 |
| Lane libx264 per pan di camera su catene | `SoftwareRenderer` separato, serializzato, usato solo con `Backend == "software"` e senza output segment | `RenderingGen/renderinggen/{cmd/renderinggen/main.go,internal/processor/processor.go,internal/processor/gpu_session.go}` (working tree); test `ExecutionPolicy|RenderSession` ok |

## Cosa resta (bloccanti prima di dire chiusa la catena)

1. **Regressione planner a 1 s: `missing required section "editorial"`.**
   `chain-validation-20261119-r6-phrase` (`job_1795083526879611192_7c4915dc`)
   e `entity-chain-bg-oswald-20261119-r12` (`job_1795083967166043266_247b4d97`)
   falliscono in `COMPILING_AUDIO` con
   `semantic-engine editorial plan worker: editorial plan: missing required
   section "editorial"`. Il denominatore comune è il payload r6/r12 costruito
   dopo l'installazione di `semantic-engine` sha `f2ff3083…` (10:18Z); va
   isolato se la causa è il binario o `chain_handoff_ms: 0`, non aggirato
   rimettendo 400 ms.
2. **Render catena con backdrop: daemon exit 1.**
   `entity-chain-bg-oswald-20261119-r11` fallisce su
   `static__scene-01-chain-image-0__image-chain__01` con
   `processor: render: ipc render: daemon status Error (1): render job failed
   with exit code 1`; serve il log del daemon Chronon per quell'item, non solo
   l'errore aggregato del processor.
3. **`fit: cover` per-item e larghezza card dall'aspetto.**
   `entity_card_layout.go` e `entity_image_geometry.go` sono modificati con
   test dedicato, ma nessuna clip è stata rifatta dopo la correzione: manca la
   prova che un asset 4:3 e uno 9:16 nella stessa catena non vengano deformati
   e che la card cambi larghezza.
4. **Backdrop su ritratti entità e segmenti di catena.**
   `image_backdrop.go` è modificato, ma il REPORT di `image_backdrop_20261119`
   documenta ancora il rifiuto su item con `image_layers` e su `image_chain`
   (`chain_disabled: 1` per quel motivo). Dopo la modifica serve un job con
   catena **e** ritratto entità, non la sola still singola.
5. **Gate automatico sul movimento di camera nei clip finali.**
   `RenderingGen/renderinggen/cmd/chainpancheck/` e
   `internal/overlay/chain_camera_pan_test.go` esistono, ma nessuna suite
   misura i clip prodotti: il criterio «la camera si muove davvero» non è
   ancora un controllo ripetibile.
6. **Catena vera vs animazioni singole.** In r9 ogni segmento porta
   `motion_id: image_fade_reveal` / `image_focus_reveal`, cioè animazioni da
   immagine singola applicate a una sequenza; la catena con pan di camera è
   stata implementata dopo e non è mai stata eseguita su un job reale.
7. **Variante a gruppi di 2 immagini** (fluidità e tempi di render) mai
   preparata: non esiste un payload né un confronto.
8. **Mappe/geo (r14, r15).** Restano: `Parigi` estratta senza coordinate
   (guard sui frammenti di nomi propri), `Gerusalemme` fusa con «Città Vecchia
   di Gerusalemme» (un pin in meno), pin multipli non tutti inquadrati
   (`per_pin_timing_not_ported`; la composizione certificata inquadra il
   primo). `r15/entity-cities` è `SUCCEEDED` alle 10:43Z; gli altri job della
   suite r15 non sono ancora stati verificati.
9. **Port `TacticalFillPack`** bloccato nel renderer Chronon3d: i layer `path`
   subiscono una traslazione di mezzo canvas in un pass prospettico e
   `enable_3d:false` non li tocca (documentato in
   `ChrononTemplate/out/tactical_fill/render_20261119/README.md`). Non è un
   lavoro di questo repository: resta tracciato qui per non perderlo.

## Prossimi passi (in ordine)

1. **Riprodurre e chiudere il FAIL a 1 s.** Rieseguire r6-phrase sul binario
   precedente (`semantic-engine` pre-`f2ff3083`) e sul corrente con
   `chain_handoff_ms` 400 vs 0: quattro combinazioni, un solo job breve.
   Gate: la combinazione col payload voluto è `SUCCEEDED` e il piano contiene
   la sezione `editorial`; se il difetto è nel planner, aggiungere un test in
   `overlay-compiler` che fallisca con quel payload.
2. **Leggere il log del daemon per r11** e classificare l'exit 1 (asset
   mancante, layer non supportato, OOM). Gate: la stessa clip catena+backdrop
   si renderizza, oppure il lowering rifiuta il caso con un errore esplicito.
3. **Rifare la clip con asset di aspetto diverso** (4:3, 1:1, 9:16) e misurare
   il rapporto sorgente/renderizzato per card. Gate: deformazione ≤ 1 % e
   larghezza derivata dall'aspetto, non costante.
4. **Eseguire `image_backdrop` con catena e ritratto entità** (niente
   `chain_disabled`) e verificare i pixel del fondo (blur > 0, zoom applicato).
   Gate: entrambe le famiglie mostrano il fondo sfocato.
5. **Collegare `chainpancheck` alla suite** `chain_validation`: per ogni clip
   finale registrare spostamento per frame e fallire sotto soglia. Gate: la
   suite scrive il numero e il job con pan passa, quello senza pan fallisce.
6. **Eseguire la catena con `image_chain` motions** (pan di camera) e
   confrontarla con r9. Gate: i `motion_id` dei segmenti sono quelli di
   catena e il clip mostra il movimento misurato al punto 5.
7. **Preparare e lanciare la variante a 2 immagini per gruppo**, misurando
   tempo di render e fluidità contro la catena da 6. Gate: report con i due
   numeri, non un'impressione.
8. **Riallineare mappe/geo** sugli esiti r15: coordinate per `Parigi`, dedup
   di `Gerusalemme`, framing multi-pin. Gate: i job r15 `SUCCEEDED` con i pin
   attesi e nessun pin fuori cornice non dichiarato.
9. **Rieseguire la suite interna completa** (`go test -short ./internal/...`
   con `TMPDIR` fuori da `/tmp` e `-p 4`) dopo le ondate dead-code: la run
   finale del 19/11 è stata interrotta.

## Gate di chiusura

- Un job reale con catena ≥ 3 entità e backdrop: `SUCCEEDED`, clip
  1920×1080, nessuna deformazione, fondo sfocato su ogni segmento.
- `chainpancheck` misura movimento > 0 su ogni clip di catena e la suite lo
  impone.
- `chain_handoff_ms: 0` non produce né buchi neri né il FAIL `editorial`.
- I punti 8–9 hanno esito registrato (mappe e suite interna), anche se il
  port `TacticalFillPack` resta al suo ticket Chronon3d.
