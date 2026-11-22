# TICKET — Catene immagine/entità e backdrop: cosa resta dopo il 2026-11-19

- **Status:** PARTIAL (agg. 2026-11-22) — catena e movimento camera
  **verificati su job reale**: `r10` 22/22 gate (6 entità, pan destro,
  `static_ratio 0,5556`, 56 coppie in movimento), `r9` 22/22 e controllo
  fly-under `r3` PASS. **Chiusi in questa sessione:** il gate automatico sul movimento
  (punto 5), i residui mappe r15 (punto 8, 4/4 job `SUCCEEDED`) e il
  **backdrop su catena** (punto 4: `r4` fly-under SUCCEEDED, fondo sfocato
  misurato su ogni frame). **Chiusi il 2026-11-22 a livello di codice/test:**
  il lowering `fit: cover` misto (4:3/1:1/9:16), il canary reale del gruppo a
  2 immagini (121,58 s, H.264 1280×720), il clamp del framing multi-pin e il
  dedup `Gerusalemme`. Restano le prove live: clip con aspetti misti, confronto
  tempi 2 vs 6 e run r15 certificata dopo i fix mappa.
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
   **COPERTURA UNITARIA CHIUSA 2026-11-22:** il test
   `TestSizedCompileFitsMixedAspectEntityChain` compila una catena con asset
   4:3, 1:1 e 9:16 e verifica le card 600×337, 600×600 e 338×600, tutte
   `FitCover`, senza marker di geometria residui. Resta la clip live: nessun
   render con quegli aspetti è stato eseguito dopo la correzione.
4. ~~**Backdrop su ritratti entità e segmenti di catena.**~~ **CHIUSO 2026-11-21**
   per la catena fly-under. `compilePanChain` scarta di proposito
   `image_backdrop` (il blur software lega la superficie al nodo immagine
   successivo), quindi il gate si misura sulla catena fly-under:
   `entity-chain-flyunder-20261120-r4` (`job_1795279801017510593_47c4e504`)
   SUCCEEDED con 6 entità/6 asset, 408 frame, 1920×1080, e il nuovo gate
   `chain.backdrop_renders_a_blurred_background` PASS
   (`median_mean=37,2`, `median_gradient=1,033`, 136 frame). Il controllo
   negativo è misurato: catena pan r12 senza backdrop → `median_mean=1,0`,
   `median_gradient=0,0`, gate FAIL. Resta aperto solo il difetto del renderer
   sul blur lungo un pan (tracciato in `compilePanChain`), non il producers.
5. ~~**Gate automatico sul movimento di camera nei clip finali.**~~
   **CHIUSO 2026-11-21 (verificato):** `ops/jobs/entity_image_chain_20261119/check_chain_pan.py`
   fa **due** misure indipendenti — piano via `cmd/chainpancheck` (ogni
   segmento deve avere un track `position_x` ≥ `--min-pan-px`) e clip via
   phase correlation sui frame del `final.mp4` (≥50% coppie in movimento,
   velocità mediana ≥0,2, direzione coerente) — con exit 0 solo se passano
   entrambe. La suite `entity_chain_animation_20261120` impone lo stesso
   criterio dentro `verify_job.py:522-530` (`static_ratio` ≤ soglia,
   `moving_pairs` ≥ segmenti-1, `max_diff` ≥ soglia) e r10 lo passa con 56
   coppie in movimento. Un clip senza pan non può più passare.
6. **Catena vera vs animazioni singole.** In r9 ogni segmento porta
   `motion_id: image_fade_reveal` / `image_focus_reveal`, cioè animazioni da
   immagine singola applicate a una sequenza; la catena con pan di camera è
   stata implementata dopo e non è mai stata eseguita su un job reale.
7. **Variante a gruppi di 2 immagini** (fluidità e tempi di render).
   **CANARY REALE PASS 2026-11-22:** `TestEntityGroupAndNewFontsRenderCanary`
   con il binario release Chronon, backend software/pipe, ha reso un MP4 H.264
   1280×720 in 121,58 s. Resta il confronto tempo/fluidità contro la catena da
   6, che richiede una run della suite.
8. **Mappe/geo (r14, r15).** **Verificato 2026-11-21:** i quattro job della
   suite r15 sono tutti `SUCCEEDED` (`entity-cities`, `map-level-country`,
   `map-level-municipality`, `map-timing-two-pins`) e `entity-cities` porta una
   mappa con **tre pin città a coordinate reali** — Roma 41.893/12.483, Torino
   45.068/7.682, **Parigi 48.853/2.348** — quindi sia «Parigi senza
   coordinate» sia il multi-pin raggruppato sono risolti dal fix di
   `geography/enrichment.go` (test `TestEnrichRetriesAmbiguousCountryCityWithoutTheStaleFrame`).
   **Fix codice/test 2026-11-22:** `mapRenderPresentation.payload` non lascia
   più che `map_level` superi lo zoom di fit multi-pin; se il livello
   ritaglierebbe un pin, usa l'intervallo di fit, mentre un livello più largo
   resta valido (`TestMapLevelCannotCropMultiPinFit`,
   `TestMapLevelWiderThanMultiPinFitIsKept`). `dedupeMapCandidatesByCompoundPlace`
   fonde «Città Vecchia di Gerusalemme» in «Gerusalemme» conservando la
   finestra e il pin città. Resta la run r15 certificata dopo il fix: nessun
   job live ha ancora esercitato i due casi.
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
   il rapporto sorgente/renderizzato per card. **Copertura unitaria chiusa
   2026-11-22** (card 600×337/600×600/338×600, `FitCover`); resta la clip live.
   Gate: deformazione ≤ 1 % e larghezza derivata dall'aspetto, non costante.
4. ~~**Eseguire `image_backdrop` con catena e ritratto entità** (niente
   `chain_disabled`) e verificare i pixel del fondo (blur > 0, zoom applicato).
   Gate: entrambe le famiglie mostrano il fondo sfocato.~~ **CHIUSO 2026-11-21**
   sulla catena fly-under (`r4`); il pan resta escluso per il difetto del blur
   software.
5. ~~**Collegare `chainpancheck` alla suite** `chain_validation`: per ogni clip
   finale registrare spostamento per frame e fallire sotto soglia. Gate: la
   suite scrive il numero e il job con pan passa, quello senza pan fallisce.~~
   **CHIUSO 2026-11-21** (`check_chain_pan.py` + `cmd/chainpancheck`; `r10`
   22/22).
6. ~~**Eseguire la catena con `image_chain` motions** (pan di camera) e
   confrontarla con r9. Gate: i `motion_id` dei segmenti sono quelli di
   catena e il clip mostra il movimento misurato al punto 5.~~ **CHIUSO
   2026-11-20/21** (`r10`: `image_group_camera_pan_right`, 22/22).
7. **Preparare e lanciare la variante a 2 immagini per gruppo**, misurando
   tempo di render e fluidità contro la catena da 6. **Canary 2 immagini PASS
   2026-11-22** (121,58 s, H.264 1280×720); resta il confronto contro la
   catena da 6. Gate: report con i due numeri, non un'impressione.
8. **Riallineare mappe/geo** sugli esiti r15. **CHIUSO IN PARTE 2026-11-21:**
   r15 4/4 `SUCCEEDED` e tre pin città con coordinate reali. **Fix codice/test
   2026-11-22:** clamp del livello mappa sul fit multi-pin e dedup dei composti
   relazionali. Resta la run r15 certificata dopo il fix.
9. **Rieseguire la suite interna completa** (`go test -short ./internal/...`
   con `TMPDIR` fuori da `/tmp` e `-p 4`) dopo le ondate dead-code: la run
   finale del 19/11 è stata interrotta.

## Aggiornamento 2026-11-20 — catena 6 entità verificata

Evidenza completa: `ops/jobs/entity_chain_animation_20261120/REPORT.md`.

- **PASS pan** (`r9`, `job_1795163816753257040_743aa63a`): 6 PERSON, 6 immagini
  `retrieved/verified/materialized/bound`, una `image_chain` da 6 segmenti,
  handoff 400 ms, 408 frame catena + 578 frame finali, PTS puliti, 22/22 gate.
  `static_ratio=0.570`, 54 coppie in movimento, `max_diff=0.180`.
- **PASS controllo fly-under** (`r3`, `job_1795164090550608845_14fa4d17`):
  stesso scene shape, 6/6 entità, 6 segmenti, `static_ratio=0.778`, 27 coppie
  in movimento, `max_diff=0.091`. Il pan è quindi una coreografia distinta e
  misurata, non un'etichetta.
- Cause corrette nel payload: `chain_gap_ms=15000` (con 5000 la fusione si
  chiudeva a 2 segmenti); `animation_counts.entities=6` è la prenotazione
  decisiva (le 3 still generiche occupavano i posti e lasciavano 4 entità
  `verified_asset_not_planned`; `max_image_queries_per_segment=0` da sola non
  bastava, r7 lo dimostra); run calda in `cache.read=true` dopo il retrieval
  freddo che ha popolato il catalogo. `overlay_categories.images=disabled`
  non è valido: spegne anche la catena.
- Corretto un difetto di telemetria: l'artefatto registrava il backend
  configurato (`vulkan`) invece di quello effettivo (`software` per il pan).
  Fix in `RenderingGen/renderinggen/internal/processor` con test dedicato;
  in attesa del deploy il verifier usa il worker log come witness.
- Blocchi esterni: worker RenderingGen riavviato più volte da un'altra
  sessione; PipelineGen terminato e `.venv-whisper` rimosso. Il venv è stato
  ricreato dal lockfile e completato con `edge-tts`; le run finali sono passate
  dopo il ripristino.
- Il gate di movimento della suite misura ora i movimenti di handoff
  (`moving_pairs`, `max_diff`, `max_static_run`), non un pan continuo durante
  le pause da 2,5 s.

### Aggiornamento 2026-11-20, sera — r10 e r11

- **PASS pan corretto** (`r10`, `job_1795166123889306562_acaae690`): 22/22
  gate, 6 entità e 6 asset distinti, `image_group_camera_pan_right`, handoff
  400 ms, 408 frame catena + 578 finali, `static_ratio 0,5556`,
  `moving_pairs 56`, `max_diff 0,180`. È la prova che il punto 6 (catena con
  pan di camera su job reale) e il punto 5 (misura del movimento) sono
  soddisfatti.
- **FAIL infrastrutturale** (`r11`, `job_1795167689555666682_3c963e4f`): la
  variante con backdrop si ferma in `COMPILING_AUDIO` con
  `COMPILING_AUDIO_FAILED message=context canceled`, senza piano né item: il
  backdrop non è stato esercitato. Va rifatta, non interpretata.
- Il gate di chiusura resta quindi aperto sul solo job catena **con** backdrop;
  tutto il resto della catena è verificato.

### Fast path CPU per immagini `Cover` — misura locale 2026-11-20

`Chronon3d/src/backends/assets/image_renderer_draw_detail.hpp` usa ora il
framebuffer cached direttamente quando il fit lascia intatta tutta la sorgente;
prima `Cover` copiata ogni volta in un framebuffer temporaneo. Sul fixture
software camera-pan 1920×1080, un draw `Cover` è sceso da **21,26 / 21,55 /
23,06 ms** a **5,64 / 5,18 / 5,64 ms** in tre processi. Lo SHA del frame
decodificato coincide con il controllo `Stretch` (`abf64a0a…`). Build CLI e
`chronon3d_renderer_core_tests` PASS. È una misura per draw, non una nuova run
end-to-end della catena r10; il gate di throughput della chain resta da
misurare sul job completo.

## Ricontrollo 2026-11-21 — nessuna run nuova, r11 ancora infrastrutturale

Verifica sui dati prodotti (nessuna run eseguita in questa sessione):

- `entity-chain-pan-20261120-r10-full.verification.json` → `passed: true`,
  `job_1795166123889306562_acaae690` (22/22 gate).
- `entity-chain-pan-20261120-r11-full.verification.json` → `passed: false`,
  `job_1795167689555666682_3c963e4f`, con
  `status=FAILED stage=COMPILING_AUDIO ... message=context canceled`: il
  backdrop non è mai stato esercitato, quindi il primo gate di chiusura resta
  non misurato.
- Nessun job successivo con catena **e** backdrop compare in
  `ops/jobs/entity_chain_animation_20261120/` (ultimo file: `upload-manifest.json`
  del 2026-11-20 10:35); `entity_chain_background_20261119/` contiene solo la
  run r45 bricolage, non la variante r11.

Il resto dei punti resta com'era: `fit: cover` senza clip rifatta, backdrop su
ritratti/catena non rieseguito, `chainpancheck` non collegato alla suite,
variante a 2 immagini non preparata, mappe/geo r15 parzialmente verificate.
Il punto 6 (catena con pan su job reale) e il punto 5 (misura del movimento)
sono invece chiusi da r10, come già dichiarato.

## Aggiornamento 2026-11-22 — residui di codice coperti

- `go test ./internal/capabilities/overlays -run 'TestDedupeMapCandidatesBy' -count=1` → PASS.
- `go test ./internal/capabilities/scripts -run 'TestMapLevel' -count=1` → PASS.
- Da `RenderingGen/renderinggen`:
  `go test ./internal/overlay -run 'TestSizedCompileFitsMixedAspectEntityChain|TestEntityGroupDuoSlotsMembersSideBySide|TestEntityGroupUsesCertifiedCardSlots' -count=1` → PASS.
- Da `RenderingGen/renderinggen` con il binario release:
  `CHRONON_BIN=…/Chronon3d/build/chronon/linux-video-release/apps/chronon3d_cli/chronon3d_cli go test ./internal/overlay -run '^TestEntityGroupAndNewFontsRenderCanary$' -count=1 -v` → PASS (121,58 s; H.264 1280×720).
- Limite: i test coprono lowering, payload e un canary del duo; non sostituiscono
  la clip live con aspetti misti, il confronto temporale 2 vs 6, né la run r15
  certificata per multi-pin e dedup.

## Gate di chiusura

- ~~Un job reale con catena ≥ 3 entità e backdrop: `SUCCEEDED`, clip
  1920×1080, nessuna deformazione, fondo sfocato su ogni segmento.~~
  **SODDISFATTO 2026-11-21** da `entity-chain-flyunder-20261120-r4`
  (6 entità, 408 frame, backdrop misurato). La catena con camera-pan resta
  esclusa dal backdrop per il difetto del blur software documentato in
  `compilePanChain`.
- `chainpancheck` misura movimento > 0 su ogni clip di catena e la suite lo
  impone.
- `chain_handoff_ms: 0` non produce né buchi neri né il FAIL `editorial`.
- I punti 8–9 hanno esito registrato (mappe e suite interna), anche se il
  port `TacticalFillPack` resta al suo ticket Chronon3d.
- I residui 3/7/8 hanno ora copertura di codice/test; le prove live (clip
  aspetti misti, confronto 2 vs 6, run r15 dopo i fix mappa) restano da fare.
