# POST /api/script/generate-from-clips

Genera uno script compilation-style a partire da clip fornite nel payload. Ogni scena viene associata a una clip diversa in sequenza (top-10 style); il Google Doc risultante mostra solo il JSON strutturato con i `drive_links` per scena.

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

Il documento Google Doc generato contiene sia il testo narrativo completo che il blocco **SpecScene JSON** formattato:

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
      "text": "Testo narrativo della scena...",
      "kind": "intro",
      "description": "Titolo/Descrizione reale estratta dai metadati della clip",
      "drive_links": ["https://drive.google.com/file/d/.../view?usp=drivesdk"]
    },
    {
      "id": "scene-1",
      "index": 1,
      "text": "Testo narrativo della scena 1...",
      "kind": "clip",
      "description": "Descrizione reale della seconda clip",
      "drive_links": ["https://drive.google.com/file/d/.../view?usp=drivesdk"]
    }
  ]
}
</pre>
```

Ogni scena include:
- `kind`: Ruolo visivo/narrativo (`intro`, `outro`, `clip`, `narration`). La prima scena viene automaticamente contrassegnata come `intro`.
- `description`: Titolo e descrizione reali recuperati dai metadati dell'asset in SQLite / Qdrant.
- `drive_links`: Array contenente gli URL Google Drive per il download diretto del media.

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
- I `drive_links` vengono dal campo `drive_link` dell'assest nel DB — se manca, la clip non viene associata
- Lo script è generato dal modello Ollama (`gemma4:e4b` di default) — il modello non vede le clip, genera solo testo libero; l'associazione scene→clip avviene dopo
- `clip_ids` (array di stringhe) è supportato come alternativa a `clips` per retrocompatibilità
