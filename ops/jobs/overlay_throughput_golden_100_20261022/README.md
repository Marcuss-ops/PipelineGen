# Golden case — 100 overlay/minuto

Payload v2 storico conservato nel repository: `../overlay_throughput_golden_100_20261022.v2.generate.json`
(SHA-256 `fe06d3b3fe68685af53fdf353de8940dd3db8c68046d97896f18b99b42c2f672`).
Il precedente SHA-256 `60fb14ec1e6ace95489f38e2b84f91dd7a28d45d89edbcf1e5c060a52d63080d`
identifica la versione precedente a `f255d9505`; quel commit ha sostituito
`render.enabled=true` con `render.enabled=false, overlays_only=true`. Non
assumere che le run storiche usino lo stesso payload: verificare lo SHA della
copia effettivamente sottomessa per ogni confronto.

Deriva dal job reale `08_fonti_media.generate.v11.json` (batch Garlasco 10 video):
20 segmenti, clip intro già selezionate, `final_job=false` (overlay-only),
riuso cache abilitato, refresh asset disabilitato, `max_overlays_per_scene=10`.
Il mix richiesto è 25 frasi brevi + 25 lunghe + 30 immagini (10 schede entità)
+ 10 date + 10 mappe = 100 overlay. Il payload corrente è però un caso **senza
mappe**: `overlay_categories.maps=disabled`, `max_map_overlays=0`,
`overlay_group_limits.maps=0` e `animation_counts.maps=0`. Non può certificare
il mix richiesto, anche se il testo del brief domanda dieci mappe. Il piano
materializzato dipende inoltre dai risultati live; le run recenti r4–r7 hanno
prodotto 65–70 item. Il test sintetico
`TestCompileOverlayPlanGoldenThroughputMixMaterializes100Items` prova 100
item con candidati completi e mappe abilitate, non la run live. Serve una
versione del workload coerente con il mix, con candidati e asset verificati;
non cambiare il payload storico fingendo che le misure restino confrontabili.

Il nuovo workload `../overlay_throughput_golden_100_20261022.v3-maps.generate.json`
(SHA-256 `7d3089014f613aff65318ad6779e5ac7c8d66cbf017f95e2f2c3f0f88c8c4f3a`)
abilita le mappe e mantiene `final_job=false`, `render.enabled=false` e
`overlays_only=true`. Il test sintetico legge e verifica direttamente i suoi
limiti e le sue categorie, poi controlla il mix 25+25+20+10+10+10 con
candidati e asset di prova. È una prova del planner, non garanzia che i
provider live producano i 100 candidati o che il throughput sia certificato.
Il driver `run_golden.sh` usa ancora la v2; per la v3 specificare il nuovo file
nel comando `submit-and-poll.sh` solo in un ambiente autorizzato.

Il workload `../overlay_throughput_golden_100_20261022.v4-maps-auto.generate.json`
(SHA-256 `071eddaaa5526270f0e1b15ccb1105c92dcc7311534579d88b55b987e1527d1b`)
è la variante **mappe a selezione automatica**: stesso identico corpo della v3
(mix, limiti, categorie, brief, cache), con la sola configurazione mappe
portata al contratto unico `map_presentation` (`mode=auto`, `animation=random`,
`camera=auto`, `style=chronon_editorial`, `selection_mode=auto`) e i campi
legacy `map_type/map_camera_animation/map_label_animation/map_animation`
rimossi (la v3 ne aveva uno: `map_animation=region_zoom`, oggi ritirato e
fail-closed). Lo scopo non è il throughput ma certificare che la **selezione
automatica** delle composizioni mappe campioni la gallery nativa
ChrononPlugin (incluse le nuove scene documentarie) su un piano reale e che il
renderer nativo le esegua. L'item id è nuovo (`...-v4-maps-auto`) sia per la
cache fredda sia per non consumare la chiave v3: le run v4 NON sono
confrontabili col throughput delle run v2/v3.

Il workload `../overlay_throughput_golden_100_20261022.v3.1-maps.generate.json`
(SHA-256 `ba3befed44c7676f810945dcf04ef68daf8898e28aac6d49c2ca5ad72fc8d846`)
è la **v3 runnabile**: corpo identico alla v3 con due sole eccezioni.
1. `map_animation`: `region_zoom` → `territory_glow_reveal` (famiglia
   `region_highlight`, id certificato dal catalogo nativo). La validazione
   runtime è fail-closed sugli id ritirati: la v3 così com'è non può mai
   compilare, quindi la correzione è necessaria, non facoltativa.
2. I tre identificatori interni (`correlation_id`/`id`/`project`) passano da
   `…-v3-maps` a `…-v3.1-maps`, come già fatto per la v4: cache fredda e
   lineage univoco per il nuovo payload.

Non esiste alcuna run riuscita basata sulla v3 (il primo tentativo v3-maps
era già morto su questo stesso errore il 28/10), quindi la v3.1 non rompe
nessuna baseline: è la definizione della misura per il mix completo con mappe.
Driver dedicato: `run_golden_v3.sh <label>`.

Aggiornamento 29/10/2026: il payload **v2 storico** conserva
`map_animation=region_zoom` nella style spec, ID ritirato che la validazione
attuale rifiuta fail-closed; non va quindi riutilizzato come input di nuove
run. Correzione di provenienza: gli artefatti `v2-r8` mostrano il payload
v2.1-nomaps e una run riuscita, non documentano un fallimento della v2
storica. Le run v6…v28 restano riferibili solo ai rispettivi payload e binari.
Quindi:

- `overlay_throughput_golden_100_20261022.v2.1-nomaps.generate.json`
  (SHA-256 `ad5e3468a5eee166df5f4f0899439e6de239b7e10490f042f9237f728db9f159`)
  è la **v2 runnabile**: corpo identico con `territory_glow_reveal` (id
  certificato) e identifieri `…-v2.1-nomaps`. Mappe DISABLED come in v2: l'id
  serve solo alla validazione a compile e non raggiunge mai un renderer.
  Driver `run_golden.sh` ora punta a questo file.
- Aggiornamento producer parity: il producer Go risolve `territory_feature`
  e il raggio area glow quando richiesti; la lane mappe è sbloccata a livello
  di compile/plan, non ancora certificata live. `territory_glow_reveal`
  richiede il catalogo ADM3 e un pin risolvibile; servono inoltre renderer
  compatibile e candidati/asset effettivi. Vedi
  `TICKET-MAX-THROUGHPUT-OVERLAY-ARCHITECTURE` §9.2 e §9.9.
- La run v2.1 moderna `results/v2-r8` è riuscita ma ha renderizzato 65 item
  in 686,127 s (5,684 item/min): non certifica né i 100 item né il gate.
  Non è comparabile alle run storiche v6…v28, che restano confrontabili solo
  accanto al loro payload/binario.

## Comando di ripetizione (operativo, non eseguito in questa verifica)

```bash
cd refactored
ops/jobs/overlay_throughput_golden_100_20261022/run_golden.sh <label>
```

Il driver usa il payload v2.1-nomaps con SHA-256
`ad5e3468a5eee166df5f4f0899439e6de239b7e10490f042f9237f728db9f159`.
Il comando effettua submit, polling e query al servizio locale: è un'azione
live e va eseguita solo nella sessione autorizzata e con prerequisiti §9.2.
Non eseguire il vecchio comando con il payload v2 storico: il suo
`map_animation=region_zoom` è ritirato e viene rifiutato fail-closed.
L'idempotency key è univoca per run (mai riusare una chiave consumata).
Il servizio deve girare sul binario con la strumentazione
(`prefetch/prepare/encode` delle mappe + `drive_publish` outbox) e col pool item
alla larghezza feed (`separate_item_render_workers=4`, cap `overlayItemMaxWidth=6`;
retuned 2026-10-23 con il receipt fuori dalla lane):
`make build && scripts/systemd/pipelinegenctl restart-verify`.

Prerequisiti della macchina (verificati 22/10 2026):
- ObjectStore L3 (`docker-objectstore-1`) con `verify_on_write`/`verify_on_read`;
  cache immagini idrata: ogni `entity_image_catalog_materializations`
  corrisponde a un oggetto L3 verificato (altrimenti il prefetch fallisce 422).
- **Topologia lane dichiarata** (le run con lane diverse NON sono confrontabili):
  attuale = 2 lane render (`chronon3d.service` + `chronon3d-b.service`, worker
  `chronon.socket_paths` = 2 socket, `gpu_lanes=2`,
  `separate_item_render_workers=4`, `RENDERINGGEN_GPU_SLOTS=2`). Le run v1–v10
  sono 1 lane; v11–v12 sono 2 lane con il receipt dentro la lane; v13+ sono
  2+ lane con il receipt di verifica fuori dalla lane (fix 1).
  Verificare il log di avvio worker: `lane/daemon parity: ... sockets=2 ...`.
- Nessun altro carico pesante sulla macchina (i tempi non sono altrimenti
  confrontabili).

## Stato delle run

| Run | Esito | Config lane | Item (piano/render/links) | Finestra render | Item/min |
| --- | --- | --- | ---: | ---: | ---: |
| v6 | SUCCEEDED | 1 | 37/37/37 | 127,9 s | 17,4 |
| v7 | FAILED (asset stale 422, poi bonificato) | 1 | 27 submit | — | — |
| v8 | SUCCEEDED | 1 | 41/41/44 | 83,4 s | 29,5 |
| v9 | SUCCEEDED | 1 | 41/41/41 | 90,5 s | 27,2 |
| v10 | SUCCEEDED | 1 | 36/36/36 | 85,0 s | 25,4 |
| **v11** | SUCCEEDED | **2** | 38/38/38 | **60,4 s** | **37,8** |
| **v12** | SUCCEEDED | **2** | 38/38/38 | **54,6 s** | **41,7** |
| v20 | SUCCEEDED | 3 + fused batching (build dc7bf31f9 pre-fix caption) | 73/73/73 | 51,9 s | **84,5** |
| v21 | **FAILED** (batch caption collision, build pre-fix) | 3 | — | — | bug trovato e fixato: `render_queue_batches.go` ammette ≤1 item con caption per batch |
| v22 | SUCCEEDED | 3 + fused (build cd1f2c0f, fix caption) | 73/73/73 | 58,7 s | **74,6** |
| v23 | SUCCEEDED | 3 + fused (build cd1f2c0f) | 74/74/74 | 56,3 s | **78,9** |
| v24 | SUCCEEDED | 3 + fused (build cd1f2c0f) | 76/76/76 | 52,9 s | **86,2** |
| v25 | SUCCEEDED | 3 + fused + **Fix 6** (downscale display-bound) | 75/75/75 | 60,5 s | **74,4** |
| v26 | SUCCEEDED | 3 + fused + Fix 6 | 72/72/72 | 55,5 s | **77,9** |
| v27 | **FAILED** (SyntaxError map renderer, pre-render) | 3 | — | — | bug trovato e fixato: `render_dynamic_map_overlay.py` (righe fuse); tentativo conservato in `results/overlay-only-v27-abort-map-syntax/` |
| v27 (retry) | SUCCEEDED | 3 + fused + Fix 6 | 76/76/76 | 66,5 s | **68,6** |
| v28-chain…chain4 | FAILED (catena) | 3 + fused + Fix 6 | — | — | fix a valle: `cv2` import, `plate_margin_factor` 1.0→2.0, migration PG 010/011 |
| v28-chain5 r1 | SUCCEEDED | 3 + fused + Fix 6 (build Go `0e9e735d`) | 74/74/74 | 54,0 s | **82,1** |
| v28-chain5 r2 | SUCCEEDED | 3 + fused + Fix 6 | 73/73/73 | 59,8 s | **73,2** |
| v28-chain5 r3 | SUCCEEDED | 3 + fused + Fix 6 | 73/73/73 | 59,2 s | **74,0** |

Baseline (1 lane, mediana v8/v9/v10): **27,2 item/min**. Dopo la seconda lane
(v11+v12): **37,8 e 41,7 item/min** (+39…+53%, overlap render 1,89×, nessuna
regressione).

**Baseline fused 24/10 (3 lane + batching, build `cd1f2c0f`, mediana v22–v24):
78,9 item/min (range 74,6–86,2), finestre 52,9–58,7 s, 73–76 item/run, 15–18
batch fused (37–45 membri), servizio per item non-fused avg 1,86–2,31 s.**
Gate 100/min: **NON raggiunto, gap 1,27×**.

**Post Fix 6 (3 run v25–v27, mediana 74,4 item/min, range 68,6–77,9):**
nessun guadagno di finestra attribuibile (carico concorrente sulla macchina,
condizione già dichiarata), ma l'asse del Fix 6 è misurato e positivo sugli
item immagine: `gpu_upload_bytes` per item immagine 465 → 237 MB (−49%),
`prepare_image_decode_ms` 411 → 169 ms (−59%). **`prepare_image_upload_ms`
non è strumentato (null in tutti i sidecar):** i proxy sono byte/count, non ms.
Gate P1/P2/P4 quantificati sui sidecar (slope durata +372 ms/s, overhead fisso
~465 ms/item, fused 0,66–0,92× singolo): tutti NON raggiunti — vedi la sezione
«Verifica 24/10/2026 (sessione 3)» in `TICKET-OVERLAY-THROUGHPUT-100-MIN.md`.

**Run di conferma sui binari nuovi (v28-chain5, 24/10 19:03–19:28) —
COMPLETE:** il blocco del toolchain mappe è stato risolto (fix `cv2` import in
`render_dynamic_map_overlay.py`; fix geometrico `plate_margin_factor` 1.0→2.0
in `render_dynamic_map_overlay.py` con repro unitario del difetto
«no prepared plate at zoom 9»; allineamento schema PG media con le migration
`010_media_drop_legacy_file_md5` + `011_media_asset_versions_content_sha256`
applicate al DB live). Mediana 3 run: **73,95 item/min** (range 73,2–82,1),
73–74 item/run, finestra 54–60 s, servizio per-item avg 2.167–2.252 ms. Il
binario Go (`0e9e735d`) include batching e render cache; il binario render
Chronon3d (`chronon3d_cli` 15:11) è **anteriore** all'implementazione P1
(`duplicate_frame`), quindi queste run non misurano P1. Mediana sostanzialmente
invariata rispetto a v25–v27 (74,4): coerente con l'assenza di cambiamenti
lato render. Summary-set: `results/v28-chain5/summary-set.json`. Le run usano lo stesso payload
immutabile ma materializzano mix diversi (plan_kinds nei summary): il confronto
è corretto solo accanto al mix, secondo la metrica per classe. Nota topology:
le run v20–v24 girano su **3 lane** (`gpu_lanes: 3`, 3 daemon, certificazione
`vramprobe` 23/10); il gate storico era definito su 2 lane. Confondente da
dichiarare: durante queste run altri processi lavoravano sulla macchina (5
sessioni di sviluppo concorrenti), condizione non prevista dal contratto di
misura («nessun altro carico pesante»).

Mappe (telemetria `Chronon dynamic map ready`): 12–40 s/render a tile-cache
calda/cold (v22 map 3: 39,8 s con 4.147 tile miss). **Rerun a cache-hit
dimostrato live: 13:41:55, mappa 192 frame servita da
`dynamic-map-video-cache` in `process_wall_s=0,01` contro 18,5 s del primo
render.** La classe mappa resta quindi costosa solo al primo render; il rerun
è ≤ costo immagine.

## Diagnosi live senza mappe — 28/10/2026

Il job `gate-90-nomaps-20261028-r1` è riuscito, ma **non è una run Gemma 2B**:
il payload effettivamente sottomesso usava `gemma4:e4b`; la copia successiva
del workload passa a `gemma4:e2b`. Risultato: 69 overlay (50 frasi, 8 immagini/
schede e 7 numeri), una composizione manifest da 68 item e un render batch
Chronon da **647,4 s**. La finestra osservata è **657,3 s**, pari a **6,3
overlay/min**. Chronon ha riportato **5,96 fps** con target 24 fps. La misura
indica che il costo dominante di questa run è la singola timeline renderizzata
su una lane; cambiare modello riduce la generazione testuale, ma non quel
render.

La CLI `goldensummary` ora riconosce le righe analytics `manifest:<digest>` come
tentativi batch, le esclude dalle statistiche per-overlay e le abbina al batch
del job. Prima le classificava come `unknown`, mescolando 647 s di composizione
nelle statistiche per-item. La finestra e il tempo batch restano misure distinte.

Workload aggiornato: `../overlay_throughput_golden_90_20261028.v1-nomaps.generate.json`
(`gemma4:e2b`, mappe disabilitate). Runner: `run_golden_nomaps.sh <label>`.
Il modello è stato scaricato sulla macchina e la prima chiamata locale breve è
terminata in **16,57 s a freddo**; è solo uno smoke test di disponibilità, non
una misura comparativa della generazione di 20 scene.

Per questo modello PipelineGen ammette tre chiamate Ollama, ma Ollama era
configurato con `OLLAMA_NUM_PARALLEL=1`. La configurazione live e il template
systemd in `scripts/systemd/ollama.service.d/gpu.conf` ora usano entrambi **3**;
Ollama è stato riavviato a job inattivi. Su due workload E2B comparabili da 20
scene, le 20 risposte modello sono scese da **48,2 s a 32,4 s** (−33%). Il job
completo senza render è sceso da **205,9 s a 182,5 s** (−11%); la fase `generate`
è scesa da circa **118 s a 94 s**. Le altre fasi restano: voiceover ~29 s e
compilazione audio ~60 s. Un successivo job con `audio.mode=NONE`, zero overlay
e zero rendering è riuscito in **147,5 s**, 20 scene, senza TTS né renderer;
`generate` valeva il 90% del critical path. Le misure sono salvate in
`results/gemma4-e2b-generation-20scene-20261028-r1/`,
`results/gemma4-e2b-generation-20scene-20261028-r2-parallel3/` e
`results/gemma4-e2b-generation-20scene-audionone-20261028-r2/`.

Il workload aveva istruzioni contraddittorie (1.900 parole nei parametri, ma
2.700–3.000 nel prompt): il prompt ora è allineato a 1.900, massimo 2.300,
20 segmenti da circa 95 parole. La run pulita `audio.mode=NONE` ha comunque
prodotto **3.186 parole**: Gemma E2B non rispetta ancora il target. Un `max_words`
rigido a 115 ha fallito sia con minimo 75 (scena 3: 56 parole) sia con minimo
55 (scena 1: 41 parole). Il cap è quindi rimosso dal workload; non c’è ancora
un controllo robusto del budget per questa taglia di modello. E2B è disponibile
e più rapido, ma la lunghezza del copione va ancora verificata prima della
pubblicazione.

Il collo di bottiglia della pubblicazione completa resta Chronon: una timeline
manifest da 68 overlay ha richiesto **647,4 s** a **5,96 fps** contro 24 fps.
La composizione attuale produce un artefatto unico per piano e usa una lane.
Per ridurre quei minuti bisogna cambiare il contratto: dividere la timeline in
chunk, renderizzare i chunk sulle lane disponibili e unirli preservando mapping
temporale e manifest. Il cambio di modello non risolve questo costo.

## Split render per-scena live — 29/10/2026 (flag OVERLAY_RENDER_PER_SCENE=enabled)

**Risultato: la finestra overlay passa da 657,3 s a 101,3 s (−85%, ×6,5; 6,3 → 39,7 overlay/min).**

Setup: gate abilitato via `refactored/start_server.sh` (export default-only,
un chiamante esplicito vince); `bin/pipelinegen` ricostruito con `go build`
diretto — `make build-server` resta bloccato dal gate preesistente
`rust-fetch` (BrainRust pinna ICU 72, host espone ICU 70);
`pipelinegenctl restart-verify` PASS; env verificato nel processo attivo via
`/proc/<pid>/environ`.

Run `split-per-scene-20261029-r1`, payload e2b aggiornato
(`../overlay_throughput_golden_90_20261028.v1-nomaps.generate.json`), job
`job_1793272410632223240_b91456a9`, run `run_ddf249f1-6c6d-48fa-83bf-a119eaee58bf`:

| Metrica | Baseline flag OFF (`gate-90-nomaps-20261028-r1`) | Split ON (`split-per-scene-20261029-r1`) |
|---|---|---|
| Finestra overlay | 657,3 s | **101,3 s** |
| Throughput | 6,3 item/min | **39,7 item/min** |
| Composizioni | 1 timeline unica (647,4 s render) | 15 batch per-scena |
| Item | 69/69 COMPLETED | 67/67 COMPLETED |
| Wall job completo | ~13 min (e4b) | **243,1 s** critical path |

Dettagli: 15 batch, uno per scena, `worker_count_matches` su tutti; totale
render batch 100,5 s; `cache_outcome=not_applicable` ovunque; nessun
`overlay per-scene split failed / falling back` nel journal (zero fallback).
Il conteggio item differisce (67 vs 69: mix numeri/immagini dell'e2b), quindi
il confronto di throughput va letto per-classe: 50 text_phrase in entrambe le
run. Caveat di comparabilità: il baseline r1 girava su payload e4b; il
costo dominante (render manifest fused) è però indipendente dal modello.

Nota architetturale: con lo split, le due code ping pong di §9.3.2 non
scala linearmente col numero di item simultanei per frame; il residuo
principale è ora la coda (`queue_wait_ms` fino a ~20 s/batch) e la coda
`generate` (46,7% del critical path). Per il contratto del gate
(≥100 item/min, mediana di 3 run comparabili, §9.3.5-A5) la soglia NON è
costante giácca: 39,7 item/min è passo avanti grande ma sotto soglia.

Pin test: `render_queue_split_test.go` — lo split fallisce fail-closed senza
client del renderer (mai silentio fallback); suite
capabilities/scripts completa verde. WIP orfano
`runner_overlay_plan_branch_test.go` (API Runner obsolete, creato da altra
sessione alle 11:07) parcheggiato in
`internal/capabilities/scripts/.wip/` — da riscrivere o archiviare.

## Layout risultati

`results/<run>/` contiene:
- `submission.json` — risposta del submit (job_id, chiave),
- `job.full.json` — snapshot `GET /api/jobs/<id>/full` al termine,
- `summary.json` — esito, conteggi (pianificati/renderizzati/pubblicati),
  fasi mappa (`prefetch/prepare/encode`), upload Drive reali (`drive_publish`),
  scomposizione operazioni e note.

## Metriche attese per run

- `overlay_plan_items` / `overlay_render_items` (COMPLETED) / `overlay_links_drive`
- finestra `overlay_render` e throughput = item COMPLETED / minuto di finestra
- fasi mappa per item: `prefetch`, `prepare`, `encode` (chronon)
- costo reale upload Drive: `drive_publish` (component=drive) sulle delivery run
  dell'outbox, joinabili al job via `run_observability.job_id`
- wall per fase da `run_stage_observations`

Un overlay conta come valido solo quando è materializzato, renderizzato e
supera i controlli di output (contratto di misura nel ticket operativo).
