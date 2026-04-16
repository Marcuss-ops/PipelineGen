# Contratto backend: endpoint, body, risposta

Documentazione **esatta** delle chiamate che il frontend fa alle API, così puoi adattare solo il backend per farle rispettare.

---

## 1. GET /api/client-config

- **Endpoint:** `GET {API_BASE_URL}/api/client-config`
- **Body:** nessuno (richiesta GET)
- **Risposta attesa (JSON):**
```json
{
  "ok": true,
  "api_base_url": "http://51.91.11.36:8000",
  "voiceover_server_url": "http://51.91.11.36:8000"
}
```
- **Note:** Il frontend usa `voiceover_server_url` per le chiamate voiceover; se manca, usa `host:8001`.

---

## 2. POST /api/script-simple

- **Endpoint:** `POST {API_BASE_URL}/api/script-simple`
- **Headers:** `Content-Type: application/json`
- **Body (esempio):**
```json
{
  "title": "How Innovators Transform Industries",
  "source": "YouTube",
  "language": "en",
  "duration": "45",
  "style": "Default",
  "model": "gemma3:4b",
  "fast_test": false,
  "max_words": 500,
  "max_entities": 5,
  "entities": true,
  "conclusion": true,
  "stock": true,
  "voiceover_languages": []
}
```
- **Risposta attesa (JSON):**
```json
{
  "ok": true,
  "script": "Testo dello script generato...",
  "output": "Testo dello script generato...",
  "entities": { "person": [], "place": [], ... }
}
```
- **In caso di errore:**
```json
{ "ok": false, "error": "messaggio", "detail": "..." }
```
- **Note:** Il frontend accetta sia `script` sia `output` come testo; se `ok` e c’è testo, lo salva. `entities` opzionale (solo primo titolo).

---

## 3. POST /api/script-save-to-docs

- **Endpoint:** `POST {API_BASE_URL}/api/script-save-to-docs`
- **Headers:** `Content-Type: application/json`
- **Body (esempio):**
```json
{
  "video_title": "How Innovators Transform Industries",
  "script_text": "Testo completo dello script...",
  "folder_id": "opzionale_id_cartella_drive"
}
```
- **Risposta attesa (JSON):**
```json
{
  "ok": true,
  "doc_url": "https://docs.google.com/document/d/...",
  "doc_title": "How Innovators Transform Industries"
}
```
- **In caso di errore:**
```json
{ "ok": false, "error": "messaggio", "hint": "suggerimento (es. credenziali Google)" }
```

---

## 4. POST /api/voiceover

- **Endpoint:** `POST {API_BASE_URL}/api/voiceover` (o `/voiceover`, `/api/generate-voiceover`, `/generate-voiceover`; il frontend prova in ordine)
- **Headers:** `Content-Type: application/json`
- **Body (esempio):**
```json
{
  "text": "Testo da sintetizzare in audio...",
  "languages": ["pt"],
  "filename": "voiceover_TitoloVideo_pt.mp3",
  "drive_folder": "HipHop"
}
```
- **Risposta attesa – variante sincrona (es. Edge TTS):**
```json
{
  "ok": true,
  "url": "https://drive.google.com/file/d/...",
  "source": "edge_tts",
  "voice": "pt-BR-AntonioNeural"
}
```
- **Risposta attesa – variante asincrona (task):**
```json
{
  "ok": true,
  "taskId": "uuid-task-id",
  "tasks": [{ "id": "uuid-task-id", "status": "pending", "language": "pt", "filename": "..." }]
}
```
- **In caso di errore:**
```json
{ "ok": false, "error": "messaggio", "hint": "..." }
```
- **Note:** Se c’è `taskId` (o `tasks[0].id`), il frontend fa polling su `GET {serverUrl}/api/task/{taskId}` fino a `status: completed` e usa `drive_file_id` o `drive_url` per l’URL finale.

---

## 5. POST /api/video/create-master

- **Endpoint:** `POST {API_BASE_URL}/api/video/create-master`
- **Headers:** `Content-Type: application/json`
- **Body (struttura; i campi sono quelli inviati dal frontend):**
```json
{
  "job_spec_version": 1,
  "video_name": "Titolo del video",
  "project_name": "Nome progetto",
  "video_style": "discovery",
  "youtube_group": "Music",
  "script_text": "Testo script...",
  "start_clips": [{ "url": "https://...", "start_time": 0, "end_time": 10, ... }],
  "middle_clips": [],
  "end_clips": [],
  "stock_clips_timestamps": [],
  "voiceover_items": [
    {
      "url": "https://drive.google.com/file/d/...",
      "name": "voiceover_Titolo_pt.mp3",
      "youtube_title": "Titolo del video",
      "channel_id": "UC...",
      "language": "pt"
    }
  ],
  "assets": { "background": "...", "music": "..." },
  "drive_folder_id": "id_cartella_drive"
}
```
- **Risposta attesa (JSON):**
```json
{
  "ok": true,
  "job_id": "uuid-job-id",
  "script_doc": "https://docs.google.com/...",
  "message": "Job creato"
}
```
- **In caso di errore:**
```json
{ "ok": false, "error": "messaggio", "detail": "..." }
```

---

## Riepilogo

| Chiamata              | Method | Endpoint                     | Body principale                          | Risposta chiave                    |
|-----------------------|--------|-----------------------------|------------------------------------------|------------------------------------|
| client-config         | GET    | /api/client-config          | —                                        | ok, api_base_url, voiceover_server_url |
| script-simple         | POST   | /api/script-simple          | title, source, language, duration, ...   | ok, script o output, entities?     |
| script-save-to-docs   | POST   | /api/script-save-to-docs    | video_title, script_text, folder_id?     | ok, doc_url, doc_title             |
| voiceover             | POST   | /api/voiceover              | text, languages, filename, drive_folder  | ok, url (sincrono) o taskId + tasks (async) |
| create-master         | POST   | /api/video/create-master    | video_name, script_text, voiceover_items, ... | ok, job_id, script_doc?, message   |

In console JS il frontend logga **endpoint e body** di ogni richiesta (prefisso `[API REQUEST]`) e un riepilogo della risposta (`[API RESPONSE]`).
