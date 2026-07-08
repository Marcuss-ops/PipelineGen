# YouTube Clips Deploy Readiness — Action Plan

**Data:** 2026-07-08  
**Owner:** `internal/api/assets/youtube/` + `internal/application/assets/sourcing/youtube/`  
**Deadline:** 2026-08-15  
**Status:** in_progress

Canonical surface: this plan is the SOLE owner of the YouTube clips deploy-readiness wave (godlike/06 SSOT).

---

## §0 Diagnosi corrente

### Cosa FUNZIONA già
- Download YouTube via yt-dlp
- Taglio clip via FFmpeg
- Registrazione locale su `data/media/clips/`
- API `POST /api/media/register-from-youtube` — sync OK, Drive KO
- API `POST /api/media/register-batch` — job accodato OK
- API `POST /api/clips/process` — job accodato OK

### Cosa NON funziona
- **Drive upload** — fallisce con "asset registered locally; Drive upload failed — retry scheduled"
- **Worker async** — si ferma al 5% in `clips/process`
- **Diagnostica job** — errori non significativi
- **Readiness endpoint** — non rileva Drive non cablato

### Fix già applicato (2026-07-08)
- `internal/application/assets/sourcing/youtube/adapters.go`: rimosso blocco che azzerava `RootFolderOverride` quando `Provider != ""`. Il `folder_id` utente veniva silenziosamente scartato perché `Provider` defaulta sempre a `"youtube"`.

---

## §1 Priorità esatte — 12 Step

### Step 1 🔴 — Verifica "Drive Publisher unwired"
```bash
rg "Drive Publisher unwired" internal/ pkg/ cmd/
rg "Publisher unwired" internal/ pkg/ cmd/
rg '"unwired"' internal/ -g '*.go'
```
**Obiettivo:** capire se il problema è DI nil (publisher non cablato) o permessi Drive.
Se `publisher == nil` → cablare il publisher nel bundle corretto.
Se `publisher != nil` → errore Google Drive (403, 404, permission).

### Step 2 🔴 — Verifica tipo credenziale Google
```bash
cat token.json | python3 -c "import json,sys; d=json.load(sys.stdin); print('type:', d.get('type'), 'client_email:', d.get('client_email'))"
```
- **Caso A:** `type: service_account` → condividi cartella `1iAGhWidRF0hpJYvku_fIavEIY50_V1wA` con `client_email` come Editor
- **Caso B:** OAuth utente → la cartella deve appartenere all'account autenticato
- **Caso C:** `client_email` vuoto → verificare `GOOGLE_APPLICATION_CREDENTIALS` env var

### Step 3 🟡 — Drive Canary obbligatorio
Endpoint admin o comando CLI che testa upload Drive in isolamento:
- Crea file dummy → upload → verifica file_id → verifica file esiste su Drive
- Folder: `1iAGhWidRF0hpJYvku_fIavEIY50_V1wA`
- **Finché questo non passa, non testare YouTube.**

### Step 4 🟡 — Drive fail-closed in produzione
Aggiungere env var `MEDIA_DRIVE_REQUIRED=true`:
- Quando `true`: Drive upload KO → job `retry_wait` / errore esplicito, MAI `completed` falso
- Comportamento attuale: "asset registered locally; Drive upload failed — retry scheduled" → sembra successo parziale

### Step 5 🟡 — Path canonico clip via DestinationRegistry
Flusso ideale:
```
register-from-youtube → MediaRegisterService → DestinationResolver
→ delivery.Publisher → DestinationYouTubeClip → Drive upload → media_assets DB
```
NON fare upload Drive direttamente dentro il service YouTube.
`PublishRequest` deve includere `ProjectID` / `Group` / `Subject` per routing semantico.
Per `/api/clips/process`, se il payload porta solo `destination.folder_id`, l'handler ora materializza una subdir di default dal `video_id` invece di caricare tutto in root Drive.

### Step 6 🟢 — Correggi `/api/clips/process`
Il job fallisce al 5% senza errori significativi. Aggiungere `job_events` persistenti:
```sql
job_events(id, job_id, step, status, message, error_code, error_detail, created_at)
```
Eventi minimi: `queued → leased → started → download_started → download_completed → ffmpeg_started → ffmpeg_completed → drive_upload_started → drive_upload_failed → db_register_completed → completed`

### Step 7 🟢 — Verifica wiring API = worker
Worker async (`clips/process`, `register-batch`) DEVE avere gli stessi adapter del sync:
```text
Drive client, delivery.Publisher, FolderManager, FileUploader,
DestinationRegistry, DB repo, job repo, outbox
```
Verifica:
```bash
rg "PublishClipToDrive" internal/ -g '*.go'
rg "sourcingPublisherAdapter" internal/ -g '*.go'
```

### Step 8 🟢 — `/readyz` severo
Aggiungere check obbligatori:
- DB accessibile + migrations applicate
- yt-dlp + ffmpeg + ffprobe presenti
- `data/media/clips/` scrivibile
- Drive credentials caricate
- Drive canary folder accessibile
- `delivery.Publisher` non nil
- DestinationClip registrata
- Worker job handler `media.clip` + `clips.process` registrati
- `/readyz` → 503 se Drive non pronto

### Step 9 🔵 — Test finale: register-from-youtube
```bash
curl -X POST :8000/api/media/register-from-youtube -d '{
  "url": "https://www.youtube.com/watch?v=jNQXAC9IVRw",
  "folder_id": "1iAGhWidRF0hpJYvku_fIavEIY50_V1wA",
  "name": "Smoke test", "start": 0, "end": 5
}'
```
Deve tornare `drive_file_id`, `drive_link`, `drive_folder_id` popolati.

### Step 10 🔵 — Test finale: register-batch
Job deve passare da `queued → running → completed`. Ogni item completato o failed con errore chiaro.

### Step 11 🔵 — Test finale: clips/process
Deve superare il 5% e completare tutti gli step: `download → ffmpeg → drive_upload → db_register → completed`.

### Step 12 ⚪ — extract-important (posticipato)
Disabilitare in prod finché:
- Gemma/Ollama raggiungibile
- Smoke test dedicato passa
- `CLIPS_EXTRACT_IMPORTANT_ENABLED=false` di default

---

## §2 Per-PR migration sequence (godlike/07 EXPAND→BACKFILL→CUTOVER→CONTRACT)

Ogni PR atterra **direttamente su `main`** per AGENTS.md Git-Lesson-2 con `Co-authored-by` trailer.

| # | PR ID | Band | Deadline | Descrizione |
|---|-------|------|----------|-------------|
| 1 | `PR-CLIPS-PUBLISHER-DIAGNOSE` | P0 | 2026-07-09 | Grep + verifica publisher nil vs Drive permission |
| 2 | `PR-CLIPS-GOOGLE-CREDENTIALS` | P0 | 2026-07-09 | Verifica tipo credenziale + condividi cartella se service account |
| 3 | `PR-CLIPS-DRIVE-CANARY` | P1 | 2026-07-15 | Endpoint o CLI per upload canary isolato |
| 4 | `PR-CLIPS-DRIVE-FAIL-CLOSED` | P1 | 2026-07-15 | MEDIA_DRIVE_REQUIRED gate + no fake-success |
| 5 | `PR-CLIPS-DESTINATION-REGISTRY` | P1 | 2026-07-22 | Routing via DestinationRegistry, non diretto |
| 6 | `PR-CLIPS-JOB-EVENTS` | P2 | 2026-07-25 | job_events table + step diagnostics |
| 7 | `PR-CLIPS-WORKER-WIRING` | P2 | 2026-07-25 | Unifica wiring sync/async stesso publisher |
| 8 | `PR-CLIPS-READYZ` | P2 | 2026-08-01 | /readyz con Drive canary + handler check |
| 9 | `PR-CLIPS-REGISTER-TEST` | P3 | 2026-08-08 | Test end-to-end register-from-youtube |
| 10 | `PR-CLIPS-BATCH-TEST` | P3 | 2026-08-08 | Test end-to-end register-batch |
| 11 | `PR-CLIPS-PROCESS-TEST` | P3 | 2026-08-08 | Test end-to-end clips/process |
| 12 | `PR-CLIPS-EXTRACT-IMPORTANT` | P4 | 2026-08-15 | extract-important smoke test + enable |

---

## §3 Config minima production

```env
MEDIA_DRIVE_REQUIRED=true
DRIVE_CLIPS_ROOT_FOLDER_ID=1iAGhWidRF0hpJYvku_fIavEIY50_V1wA
GOOGLE_APPLICATION_CREDENTIALS=google-accounting/service-account.json
JOB_WORKER_ENABLED=true
JOB_MAX_ATTEMPTS=3
JOB_RETRY_BACKOFF_SECONDS=30
CLIPS_EXTRACT_IMPORTANT_ENABLED=false
```

---

## §4 Verifica gate per wave-flip

Wave flippa a `status: shipped` quando:
- Tutti i 12 linked_issues sono `status: shipped`
- `register-from-youtube` torna `drive_file_id` + `drive_link` popolati
- `register-batch` completa `queued → running → completed`
- `clips/process` supera il 5% e completa
- `/readyz` riporta tutti i check OK

---

## §5 Cross-references

- `architecture/current.yaml#PR-CLIPS-DEPLOY-READINESS` (wave-tracker)
- `AGENTS.md` §Recent cross-cutting closures (lockstep mirror)
- `CHANGELOG.md` `## Unreleased → ### Documentation` (closure meta-entry)
- `internal/application/assets/sourcing/youtube/adapters.go` (fix già applicato)
- `internal/infrastructure/drive/publisher.go` (canonical Drive write seam)
- `architecture/waves/wave_p1_high.yaml#PR-P12-DRIVE-COMPLETION-2026-07-08` (sister Pattern 12 wave)

---

## §6 Honest scope-lock (godlike/07)

- La fix `adapters.go` è già su `origin/main` (PR-REGISTER-DRIVE-FOLDER-FIX)
- I permessi Drive vanno verificati PRIMA di testare YouTube
- `extract-important` è deliberatamente posticipato a Step 12
- Il canary Drive deve essere la PREREQUISITE per ogni test YouTube
