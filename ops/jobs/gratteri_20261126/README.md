# Gratteri — 18 hook (2026-11-26)

Batch di 18 finestre curate da 4 interviste a Nicola Gratteri, destinate alla
nuova cartella Drive `Clips/Saggezza/Gratteri`
(`1oW6xyqgA9Q-vb2K0eucM4bwCiZrwDqFS`), caricamento flat (`create_subfolder=false`).

## Scelta editoriale

- 4 sorgenti: `WdbdwtO2v0E` (6 clip), `CVUzdDyyPW8` (5), `a9Lr_OQJ984` (4),
  `NVtQAg7VK74` (3).
- Ogni clip è una singola dichiarazione autoconclusiva di 9–25 s: nessuna
  finestra duplicata, nomi unici, tutti dentro il gate durata 4–60 s.
- `require_transcript_ready=true`: la trascrizione (Whisper first, sottotitoli
  YouTube come fallback) è un requisito **hard** prima di committare/indicizzare.
  Un clip senza text track READY non viene pubblicato come successo silenzioso.
- `write_summary=true`, `force_keyframes=true`, `keep_audio=true`,
  `strategy=verify` (idempotente: clip ID `yt_<video>_<start>_<end>_v1`).

## Build

```bash
cd refactored/ops/jobs/gratteri_20261126
python3 build_payloads.py
```

Il builder fallisce su finestre duplicate, nomi duplicati, durate fuori range
o timestamp oltre la durata nota del video.

## Submit

```bash
cd refactored
./scripts/velox submit clips-process \
  --key gratteri-20261126 \
  --payload ops/jobs/gratteri_20261126/payloads/a9Lr_OQJ984.process.json \
  --json
./scripts/velox poll <job_id> --timeout 2h --interval 10s
```

`NVtQAg7VK74` è geo-bloccato ("available in Italy") da questo host: il job
fallisce in acquisizione. In quel caso serve un proxy italiano o il download
manuale su una macchina IT; le altre 15 clip non dipendono da quel video.

## Esito (2026-11-26)

**15/18 clip pubblicate** in `Saggezza/Gratteri`, tutte `INDEXED` con 10 tracce
testuali READY (italiano + 9 traduzioni) e embedding in `media_embeddings`.
Dettaglio per clip in `verification.json`.

| Video | Job | Esito |
| --- | --- | --- |
| `a9Lr_OQJ984` | `job_1795681891709299697_2c54fc3d` | SUCCEEDED, 4/4 |
| `WdbdwtO2v0E` | `job_1795682229664817792_8e35f312` | SUCCEEDED, 6/6 |
| `CVUzdDyyPW8` | `job_1795682229726725463_5ec01b46` | SUCCEEDED, 5/5 |
| `NVtQAg7VK74` | `job_1795682229810151977_a211cacf` | FAILED, 0/3 (`Video unavailable`, geo) |

Due problemi d'impianto incontrati e risolti:

1. **Cookie jar YouTube stale** (`/etc/pipelinegen/secrets/youtube.cookies.txt`):
   con i cookie yt-dlp vedeva solo storyboard (`Requested format is not
   available`). Il jar è stato svuotato (backup in
   `youtube.cookies.txt.bak-20261126-0835`); senza cookie i formati tornano.
   Se serve di nuovo l'accesso autenticato, va rigenerato un jar fresco.
2. **Fan-out traduzioni deduplicato**: `asset.text.materialize` usa
   `(type, correlation_id)` e tutti i segmenti di un'estrazione condividono il
   correlation id del job → viene accodato **un solo** materialize per job.
   Le clip restanti sono state completate con il backfill canonico idempotente:

   ```bash
   ./bin/admin text-tracks-backfill --source=youtube \
     --languages=it,en,de,es,pt-BR,fr,pl,ru,tr,id \
     --asset-ids='<csv>' --all --apply --json
   ```

   Nota: il parser accetta sia `--flag valore` sia `--flag=valore`
   (fix GRAT-03, `bin/admin` ricostruito).

## Verifica

- Job completati: `velox poll`, poi `media_assets` per i 15 ID in
  `verification.json`.
- Drive: 15 mp4 in `Saggezza/Gratteri`; i sidecar sottotitoli vanno nel
  destination dedicato `youtube_subtitles/<videoId>`.
- Text track: 10 READY per clip (`asset_text_tracks`), `index_state=INDEXED`,
  1 embedding per clip.
