# Golden case — 100 overlay/minuto

Payload immutabile: `../overlay_throughput_golden_100_20261022.v2.generate.json`
(SHA-256 `60fb14ec1e6ace95489f38e2b84f91dd7a28d45d89edbcf1e5c060a52d63080d`).

Deriva dal job reale `08_fonti_media.generate.v11.json` (batch Garlasco 10 video):
20 segmenti, clip intro già selezionate, `final_job=false` (overlay-only),
riuso cache abilitato, refresh asset disabilitato, `max_overlays_per_scene=10`.
Il mix richiesto è 25 frasi brevi + 25 lunghe + 30 immagini (10 schede entità)
+ 10 date + 10 mappe = 100 overlay; il piano materializzato dipende dai
risultati live di ricerca e tipicamente consegna 37–41 item.

## Comando di ripetizione

```bash
cd refactored
scripts/submit-and-poll.sh \
  ops/jobs/overlay_throughput_golden_100_20261022.v2.generate.json \
  overlay-throughput-golden-100 \
  overlay-throughput-golden-vN-<etichetta>-$(date -u +%Y%m%dT%H%M%SZ) \
  3600
```

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
