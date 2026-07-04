# POST /api/script/generate-from-clips

Genera uno script compilation-style a partire da clip fornite nel payload. Ogni scena viene associata a una clip diversa in sequenza (top-10 style); il Google Doc risultante include un blocco **SpecScene JSON** canonico dove l'URL della clip risiede in `scene.bindings.clip.drive_link` (singleton, lived dentro l'oggetto `bindings` — non un array `drive_links` alla radice della scena).

## Request

```
POST /api/script/generate-from-clips
Authorization: Bearer <admin-token>
Content-Type: application/json
```

### Body

| Campo | Tipo | Default | Obbligatorio | Descrizione |
|---|---|---|---|---|
| `topic` | string | — | sì | Argomento dello script |
| `title` | string | — | no | Titolo dello script e del documento |
| `clip_ids` | array of strings | `[]` | sì | Lista degli ID delle clip da utilizzare |
| `intro_clips` / `intro_clip_ids` | array of strings | `[]` | no | Lista degli ID delle clip da forzare all'inizio come Intro |
| `tone` | string | `"documentary"` | no | Tono narrativo dello script (es. `"funny, humorous countdown"`, `"cinematic and energetic"`) |
| `guidelines` / `custom_prompt` / `style_instructions` | string | — | no | Prompt personalizzato per guidare il modello (es. stili di scrittura, focus comico o tecnico) |
| `language` | string | `"en"` | no | Lingua dello script (es. `"en"`, `"it"`) |
| `model` | string | `"gemma2:2b"` | no | Modello LLM per la generazione della prosa |
| `target_words` | int | `300` | no | Conteggio target delle parole |
| `num_clips` | int | `0` | no | Numero di clip/scene da usare davvero nel risultato finale |
| `segment_words` | int | `0` | no | Lunghezza target di ogni segmento, in parole |
| `segment_topics` | array of strings | `[]` | no | Lista ordinata di argomenti per i segmenti |
| `generate_document` / `generate_doc` | bool | `false` | no | Abilita la creazione del Google Doc |
| `save_to_db` | bool | `false` | no | Salva lo script generato nel database |
| `generate_voiceover` | bool | `false` | no | Abilita la generazione del voiceover per le scene dello script |
| `voiceover_group` | string | — | no | Nome della cartella logica sotto il root voiceover, letto dal DB seedato |
| `voiceover_folder_id` | string | — | no | Override esplicito della cartella voiceover; accetta sia ID sia URL Drive |

### Oggetto clip (alternativo a `clip_ids`)

| Campo | Tipo | Descrizione |
|---|---|---|
| `clip_id` | string | ID dell'asset o file |
| `title` | string | Titolo descrittivo della clip |
| `url` | string | URL completo del file su Drive |

Esempio:
```json
{
  "clip_id": "bbb3af30-beca-5cef-a43b-f6ccb5604531",
  "title": "Jackie Chan - Hollywood vs Hong Kong Action",
  "url": "https://drive.google.com/file/d/1-AR5J1fgSMonaWZSWH_q-21OdUdQgPKP/view"
}
```

## Response (async)

```json
{
  "ok": true,
  "job_id": "job_1782556920495507271_20131273",
  "status": "QUEUED",
  "status_url": "/api/jobs/job_1782556920495507271_20131273/full"
}
```

## Esempio completo

```bash
curl -X POST http://localhost:8000/api/script/generate-from-clips \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer test-admin-token-12345" \
  -d '{
    "topic": "Top 10 Craziest & Funniest Jackie Chan Moments",
    "title": "Top 10 Funniest Jackie Chan Moments",
    "language": "en",
    "tone": "funny, humorous and enthusiastic top 10 countdown",
    "guidelines": "Write an entertaining, funny, and high-energy Top 10 compilation countdown script. Highlight Jackie Chan comedy timing and stunts.",
    "model": "gemma2:2b",
    "target_words": 400,
    "generate_document": true,
    "generate_doc": true,
    "intro_clips": ["f457b501-143d-5283-82b2-1bc165eef998"],
    "clip_ids": [
      "f457b501-143d-5283-82b2-1bc165eef998",
      "e9455645-1a2c-550f-8aa0-d93dca9afe27",
      "bbb3af30-beca-5cef-a43b-f6ccb5604531"
    ]
  }'
```

## Struttura del Google Doc

Il documento Google Doc generato contiene sia il testo narrativo completo che il blocco **SpecScene JSON** canonico (vedi `internal/application/scripts/usecase/specscene_validator.go` per la validazione del wire shape; vedi `docs/api/ACTIVE_API_GENERATED.md` per il multi-endpoint consistency).

```html
<h1>Titolo script</h1>
<h2>SpecScene JSON</h2>
<pre>
{
  "version": 1,
  "scenes": [
    {
      "id": "scene-0",
      "index": 0,
      "text": "Testo narrativo della scena 0...",
      "kind": "intro",
      "bindings": {
        "clip": {
          "clip_id": "f457b501-143d-5283-82b2-1bc165eef998",
          "drive_link": "https://drive.google.com/file/d/.../view?usp=drivesdk"
        }
      }
    },
    {
      "id": "scene-1",
      "index": 1,
      "text": "Testo narrativo della scena 1...",
      "kind": "clip",
      "bindings": {
        "clip": {
          "clip_id": "e9455645-1a2c-550f-8aa0-d93dca9afe27",
          "drive_link": "https://drive.google.com/file/d/.../view?usp=drivesdk"
        }
      }
    }
  ]
}
</pre>
```

Ogni scena include:
- `kind`: Ruolo visivo/narrativo (`intro`, `outro`, `clip`, `narration`). La prima scena viene automaticamente contrassegnata come `intro`.
- `text`: Testo narrativo della prosa generata dal modello (canonical V1 `ModelScriptOutputV1.text`).
- `bindings`: Bag tipizzata degli artifact producers che hanno contribuito alla scena. Possibili sotto-binding:
  - `bindings.clip`: `{ "clip_id": "...", "drive_link": "https://..." }` — singleton URL della clip associata a questa scena (NON un array).
  - `bindings.image`: `{ "url": "https://...", "status": "generated" | "failed" | "pending" }` — popolato dal `ImageProcessor` quando `output.generate_scene_images=true`.
  - `bindings.voiceover`: `{ "status": "completed" | "failed", "link": "https://..." }` — popolato dal `VoiceoverProcessor` quando `output.generate_voiceover=true`.
  - `bindings.stock`: `{ "url": "https://..." }` — popolato dal `StockAssociationProcessor` quando Qdrant matcha uno stock alla scena.

> **Canonical-SSOT discipline (godlike/06 / godlike/07)**: il wire shape qui sopra è l'unica forma accettata dai nuovi writer. I vecchi campi alla radice della scena (`description`, `drive_links` come array) sono **legacy** e sono stati rimossi dal contratto canonico a partire dalla PR6 (June 2026). Se devi consumare scene da un sistema esterno che ancora emette la forma legacy, converti via `internal/application/scripts/usecase/specscene_validator.go::ValidateAndEnrichSpecScene` prima di scriverle nel `SpecScene` envelope canonico. Per la corrispondente chiusura del bug P0 sulla persistenza (commit `d17c78ae`) vedi `CHANGELOG.md` §Unreleased.

## Voiceover

Quando `generate_voiceover=true`, il flusso usa il voiceover root configurato e crea una sottocartella per lo script corrente:

1. Se il payload passa `voiceover_folder_id`, quello ha priorità.
2. Se `voiceover_group` è presente, il sistema prova a risolverlo come nome cartella sotto il root voiceover.
3. Se non c’è un override, usa il root voiceover configurato.
4. In tutti i casi la destinazione finale crea una subfolder basata sul titolo dello script, quindi basta passare il nome logico della cartella già presente nel DB, oppure l’URL Drive del root.

I root Drive accettano sia ID sia URL del tipo `https://drive.google.com/drive/folders/<id>?usp=drive_link`.
Le mappature dei gruppi voiceover sono seedate in `migrations/sqlite/037_seed_voiceover_categories.sql`, quindi i folder ID non vanno hardcoded nel codice applicativo.

## Come funziona internamente

1. **Handler** (`handler_legacy_adapters.go`): parsifica il payload, mappa `intro_clips`, `tone` e le `guidelines` (o `custom_prompt`) nella struttura canonica `GenerationEnvelopeV2`.
2. **Source resolver** (`source_resolver_clips.go`): carica i metadati delle clip dal DB (tramite `ClipsRepository.GetClip`), costruisce `ClipEvidence` includendo `DriveLinks` e `ClipNames`.
3. **Engine** (`engine.go`): chiama il modello LLM con il topic, le linee guida di stile e le informazioni sulle clip per generare la prosa.
4. **DocumentProcessor** (`processor_document.go` & `generation_html.go`): genera il Google Doc popolando il blocco JSON strutturato con ruoli `kind` ed i descrittori reali delle clip.

## Note

- Le clip devono esistere nel DB (`media_assets` table / Qdrant) per essere risolte — il `clip_id` deve corrispondere a un asset presente
- L'URL dentro `scene.bindings.clip.drive_link` viene dal campo `drive_link` dell'asset nel DB (popolato dal `ClipBindingsProcessor` leggendo `ClipEvidence.DriveLinks[clip_id]`); se manca, la clip non viene associata e il `bindings.clip` rimane vuoto a parte `clip_id`.
- Lo script è generato dal modello Ollama (`gemma4:e4b` di default) — il modello non vede le clip, genera solo testo libero; l'associazione scene→clip avviene dopo
- `clip_ids` (array di stringhe) è supportato come alternativa a `clips` per retrocompatibilità
