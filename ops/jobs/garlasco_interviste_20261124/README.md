# Garlasco interviste — batch 2026-11-24

10 interviste YouTube sul caso Garlasco, con 154 finestre curate a mano:
98 clip principali e 56 hook. Il batch è la continuazione del pool
`Garlasco_TG_Interviste_Stock_20x60s_5s`.

## Destinazione Drive

| Campo | Valore |
|---|---|
| Cartella padre | `1c-pjyGbD56VzwycK8N0yqvycjKpnPrPM` |
| Nome cartella | `Garlasco_TG_Interviste_Stock_20x60s_5s` |
| Sottocartella batch | `Interviste_20261124` |
| `create_subfolder` | `true` |

La sottocartella viene creata dal worker al primo upload, quindi i 154 clip
non si mescolano ai 572 file già presenti nel pool. Se si vuole pubblicare
direttamente nella cartella padre, impostare `create_subfolder: false` e
rimuovere `subfolder_name` nei payload.

## Sorgenti

| # | Video ID | Programma | Main | Hook |
|---|---|---|---:|---:|
| 01 | `3L6fqRn2WrA` | TG1 | 21 | 11 |
| 02 | `xxpJfbMG7Oo` | TG1 | 8 | 5 |
| 03 | `HqshVryJ6lg` | Vita in diretta | 7 | 4 |
| 04 | `zldJjKZqbZE` | Approfondimento | 9 | 5 |
| 05 | `jdr5-Qx479g` | Storie italiane | 8 | 5 |
| 06 | `YJ6NTtHPEt8` | Intervista | 5 | 4 |
| 07 | `Koa43BvVe2w` | Segreti | 10 | 5 |
| 08 | `sf5ZrrVqY3Q` | Approfondimento | 11 | 5 |
| 09 | `Bu3wqhZPR48` | Intervista | 10 | 6 |
| 10 | `nnF8Z2o27u8` | Intervista | 9 | 6 |

Il blocco finale del testo incollato ripeteva `xxpJfbMG7Oo` e
`HqshVryJ6lg`: sono stati deduplicati per `video_url`.

## Mapping sul contratto `POST /api/clips/process`

Il testo di partenza usava `{video_url,title,clips[],hook_clips[]}`. Il
builder converte ogni voce in un `Segment`:

- `start` / `end` normalizzati a `HH:MM:SS`
- `name` = `topic`
- `summary` = `summary`
- `topics` = `[vector_description, topic]`
- `texts[0]` = `{language_code: "it", title: topic, summary, description: vector_description}`
- `category` = `garlasco_interview`
- `source_title` / `source_channel` = titolo e programma
- `tags` = Garlasco, Chiara Poggi, Andrea Sempio, programma, `intervista`, `hook` per gli hook
- `mentioned_people` / `speakers` dedotti dai nomi presenti nel testo
- `hook` = summary solo per gli hook
- `quality_score` = 1.0 main, 0.9 hook
- `search_visibility` = `high`

Top-level: `strategy: verify`, `force_keyframes: true`, `keep_audio: true`,
`write_summary: true`, `concurrency: 2`.

Le clip principali arrivano a 118 s; il server ha
`VELOX_YOUTUBE_MAX_SEGMENT_DURATION_SECONDS=600`, quindi passano il gate
4–600 s. Gli hook sono tutti sotto i 16 s.

## Build e validazione

```bash
cd refactored/ops/jobs/garlasco_interviste_20261124
python3 build_payloads.py
```

Il builder scrive `payloads/*.process.json` e `manifest.json`, e fallisce se
trova finestre duplicate, nomi duplicati, durate fuori range o metadati
troppo poveri.

## Submit

Canary consigliato: `06_YJ6NTtHPEt8` (9 clip, il più piccolo).

```bash
cd refactored
./scripts/velox submit clips-process \
  --key garlasco-interviste-20261124 \
  --payload ops/jobs/garlasco_interviste_20261124/payloads/06_YJ6NTtHPEt8.process.json \
  --json
./scripts/velox poll <job_id> --timeout 2h --interval 10s
```

Per l'intero batch:

```bash
cd refactored/ops/jobs/garlasco_interviste_20261124
./submit_all.sh
```

`submit_all.sh` usa `velox submit` (che deriva una chiave di idempotenza
deterministica dal progetto + hash del payload) e `velox poll`, quindi un
re-run non duplica i job già inviati con lo stesso payload.

Se il batch è già stato inviato, non rilanciare `submit_all.sh`: i job
correnti sono elencati in `jobs_all.tsv`. Per controllarne uno:

```bash
cd refactored
./scripts/velox poll <job_id> --timeout 2h --interval 20s
```

## Note operative

- Il batch scarica solo le finestre richieste (staging a blocchi), non i
  video interi.
- Il worker carica su Drive e committa `media_assets` / `asset_text_tracks`
  in modo atomico; un upload Drive fallito non è un successo silenzioso.
- Se un video è age-restricted o non disponibile, il job fallisce con
  classificazione terminale/retryable: partire dal canary per verificarlo.
