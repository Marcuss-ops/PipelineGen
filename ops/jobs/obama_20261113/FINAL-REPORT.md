# Obama clips — batch 2026-11-13

## Esito

- Video completati: **11/12**
- Clip indicizzate: **63** (asset unici: 63)
- Lingue tracce: **en** (solo inglese)
- Stato indice: **INDEXED**
- Embedding: **63 / 63**
- Outbox pending: **0**
- Riferimenti Drive: **63 / 63**
- Problemi di durata/specifica: **0**
- Video 8: **blocked** — sorgente YouTube non disponibile (`yt-dlp: Video unavailable`), 0/6 segmenti processati

Cartella Drive: https://drive.google.com/drive/folders/1n4YRewXB7F7nC5QozS8RmTF2tBLFDfFr

## Video

| # | Video ID | Titolo | Job | Clip | Stato |
|---:|---|---|---|---:|---|
| 1 | `Hb2rKGfIOrM` | — | `job_1794560922193535111_d512a6b2` | 5 | done |
| 2 | `uI-hgSE5QIw` | Barack Obama BREAKS SILENCE on Trump's ape video, Bad Bunny, and 20... | `job_1794561796575740731_8723aa79` | 5 | done |
| 3 | `2TtdPbeKNFc` | President Obama and Jimmy Had an Awkward First Meeting | `job_1794562025368252615_073f50e5` | 4 | done |
| 4 | `3dZ2sYvpjCg` | Barack Obama's 2006 Interview \| Late Night with Conan O'Brien | `job_1794562208416725733_b77e251e` | 5 | done |
| 5 | `wuECw_UB0P4` | President Barack Obama (Aired: 07/29/2010) | `job_1794562437348742787_aed1eb10` | 5 | done |
| 6 | `UnW3xkHxIEQ` | President Barack Obama: Between Two Ferns with Zach Galifianakis | `job_1794562707961697024_cb2787a6` | 5 | done |
| 7 | `X0a4GHqOlOg` | Barack, Michelle Obama speak out in 1st joint interview since leavi... | `job_1794562905156848235_b76dc53b` | 5 | done |
| 8 | `mgfO4nnPL3Y` | A Conversation with Barack Obama | — | 0 | **blocked** |
| 9 | `xOl7HTXuLvk` | Barack Obama shares personal stories & experiences: Family, leaders... | `job_1794564824349801928_e730f91c`<br>fix `job_1794566907752914156_0a23bf5d` | 7 | done |
| 10 | `NxFkEj7KPC0` | Obama out: President Barack Obama's hilarious final White House cor... | `job_1794565622342088491_0ea8a8f7` | 7 | done |
| 11 | `XeQlFEkdQFo` | President Barack Obama Takes The Colbert Questionert | `job_1794565832388343036_13d15b9a` | 8 | done |
| 12 | `yvYCzh0BcmM` | EXCLUSIVE: Barack Obama in his own words \| MS NOW FULL INTERVIEW | `job_1794566071680297314_e29881f7`<br>fix `job_1794566970038827594_c5c8d8c5` | 7 | done |

## Correzioni fuori durata

Quattro segmenti iniziali superavano i limiti (2 clip >60s, 2 hook >15s). Sono stati ritagliati, reindicizzati in `en` e i vecchi asset sono stati spostati nel cestino Drive.

| Video | Vecchio asset | Durata vecchia | Nuovo asset | Durata nuova | Vecchio Drive (trash) | Nuovo Drive |
|---:|---|---:|---|---:|---|---|
| 9 | `yt_xOl7HTXuLvk_3060_3135_v1` | 75s | `yt_xOl7HTXuLvk_3060_3120_v1` | 60s | `1e3V8_iv7ygDxZUKJfjh1Dmn2ijbThuCE` | `1KwQFk7t15yPs6x8LD3P7ye2uEo60rZ4M` |
| 12 | `yt_yvYCzh0BcmM_2402_2475_v1` | 73s | `yt_yvYCzh0BcmM_2402_2462_v1` | 60s | `12Hw7qpu2bqSN32h36WSOnS5R0JV7Y6PY` | `1h7biHI7A48T3RDCpRk5oYEh7ZasxHRv9` |
| 12 | `yt_yvYCzh0BcmM_39_55_v1` | 16s | `yt_yvYCzh0BcmM_39_54_v1` | 15s | `1PT-bw0TTxA-mMi6dW043kgb0sOpw1DqH` | `1QKijiLM0yVoEwqi2ueJ9jXTjh-8GILHc` |
| 12 | `yt_yvYCzh0BcmM_2402_2439_v1` | 37s | `yt_yvYCzh0BcmM_2402_2417_v1` | 15s | `1PqRpZ07hOgVs5tO7o1NxHhtTKPyC_bVr` | `1SP95H6ZLyawKXw87rCPLZqmGlTNXVR42` |

## Verifica e servizio

- Backfill `text-tracks-backfill --languages=en` eseguito su tutti gli asset; 0 failure, tracce finali solo `en`.
- Verifica API: `index_state=INDEXED`, `has_embedding=true`, `pending_outbox_events=0`, `text_tracks=[en]` su tutti i 63.
- `index_health=STALE` su tutti gli asset: warning noto e accettato; non blocca il retrieval.
- Config `translate_clips` ripristinata dal backup `config.yaml.before-en-only` (diff vuoto).
- Servizio riavviato; `/ready` = **True**, PID **420698**, avviato **2026-11-13T11:02:03Z**.

## Artefatti

- `state.json` — stato per video, job, asset IDs, correzioni.
- `asset-verification.json` — verifica completa dei 63 asset.
- `fix-results.json` — job e asset dei 4 ritagli correttivi.
- `batch.log`, `fix.log` — log operativi.

> Nota: il video 8 non è stato caricato perché YouTube lo restituisce come `Video unavailable`; non è un errore di transcodifica o di indice. Gli altri 11 video sono completi.
