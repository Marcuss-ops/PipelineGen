# Frontend ↔ API: interazione e chiamate in dettaglio

Questo documento descrive **come il frontend (Creator / Studio) interagisce con le API** del backend: quali variabili usa, in che ordine vengono fatte le chiamate e con quali payload.

---

## 0. Disponibilità server API e documentazione (docs)

- **Quando il server API è spento**: nessun endpoint è raggiungibile (né script, né voiceover, né documentazione). Il frontend non può chiamare le API e le docs non sono disponibili.

- **Quando il server API è acceso**:
  - **GET** `{base_url}/api/docs` è disponibile e restituisce la documentazione completa (JSON con base_url, endpoints, curl, availability, wake_from_remote, ecc.).
  - **GET** `{base_url}/health` è accessibile.
  - Tutti gli altri endpoint sono raggiungibili sullo stesso base_url.

**Accensione API da remoto (wake server)**  
Se sul server è in esecuzione **wake_api_server.py** (porta 4999 di default):
- **GET** `http://<IP_SERVER>:4999/wake` avvia l’API in background se non risponde (l’API parte con **API_ONLY=1**).
- **GET** `http://<IP_SERVER>:4999/status` restituisce `api_running`, `docs_url` e `wake_port` senza avviare nulla.

Avvio wake: **`./run_wake_server.sh`** (log in `/tmp/wake_api.log`). Per dettagli su variabili WAKE_PORT, API_PORT e endpoint wake vedi **[API_TRIGGER_QUESTO_PC.md](./API_TRIGGER_QUESTO_PC.md)** e **[REMOTE_ENDPOINTS.md](./REMOTE_ENDPOINTS.md)** (sezione "Accensione API da remoto").

**Contratto esatto per il backend:** endpoint, body e risposta attesa per ogni chiamata sono in **[API_CONTRATTO_BACKEND.md](./API_CONTRATTO_BACKEND.md)** (per adattare solo il backend). In console JS il frontend logga `[API REQUEST]` (endpoint + body) e `[API RESPONSE]` (status + corpo).

---

## 1. Configurazione iniziale (URL base e voiceover)

### 1.1 Impostazione all’avvio

- **`config.js`** (caricato da `index.html`):
  - `window.API_BASE_URL = window.location.origin` (es. `http://localhost:8000` o `http://77.93.152.122:8000`)
  - `window.VOICEOVER_SERVER_URL = 'http://' + host + ':8001'` (fallback se il backend non fornisce altro)
  - `window.voiceover_server_url = window.VOICEOVER_SERVER_URL`

- **Subito dopo** (`index.html`), se `API_BASE_URL` è impostato:
  - **GET** `{API_BASE_URL}/api/client-config`
  - Body: nessuno
  - In risposta: se `data.ok && data.voiceover_server_url` → `window.voiceover_server_url = data.voiceover_server_url`
  - Così il frontend usa l’URL voiceover restituito dal backend (es. stesso host:8000 se il voiceover è esposto lì), invece del default :8001.

In sintesi: **tutte le chiamate API** usano `(window.API_BASE_URL || '') + '/api/...'`. Il voiceover può usare lo stesso host (es. `/api/voiceover` su 8000) o un server dedicato (8001) se restituito da `client-config`.

---

## 2. Riepilogo per area funzionale

| Area | File principale | Tipo chiamate |
|------|-----------------|----------------|
| Script (generazione + salvataggio Docs) | `studio-titles.js` | POST script-simple, POST script-save-to-docs |
| Script multipli / batch | `studio-titles.js` | POST script-multiple |
| Voiceover (generazione + polling) | `studio-titles.js`, `studio-voiceover.js` | POST voiceover, GET task, POST queue/start |
| Invio job video (create-master) | `studio-titles.js`, `studio-misc.js` | POST api/video/create-master |
| Drive (cartelle, file, upload) | `studio-titles.js`, `studio-projects.js`, `studio-misc.js`, `studio-voiceover.js` | POST drive/clip-folder-id, create-folder, files, read-txt, upload-thumbnail |
| YouTube (canali, livestream, ricerca) | `studio-projects.js`, `studio-misc.js`, `studio-titles.js` | POST youtube/group-channels, livestreams, GET group-livestreams, POST content/research-youtube, clip/analyze-youtube, download-youtube |
| Stock / clip / suggerimenti | `studio-titles.js`, `studio-cache.js` | POST stock/..., clip/..., drive/... |
| Progetti salvati | `studio-projects-saved.js` | GET/DELETE/POST api/stock/project |
| Categorie titoli | `studio-misc.js` | GET api/titles/categories, structure |
| Server (restart/status) | `studio/core/server-restart.js` | POST api/server/restart, GET api/server/status |

---

## 3. Flusso “Genera script” (tab Script / Titoli)

1. **POST** `{API_BASE_URL}/api/script-simple`
   - **Quando**: click su “Genera script” per uno o più titoli (un’invocazione per titolo).
   - **Body**: `{ title, source, language, duration, style, model, fast_test, max_words, max_entities, entities, conclusion, stock, voiceover_languages: [] }`
   - **Risposta attesa**: `{ ok, script, output?, entities? }`
   - Se `ok` e c’è testo: lo script viene salvato nello stato (es. `projectScripts[currentTitle]`).

2. **Subito dopo il primo script generato** (solo per il primo titolo):
   - **POST** `{API_BASE_URL}/api/script-save-to-docs`
   - **Body**: `{ video_title: currentTitle, script_text: scriptText }`
   - **Risposta attesa**: `{ ok, doc_url?, doc_title? }` oppure `{ ok: false, error, hint? }`
   - Se `ok && doc_url`: il frontend aggiorna `firstScriptApiResponse.doc_url` e mostra “Apri su Google Docs” / link nel log.

---

## 4. Flusso Voiceover

### 4.1 Scelta dell’endpoint

- Il frontend costruisce una lista di **endpoint candidati** (in ordine):
  1. `{API_BASE_URL}/api/voiceover`
  2. `{API_BASE_URL}/voiceover`
  3. `{API_BASE_URL}/api/generate-voiceover`
  4. `{API_BASE_URL}/generate-voiceover`
  5. Stessi path ma con base = `window.VOICEOVER_SERVER_URL` (o `window.voiceover_server_url` da client-config).
- Fa **fetch POST** sul primo candidato che non restituisce 404; l’origin dell’endpoint usato viene salvato in `window.__resolvedVoiceoverServerUrl` per il polling.

### 4.2 Chiamata di generazione

- **POST** `{endpoint}` (uno degli URL sopra)
  - **Quando**: generazione voiceover per un titolo/lingua (da tab Script quando si generano i voiceover, o da tab Voiceover).
  - **Body**: `{ text, languages: [apiLang], filename, drive_folder }`  
    (`drive_folder` = nome cartella Drive del gruppo, es. "HipHop").
  - **Risposta**:
    - **Asincrona (task)**: `{ ok, taskId }` → il frontend fa polling su GET `/api/task/{taskId}` (sullo stesso `__resolvedVoiceoverServerUrl`).
    - **Sincrona**: `{ ok, url }` (e opzionalmente senza `taskId`) → il frontend imposta subito l’URL nel progetto, senza polling.

### 4.3 Polling task (se c’è taskId)

- **POST** `{serverUrl}/api/queue/start` (una volta, per avviare la coda se necessario).
- **GET** `{serverUrl}/api/task/{taskId}`
  - Ripetuto a intervalli fino a `task.status === 'completed'` o `'failed'` o timeout.
  - Da `task.drive_file_id` o `task.drive_url` il frontend costruisce l’URL Drive e lo associa al titolo/lingua.

Quindi: **le chiamate voiceover** possono andare tutte allo stesso `API_BASE_URL` (es. porta 8000) se il backend espone `/api/voiceover` lì; altrimenti si usa il server restituito da client-config (es. :8001).

---

## 5. Flusso “Invia al Master” (create-master)

- **POST** `{API_BASE_URL}/api/video/create-master`
  - **Quando**: invio del job video (un job per titolo, dalla UI “Invia al Master” / creazione video).
  - **Body**: oggetto “job” con ad es. `video_name`, `script_text`, `start_clips`, `middle_clips`, `end_clips`, `stock_clips_timestamps`, `voiceover_items` (array con `url`, `name`, `youtube_title`, `channel_id`), `assets`, `youtube_group`, `project_name`, `drive_folder_id`, ecc.
  - **Risposta attesa**: `{ ok, job_id?, script_doc?, message?, error? }`

Il frontend verifica che nel payload non ci siano placeholder `xxx` o `VOICEOVER_ID` prima di inviare; gli URL voiceover devono essere già risolti (da risposta sincrona o da polling).

---

## 6. Chiamate Drive

Tutte **POST** verso `{API_BASE_URL}/api/...` salvo dove indicato.

| Endpoint | Uso | Body tipico |
|----------|-----|-------------|
| `/api/drive/clip-folder-id` | Ottiene folder_id per nome cartella (clip o voiceover) | `{ folder_name, group? }` |
| `/api/drive/create-folder` | Crea cartella su Drive | `{ parent_id?, name, ... }` |
| `/api/drive/files` | Elenco file in una cartella | `{ folder_id?, ... }` |
| `/api/drive/read-txt` | Legge contenuto file di testo | identificativo file/cartella |
| `/api/drive/upload-thumbnail` | Upload thumbnail | FormData / multipart |
| `/api/drive/folders` | Lista cartelle (es. sotto parent) | `{ parent_id? }` |
| `/api/drive/folder-info` | Info su una cartella | identificativo cartella |

Usati da: tab Titoli (clip, stock, script), tab Progetti (cartelle gruppo), tab Voiceover (file, cartelle, upload thumbnail).

---

## 7. Chiamate YouTube / contenuti

| Endpoint | Metodo | Uso | Body/query |
|----------|--------|-----|------------|
| `/api/youtube/group-channels` | POST | Canali per gruppo | `{ group }` |
| `/api/youtube/group-livestreams` | GET | Livestream attivi/schedulati | `?group=...&status=active,scheduled` |
| `/api/youtube/livestreams/start` | POST | Avvio livestream | body specifico |
| `/api/content/research-youtube` | POST | Ricerca contenuti YouTube | body specifico |
| `/api/clip/analyze-youtube` | POST | Analisi clip YouTube | body con URL/id |
| `/api/clip/download-youtube` | POST | Download clip da YouTube | body con URL/id, cartella, ecc. |

Queste chiamate servono a popolare canali, livestream, suggerimenti clip e download; tutte usano `API_BASE_URL`.

---

## 8. Chiamate Stock / Clip / Cache

| Endpoint | Uso |
|----------|-----|
| `/api/stock/extract-subject` | Estrae soggetto da titolo/testo |
| `/api/stock/search-folders` | Cerca cartelle stock |
| `/api/stock/search-youtube` | Cerca video YouTube per stock |
| `/api/stock/create-folder` | Crea cartella stock |
| `/api/stock/report` | Report stock (GET) |
| `/api/stock/project/{name}` | GET/DELETE progetto salvato |
| `/api/stock/project` | POST salvataggio progetto |
| `/api/clip/search-folders` | Cerca cartelle clip |
| `/api/clip/read-folder-clips` | Legge clip in cartella |
| `/api/clip/drive-rank` | Ranking clip Drive |
| `/api/clip/suggest` | Suggerimenti clip |
| `/api/clip/subfolders` | Sottocartelle clip |
| `/api/clip/create-subfolder` | Crea sottocartella |
| `/api/clip-multiple` | Upload/elaborazione clip multipli |
| `/api/clip/download-youtube` | Download clip da YouTube |

Tutte con **POST** (tranne report e GET project) verso `{API_BASE_URL}/api/...`.

---

## 9. Altre chiamate

| Endpoint | Metodo | Uso |
|----------|--------|-----|
| `/api/translate-title` | POST | Traduzione titolo |
| `/api/detect-language` | POST | Rilevamento lingua |
| `/api/auto-research` | POST | Ricerca automatica |
| `/api/generate-description-tags` | POST | Genera descrizione/tag (voiceover UI) |
| `/api/titles/categories` | GET | Categorie titoli |
| `/api/titles/categories/structure` | GET | Struttura categorie |
| `/api/cookies/validate` | GET | Validazione cookie (query group) |
| `/api/server/restart` | POST | Restart server |
| `/api/server/status` | GET | Stato server |

Anche queste usano `API_BASE_URL`.

---

## 10. Tipo di chiamate (tecnico)

- **Sempre** `fetch()` nativo (nessun axios).
- **Headers**: per JSON viene impostato `Content-Type: application/json` e il body è `JSON.stringify(...)`.
- **Base URL**: `(window.API_BASE_URL || '') + path`; se la pagina è servita dallo stesso server (stessa origin), `API_BASE_URL` è l’origin quindi le chiamate sono same-origin (es. `http://localhost:8000/api/...`).
- **Voiceover**: prima si prova `API_BASE_URL` (stesso server), poi eventuale server dedicato da `client-config` / `VOICEOVER_SERVER_URL`, così con un solo host (es. 8000) tutto funziona senza 8001.

---

## 11. Riassunto flussi principali

1. **Caricamento pagina**: legge `config.js` → `API_BASE_URL`; opzionale GET `client-config` → aggiorna `voiceover_server_url`.
2. **Genera script**: per ogni titolo POST `script-simple`; dopo il primo successo POST `script-save-to-docs` per mostrare il link Docs.
3. **Genera voiceover**: POST a uno degli endpoint voiceover (stesso host o server dedicato); se c’è `taskId`, POST `queue/start` e GET `task/{taskId}` in polling fino a completamento.
4. **Invia video**: POST `api/video/create-master` con payload completo (script, clip, voiceover_items con URL già risolti).
5. **Progetti / Drive / YouTube / Stock**: varie POST (e poche GET) verso `api/drive/...`, `api/youtube/...`, `api/stock/...`, `api/clip/...` secondo le azioni dell’utente (selezione gruppo, cartelle, download, suggerimenti, ecc.).

In questo modo il frontend usa **un solo base URL** per quasi tutto; il voiceover può essere sullo stesso host (raggiungibile ovunque con lo stesso `API_BASE_URL`) o su un server separato configurato via `client-config`.
