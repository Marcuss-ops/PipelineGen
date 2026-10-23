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

Baseline (1 lane, mediana v8/v9/v10): **27,2 item/min**. Dopo la seconda lane
(v11+v12): **37,8 e 41,7 item/min** (+39…+53%, overlap render 1,89×, nessuna
regressione).

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
