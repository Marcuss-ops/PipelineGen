# Obama Pivot / MS NOW — 13 novembre 2026

## Esito

- **2/2 video completati**
- **36 nuovi asset** creati, caricati su Drive e indicizzati
- **14 asset preesistenti** dei due video verificati e lasciati invariati
- Lingua delle tracce: **solo `en`**
- Destinazione Drive: cartella `Obama` (`1n4YRewXB7F7nC5QozS8RmTF2tBLFDfFr`)

| Video | ID YouTube | Nuovi clip | Nuovi hook | Asset nuovi | Momenti saltati per copertura esistente | Job |
|---|---|---:|---:|---:|---:|---|
| The Pivot | `xOl7HTXuLvk` | 8 | 9 | 17 | 2 | `job_1794571383277272172_8f57fe99` |
| MS NOW | `yvYCzh0BcmM` | 9 | 10 | 19 | 1 | `job_1794571966321577265_7f867aa3` |

Totale per i due video: **50 asset** (14 preesistenti + 36 nuovi).

## Scelte di taglio e deduplica

Tre momenti erano gia coperti da asset esistenti e non sono stati duplicati:

- `xOl7HTXuLvk` **16:00-16:39** — coperto da `yt_xOl7HTXuLvk_940_1000_v1` (15:40-16:40)
- `xOl7HTXuLvk` **50:52-52:40** — coperto da `yt_xOl7HTXuLvk_3060_3120_v1` (51:00-52:00)
- `yvYCzh0BcmM` **05:38-06:14** — coperto da `yt_yvYCzh0BcmM_338_375_v1` (05:38-06:15)

Gli hook nuovi sono stati inclusi **tutti**, anche quando ricadono dentro un momento gia coperto.

Tre finestre oltre i 60s sono state accorciate conservando lo start:

- `xOl7HTXuLvk` 10:04-11:09 → **10:04-11:04** (60s)
- `xOl7HTXuLvk` 54:09-55:22 → **54:09-55:09** (60s)
- `yvYCzh0BcmM` 31:09-32:15 → **31:09-32:09** (60s)

Tutte le finestre sono dentro la durata sorgente (`3347s` e `2651s`); hook tutti ≤15s.

## Verifica post-restart

- **36/36** nuovi asset con `index_state=INDEXED`
- **36/36** con `has_embedding=true`
- **36/36** con `pending_outbox_events=0`
- **36/36** con tracce `["en"]`
- **36/36** con `drive_file_id` valorizzato
- **14/14** asset preesistenti ancora `INDEXED`, con embedding e senza eventi pendenti
- **0 problemi** rilevati
- `index_health=STALE` sui nuovi asset: warning noto e accettato, non blocca il retrieval

## Config e servizio

- Config multilingua ripristinata da `config.yaml.before-en-only-pivot`; `cmp` identico.
- Servizio riavviato: `/ready=true`, PID **585518**, avviato `2026-11-13T12:23:28Z`.
- Durante il batch il servizio è stato riavviato due volte (12:04:45 e 12:17:18). Entrambi i job sono passati in `RETRY_WAIT`, sono ripartiti e hanno completato correttamente, senza perdita di clip.

## File del batch

- `ops/jobs/obama_pivot_msnow_20261113/manifest.json` — 36 segmenti, note di deduplica e tagli
- `ops/jobs/obama_pivot_msnow_20261113/preflight.json` — validazione durate e sorgente
- `ops/jobs/obama_pivot_msnow_20261113/state.json` — job, item e asset
- `ops/jobs/obama_pivot_msnow_20261113/BATCH-REPORT.json` — report del runner
- `ops/jobs/obama_pivot_msnow_20261113/asset-verification.json` — verifica post-restart
- `ops/jobs/obama_pivot_msnow_20261113/batch.log` — log del batch

Nessun commit o push effettuato.
