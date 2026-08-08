# Batch Drive Clip Indexer — Runbook operativo

End-to-end: nuovi clip su Google Drive → indicizzati in `refactored`'s `media_assets` via
manifest JSON strict-validated. Nessun nuovo codice Go viene scritto: si riusa
esattamente `cmd/admin/index-drive-clip` per ogni manifest. Il wrapper bash fa
pre-build del binario per evitare recompile per ogni singolo clip.

> Vincoli del codice che guidano il design:
> - `cmd/admin/manifest.go` usa `disallowUnknownFields` → qualunque campo extra
>   nel manifest viene rifiutato dal decoder.
> - `cmd/admin/manifest.go::Validate()` fallisce chiuso su qualunque campo
>   richiesto mancante (`drive_file_id`, `name`, `description`, `tags`,
>   `source`, `category|local_subdir`, `group`).
> - `cmd/admin/index_drive_clip.go::resolveClipDuration` fallisce chiuso se
>   `ffprobe` fallisce E non hai passato `--allow-declared-duration`.
> - `cmd/admin/drive_reconcile.go` è l'unico writer canonico per
>   `drive_folder_catalog` con `source='discovered'`.
> - `cmd/admin/index-drive-clip` è idempotente su `asset_id = drive_file_id`:
>   rilanciare dopo un successo parziale è sicuro.

## 1. Prerequisiti una-tantum

```bash
# (a) configurazione Go + secrets Drive + cfg runtime vedi ARCHITECTURE.md
# (b) jq per il pre-check JSON sintattico del wrapper:
which jq || sudo apt-get install -y jq
# (c) sqlite3 binary (per ispezionare il DB locale data/media/media.db.sqlite):
which sqlite3 || sudo apt-get install -y sqlite3
```

## 2. Step 0 — pre-flight del folder Drive (catalog)

Fai un dry-run, poi applica. Questo popola `drive_folder_catalog` PRIMA che i
tuoi nuovi manifest provino a scaricare da Drive (alcuni path resolver
controllano la presenza del catalog).

```bash
cd refactored
go run ./cmd/admin drive-reconcile --root "<ROOT_FOLDER_ID>"          # dry-run
go run ./cmd/admin drive-reconcile --root "<ROOT_FOLDER_ID>" --apply  # scrive catalog
go run ./cmd/admin drive-reconcile --root "<ROOT_FOLDER_ID>" --apply --sync-assets
                                                                     # aggiunge entry da media_assets
```

Aspettati output tipo: `Status: 87 discovered, 3 asset-synced, 0 errors`.

## 3. Step 1 — preparazione manifest

Per ogni nuovo clip che vuoi indicizzare:

```bash
cd refactored
cp cmd/admin/manifests/_TEMPLATE.json cmd/admin/manifests/<slug>.json
# apri <slug>.json e sostituisci TUTTI i campi "REPLACE_*"
```

Validazione locale in due click prima di passare al batch:

```bash
# Sintassi JSON:
jq -e . cmd/admin/manifests/<slug>.json

# Validazione completa del manifest loader (richiede il package cmd/admin):
go run ./cmd/admin index-drive-clip --manifest cmd/admin/manifests/<slug>.json
# Se la validazione fallisce, vedrai i campi mancanti nel log prima di
# qualunque chiamata Drive.
```

Per uno schema canonico osserverai questo formato (uguale a `beluga.json` e
`stargazer-fish.json`):

| campo                     | required | note                                                           |
| ------------------------- | -------- | -------------------------------------------------------------- |
| `drive_file_id`           | sì       | ID Drive del clip (44 caratteri, parte di `https://drive.google.com/file/d/<id>/view`) |
| `name`                    | sì       | titolo human-readable                                          |
| `description`             | sì       | paragrafo principale; entra in `SearchText`                    |
| `description_alt`         | no       | variante localizzata (es. IT)                                  |
| `tags`                    | sì (≥1)  | lista non-vuota, anche in `SearchText`                         |
| `source`                  | sì       | enum atteso: `clip_drive`, `ai_generated`, `stock`, `youtube_clip` |
| `category`                | sì*      | *serve `category` OPPURE `local_subdir`                       |
| `group`                   | sì       | es. `funny_animals`, `topfive_fish`                            |
| `local_subdir`            | no       | override di `category` per la directory di storage locale      |
| `default_filename`        | no       | usato SOLO se Drive restituisce `name=""` (raro)               |
| `duration_fallback_seconds` | no     | `> 0` richiede `--allow-declared-duration` su index-drive-clip |
| `metadata`                | no       | mappa `string→string`, valori `content_type`, `subject`, `visual_summary`, `timeline_json`, `hook`, `audio_policy`, `sound_design_plan` |

## 4. Step 2 — dry-run del batch

```bash
cd refactored
./scripts/batch_index_drive_clips.sh \
    --manifests-dir cmd/admin/manifests \
    --pattern "*.json" \
    --build                # forza una rebuild fresca del binario ./bin/admin
```

> `--build` **ricompila sempre**; senza `--build` il wrapper riusa il
> `./bin/admin` cachato (che deve esistere già). Il wrapper NON fa
> staleness check sui sorgenti Go: se modifichi `cmd/admin/*.go`, devi
> passare `--build` per vederle applicate.

Output atteso (DRY-RUN, exit 0):

```
=== Batch Drive Indexer ===
  manifests_dir    : cmd/admin/manifests
  pattern          : *.json
  mode             : DRY-RUN
  ...
— [cmd/admin/manifests/beluga.json]
   would run: './bin/admin index-drive-clip --manifest cmd/admin/manifests/beluga.json'
— [cmd/admin/manifests/<slug>.json]
   would run: ...
```

## 5. Step 3 — esecuzione reale

Modalità **continue-on-error** (default): anche se un manifest fallisce, il
wrapper tenta gli altri. Consigliata per run iniziali dove potresti avere
qualche metadata sbagliata.

```bash
./scripts/batch_index_drive_clips.sh --apply --build
```

Modalità **strict**: ferma tutto al primo errore. Consigliata quando sei
sicuro di voler fall-closed su qualunque problem (es. CI).

```bash
./scripts/batch_index_drive_clips.sh --apply --strict --build
```

Per manifest con `duration_fallback_seconds > 0` (es. beluga.json ha 9s):

```bash
./scripts/batch_index_drive_clips.sh --apply --allow-declared-duration --build
```

Per re-usare lo stesso manifest contro un Drive ID nuovo (es. hai rifatto
l'upload):

```bash
./scripts/batch_index_drive_clips.sh \
    --apply \
    --drive-file-id "<NUOVO_DRIVE_ID>" \
    --manifests-dir cmd/admin/manifests/<slug>.json
```

Output finale:

```
=== Summary ===
  manifests : 3
  indexed   : 2
  failed    : 1
  elapsed   : 12.456s
  next steps: fix the failing manifest(s) above and re-run with --apply.
```

(Rilanciare è sicuro: `index-drive-clip` è idempotente su `asset_id = drive_file_id`.)

## 6. Step 4 (opzionale) — re-embedding di massa

I nuovi asset sono indicizzati ma potrebbero non avere ancora embeddings
semantic/transcript. Per generarli in parallelo:

```bash
cd refactored
# fai partire il job media.reindex con limite (es. 1000) per non saturare la GPU:
go run ./cmd/... jobs reindex --source admin --limit 1000    # adatta comando al dispatcher corrente
# oppure programmaticamente via il job handler in
# internal/infrastructure/indexing/clipindexer/batch.go::HandleJob.
```

## 7. Note operative

- **Idempotenza**: `index-drive-clip` non duplica asset (EnqueueAndIndex upsert
  su `asset_id = drive_file_id`); `drive-reconcile` non duplica catalog rows.
  Rilanciare il wrapper dopo un parziale fallimento è quindi sicuro.
- **Re-run safety**: il Summary finale del wrapper lo dichiara esplicitamente
  per rassicurare l'operatore. La `--drive-file-id OVERRIDE` permette di
  re-usare un singolo manifest contro un Drive ID nuovo (es. dopo re-upload).
- **Fail closed**: non lanciare `--allow-declared-duration` a meno che il
  manifest non dichiari esplicitamente `duration_fallback_seconds > 0` (vedi
  validator strict). Altrimenti ottieni un errore con mention del flag.
- **Race**: due run concorrenti sullo stesso `drive_file_id` sono serializzati
  da `EnqueueAndIndex` ma possono produrre log rumoreggi. Evita di lanciare il
  wrapper in parallelo.
- **Cleanup**: i clip locali vengono salvati in
  `cfg.Storage.MediaDir/<local_subdir|category>`. Per liberare lo spazio,
  vedi gli eventuali job in `cmd/admin/cleanup_*` (non documentati in questo
  runbook per non fare overlap con procedure di storage esistenti).

## 8. Troubleshooting

| errore                                     | causa                                           | rimedio                                                      |
| ------------------------------------------ | ----------------------------------------------- | ------------------------------------------------------------ |
| `--manifest is required`                   | hai invocato `index-drive-clip` senza `--manifest` | usa `scripts/batch_index_drive_clips.sh` che lo passa sempre |
| `disallowUnknownFields: ...`               | hai aggiunto un campo custom al manifest        | rimuovilo: lo strict decoder rifiuta qualunque chiave extra  |
| `manifest: name is required`              | placeholder `REPLACE_*` non sostituito          | modifica il manifest                                         |
| `Drive file is missing or trashed`         | ID Drive sbagliato o file eliminato              | verifica con `scripts/list-drive-folder.ts`                  |
| `probe clip duration failed`               | `ffprobe` non disponibile o clip corrotto        | aggiungi `--allow-declared-duration` (solo se manifest ha `duration_fallback_seconds > 0`) |
| `outbox baseline read failed`              | DB locked / permessi                            | rilancia dopo qualche secondo                                |
