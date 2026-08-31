# PipelineGen: invio corretto dei job `script.generate`

Questo è il percorso canonico per generare uno script da clip, voiceover,
timing, sottotitoli e clip localizzate. Il job è asincrono: la risposta al
`POST` conferma solo l'accodamento, non il completamento.

## Contratto minimo

Endpoint:

```text
POST http://127.0.0.1:8000/api/script/generate
```

Header obbligatori:

```text
Authorization: Bearer $VELOX_ADMIN_TOKEN
Content-Type: application/json
Idempotency-Key: <chiave-unica-per-il-run>
X-Request-ID: <request-id>
```

Il payload deve avere `version: 2`, `preset: custom` e almeno un elemento in
`items`. Per i job con voiceover è obbligatorio valorizzare `items[0].project`.

## Payload clip-only consigliato

```json
{
  "version": 2,
  "preset": "custom",
  "correlation_id": "margot-robbie-20260822-123000",
  "force_refresh": false,
  "items": [{
    "id": "margot-robbie-20260822-123000",
    "project": "margot-robbie-20260822-123000",
    "title": "Margot Robbie - 20 Funny Clips",
    "language": "en",
    "tone": "fast, playful and concise",
    "style": "Write a new short funny narrator intro for each matching clip. Use only the supplied description and transcript. Do not copy the source text. Do not invent facts. Keep each intro to one or two punchy sentences.",
    "source": {
      "type": "clips",
      "clip_ids": ["clip-id-1", "clip-id-2"],
      "intro_clip_ids": ["clip-id-1", "clip-id-2", "clip-id-3"],
      "num_clips": 20,
      "ordering_strategy": "input_order",
      "force_refresh": false
    },
    "script_params": {
      "target_words": 135,
      "segment_words": 30,
      "force_refresh": false,
      "segments": [
        {
          "id": "scene-00",
          "kind": "intro",
          "topic": "clean short topic",
          "source_text": "Only the factual clip description, without instructions.",
          "clip_ids": ["clip-id-1"],
          "target_words": 25
        }
      ]
    },
    "docs": {
      "enabled": true,
      "languages": ["en"],
      "folder_id": "GOOGLE_DOCS_FOLDER_ID"
    },
    "audio": {
      "mode": "COMBINED_TIMELINE",
      "mix_policy": "VOICEOVER_ONLY",
      "background_music": {
        "asset_id": "BACKGROUND_ASSET_ID",
        "start_ms": 0,
        "end": "video_end",
        "loop": true,
        "gain_db": -28
      }
    },
    "output": {
      "save_to_db": true,
      "generate_metadata": false,
      "extract_entities": "disabled",
      "generate_timeline": true,
      "render": {
        "enabled": true,
        "drive_folder_id": "RENDER_ROOT_FOLDER_ID",
        "drive_subfolder_name": "unique-run-folder",
        "watermark": {
          "enabled": true,
          "text": "TODAYLAUGHT",
          "position": "center",
          "opacity": 1
        }
      }
    }
  }]
}
```

### Regole importanti

- `source_text` deve contenere solo la descrizione della clip. Non inserire
  `Clip description:`, `Write...`, istruzioni, URL o JSON nel testo passato al
  modello. Le istruzioni stanno in `style`.
- Ogni segmento deve avere il proprio `clip_ids` e il proprio `source_text`.
- `intro_clip_ids` (legacy) identifica le 2–3 clip introduttive; non sostituisce
  `clip_ids`. Per intro/outro letterali usare il nuovo contratto `intro`/`outro`.
- **Intro/outro letterali (nuovo contratto)**: `items[0].intro` e `items[0].outro`
  sono sezioni **letterali non toccate dal LLM**. Solo `text` + `clip_ids` (1 o 2 clip)
  sono permessi. Il testo viene iniettato verbatim come prima/ultima
  `SpecScene` con `Kind=intro/outro`, mai riscritto da `source_text` o dai
  transcript. Esempio (2 clip intro + 2 clip outro come richiesto):

  ```json
  {
    "intro": { "text": "Welcome back — today 30 wild Matt Damon moments!", "clip_ids": ["yt_intro_123_v1", "yt_intro_124_v1"] },
    "outro": { "text": "Thanks for watching — see you next time!", "clip_ids": ["yt_outro_456_v1", "yt_outro_457_v1"] }
  }
  ```

  Regole: `text` deve passare `ValidateSpeakableText` (no URL, no marker), `clip_ids`
  1 o 2 ID esistenti nel catalogo, non duplicati con `source.clip_ids` o
  `segments[].clip_ids`, `intro`/`outro` non possono condividere clip,
  richiede `source.type` clip-bearing (`clips|search|catalog|curate`). La clip
  viene verificata in preflight e compilata nel timeline — nessuna riscrittura LLM.
- `output.render` crea esclusivamente le clip localizzate richieste. La
  creazione del video finale completo non appartiene al contratto
  `script.generate`.
- `audio.mix_policy` va dentro `items[0].audio`. Metterlo dentro
  `output.audio` non viene applicato al percorso canonico.
- Per un run ripetibile usare `force_refresh: false`. Usare `true` solo quando
  si vuole rigenerare esplicitamente testo/TTS/render.
- Il nome `drive_subfolder_name` deve essere unico per ogni run. Non riusare un
  nome già presente: Drive può restituire più cartelle omonime.
- Fingerprint/cache: `intro`/`outro` partecipano al `CacheKey`/`Fingerprint`.
  Cambiare `intro.text` o `intro.clip_ids` invalida la cache e genera un nuovo run.

## Invio e polling

```bash
set -a; . /etc/pipelinegen/pipelinegen.env; set +a
RUN_ID="margot-robbie-$(date -u +%Y%m%d-%H%M%S)"

curl -sS -X POST http://127.0.0.1:8000/api/script/generate \
  -H "Authorization: Bearer $VELOX_ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  -H "X-Request-ID: ${RUN_ID}-request" \
  -H "Idempotency-Key: ${RUN_ID}-request" \
  --data-binary @payload.json
```

La risposta contiene `job_id`. Controllare:

```bash
curl -sS \
  -H "Authorization: Bearer $VELOX_ADMIN_TOKEN" \
  "http://127.0.0.1:8000/api/jobs/<job_id>/full" | jq .
```

Considerare il job riuscito solo quando:

```text
status = SUCCEEDED
expected_render_count = successful_render_count
failed_render_count = 0
documents.en.link presente (se Docs è abilitato)
```

Non fermarsi a `QUEUED`, `RUNNING` o alla presenza di un singolo artefatto.

## Dove trovare i risultati

- Google Doc: `result.data.result.documents.<lang>.link`.
- Cartella delle clip: `result.data.result.render.drive_subfolder_name` sotto
  `render.drive_folder_id`.
- Voiceover e timing dettagliato: artefatti Drive per scena; il timing
  parola-per-parola resta nei file tecnici JSON/SRT/VTT, non nel Google Doc.
- Il Google Doc contiene testo, riepilogo audio e link utili; non deve
  contenere il blocco `Scene Speech Timing JSON`.

## Metriche da leggere

Dal campo `.timing` del risultato completo leggere:

```text
wall_ms
attributed_ms
unattributed_ms
bottleneck_stage
bottleneck_operation
critical_path[]
operations[]
fanout[]
```

Le metriche del job di riferimento del 22 agosto 2026 sono state:

```text
wall time       367.651 s
generate         60.302 s
voiceover       227.051 s  <-- collo di bottiglia
audio compile    25.673 s
document         10.936 s
non attribuito   43.642 s
Ollama           37 chiamate / 214.502 s
TTS              20 chiamate / 180.463 s
```

In quel run 20/20 clip sono riuscite, ma `render_metrics.wall_ms` è rimasto a
zero: il tempo dei child render non era ancora restituito al parent. Il
contratto aggiornato espone `LocalizedRenderResult.started_at`,
`finished_at` e `wall_ms`; `render_metrics.work_ms` somma il lavoro dei child,
mentre `render_metrics.wall_ms` misura primo-start → ultimo-finish.

Per il voiceover, `.timing.stages` separa già `tts`, `publish` e `finalize`;
`.timing.operations` contiene il lavoro cumulativo e non va confuso con il
wall-clock quando le scene sono concorrenti. I nuovi contatori
`audio_metrics.voiceover_requested`, `voiceover_reused` e
`voiceover_generated` descrivono per ora il riuso del checkpoint in memoria,
non un HIT cross-run SQLite: il log espone `db_cache_status` per evitare
interpretazioni errate.

## Errori frequenti

- `IDEMPOTENCY_KEY_REQUIRED`: manca l'header `Idempotency-Key`.
- `Project is required for voiceover-enabled generation`: manca `project`.
- `SCRIPT_SCENE_TEXT_CONTAMINATED`: il modello ha ricevuto istruzioni dentro
  `source_text` o ha copiato il materiale; pulire il source e rilanciare.
- `scene ... clip audio asset is missing`: usare `audio.mix_policy` a livello
  item con `VOICEOVER_ONLY` quando il render deve usare il voiceover senza
  dipendere dalla traccia audio della clip.
- `ambiguous folder`: il nome della sottocartella Drive non è unico.
- Timeout o verifica Drive: controllare i log di upload e il file ID, non
  dichiarare riuscito il job solo perché l'upload è stato avviato.
