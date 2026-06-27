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
| `clips` | array of objects | `[]` | sì | Clip da associare alle scene (vedi sotto) |
| `num_clips` | int | `0` | no | Numero di clip/scene da usare davvero nel risultato finale |
| `language` | string | `"en"` | no | Lingua dello script |
| `style` | string | `"documentary"` | no | Stile narrativo |
| `segment_words` | int | `0` | no | Lunghezza target di ogni segmento, in parole |
| `segment_topics` | array of strings | `[]` | no | Lista ordinata di argomenti per i segmenti |
| `generate_document` | bool | `false` | no | Crea Google Doc |
| `generate_doc` | bool | `false` | no | Alias di `generate_document` |
| `sentences_per_image` | int | `6` | no | Lunghezza media scena |
| `min_quality_score` | float | `0.5` | no | Qualità minima (non usato) |
| `enable_scene_images` | bool | `false` | no | Genera immagini AI per scena |
| `extract_entities` | bool | `false` | no | Estrai entità (persone/luoghi) |
| `generate_metadata` | bool | `false` | no | Genera metadata video |

### Oggetto clip

| Campo | Tipo | Descrizione |
|---|---|---|
| `clip_id` | string | ID del file su Google Drive |
| `title` | string | Titolo descrittivo della clip |
| `url` | string | URL completo del file su Drive |

Esempio:
```json
{
  "clip_id": "1HJX8AiYk-BlhkKqly51GNtSyd8ttf3oG",
  "title": "Jackie Chan - Ordering Breakfast in LA",
  "url": "https://drive.google.com/file/d/1HJX8AiYk-BlhkKqly51GNtSyd8ttf3oG/view"
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
    "topic": "Jackie Chan kung fu philosophy",
    "clips": [
      {
        "clip_id": "1HJX8AiYk-BlhkKqly51GNtSyd8ttf3oG",
        "title": "Ordering Breakfast in LA",
        "url": "https://drive.google.com/file/d/1HJX8AiYk-BlhkKqly51GNtSyd8ttf3oG/view"
      },
      {
        "clip_id": "14AxeNGtrlzgHtz3gx5vECjmmluRbtd2R",
        "title": "Airplane Misunderstanding",
        "url": "https://drive.google.com/file/d/14AxeNGtrlzgHtz3gx5vECjmmluRbtd2R/view"
      }
    ],
    "language": "en",
    "generate_document": true,
    "generate_doc": true
  }'
```

## Struttura del Google Doc

Il documento contiene solo:

```
<h1>Titolo script</h1>
<h2>SpecScene JSON</h2>
<pre>
{
  "version": 1,
  "scenes": [
    {
      "id": "scene-1",
      "index": 0,
      "text": "Testo narrativo...",
      "kind": "narration",
      "drive_links": ["https://drive.google.com/file/d/.../view?usp=drivesdk"]
    },
    ...
  ]
}
</pre>
```

Ogni scena ha un array `drive_links` con l'URL della clip a cui è associata. Le clip vengono assegnate in ordine alfabetico per `clip_id`, ciclando se ci sono più scene che clip.

## Come funziona internamente

1. **Handler** (`handler_legacy_adapters.go`): parsifica il payload, estrae i `clip_id` dalle `clips`
2. **Source resolver** (`source_resolver_clips.go`): carica i metadati delle clip dal DB (tramite `ClipsRepository.GetClip`), costruisce `ClipEvidence` con `DriveLinks` map
3. **Engine** (`engine.go`): chiama Ollama con il topic + testo delle clip, genera lo script
4. **ClipBindingsProcessor** (`processor_clip_bindings.go`): postprocessor che assegna ogni scena a una clip, popolando `scene.Bindings.Clip` con `clip_id` e `drive_link`
5. **DocumentProcessor** (`processor_document.go`): genera il Google Doc, renderizza il JSON con `drive_links` invece di `bindings`

## Note

- Le clip devono esistere nel DB (`media_assets` table / Qdrant) per essere risolte — il `clip_id` deve corrispondere a un asset presente
- I `drive_links` vengono dal campo `drive_link` dell'assest nel DB — se manca, la clip non viene associata
- Lo script è generato dal modello Ollama (`gemma4:e4b` di default) — il modello non vede le clip, genera solo testo libero; l'associazione scene→clip avviene dopo
- `clip_ids` (array di stringhe) è supportato come alternativa a `clips` per retrocompatibilità
