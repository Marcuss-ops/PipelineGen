# Obama extra batch — 13 novembre 2026

## Esito

- **2/2 video completati**
- **34/34 clip elaborati, caricati su Drive e indicizzati**
- Lingua finale delle tracce: **solo `en`**
- Destinazione Drive: cartella `Obama` (`1n4YRewXB7F7nC5QozS8RmTF2tBLFDfFr`)

| Video | ID YouTube | Clip | Hook | Asset | Job |
|---|---|---:|---:|---:|---|
| President Barack Obama in Conversation with Marc Maron \| WTF Podcast | `Hb2rKGfIOrM` | 12 | 8 | 20 | `job_1794568820633134608_df6ffa08` |
| Obama Candid Answers To Intern Questions — Full Video | `v8e5d8HOaMA` | 7 | 7 | 14 | `job_1794569607983229939_60e3ff1e` |

## Verifica

- **34/34** asset con `index_state=INDEXED`
- **34/34** con `has_embedding=true`
- **34/34** con `pending_outbox_events=0`
- **34/34** con tracce `["en"]`
- **34/34** con `drive_file_id` valorizzato
- **0** problemi di durata: clip principali ≤ 60s, hook ≤ 15s
- Tutte le finestre sono dentro la durata della sorgente (`4078s` e `620s`)
- `index_health=STALE` su tutti: warning noto e accettato, non blocca il retrieval

## Config e servizio

- `translate_clips` era stato limitato temporaneamente a `en` per il batch.
- Config originale ripristinata da `config.yaml.before-en-only-extra`; `cmp` identico.
- Servizio riavviato: `/ready=true`, PID **489063**, avviato `2026-11-13T11:43:09Z`.

## Nota operativa

Il primo backfill del video WTF è fallito perché il processo del runner non aveva esportato `PIPELINEGEN_MEDIA_POSTGRES_DSN`; `bin/admin` non vedeva quindi il PostgreSQL media e `CueWriter` risultava nil. I 20 clip erano già committati e caricati: il backfill è stato ripetuto con `.env` esportato, senza riscaricare né duplicare i clip, e tutti i 20 asset sono stati verificati.

Il video WTF aveva già **5 clip** da un batch precedente. Non sono state rimosse: le 20 nuove finestre sono distinte e si aggiungono a quelle esistenti.

## File del batch

- `ops/jobs/obama_extra_20261113/state.json` — stato completo, job, item e asset
- `ops/jobs/obama_extra_20261113/asset-verification.json` — verifica post-restart dei 34 asset
- `ops/jobs/obama_extra_20261113/BATCH-REPORT.json` — report del runner
- `ops/jobs/obama_extra_20261113/resume.log` — log del recupero video 1 e del video 2
- `ops/jobs/obama_extra_20261113/batch.log` — log del primo tentativo
