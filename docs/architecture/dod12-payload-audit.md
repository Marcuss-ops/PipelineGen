# DoD #12 Audit — Violazioni `drive_folder_id` / `root_folder_id` / `drive_link` nei payload pubblici

Data: 2026-07-06  
Scope: `internal/api/**/*.go` — handler request types (JSON payload input)  
Regola DoD #12: "Le API non richiedono più drive_folder_id, drive_link o root_folder_id nei payload normali"

## Riepilogo

| Classificazione | Conteggio | Azione |
|----------------|-----------|--------|
| **ALLOWED — Response/output** | ~20 | ✅ Nessuna |
| **ALLOWED — Frozen legacy (410)** | 2 | ✅ Scadenza 2026-12-31 |
| **ALLOWED — Admin/system** | 7 | ✅ Nessuna |
| **DEPRECATED — Ha `Location` alternativo** | 3 | ⏳ Backward-compat fino a 2026-12-31 |
| **MUST-MIGRATE — Nessun alternativo** | 5 | 🔴 Da migrare a `AssetLocationInput` |
| **OUT-OF-SCOPE** | ~15 | ✅ Test, service-layer, response |

**Verdetto**: 5 siti **MUST-MIGRATE** + 3 siti **DEPRECATED** (già in corso). Il resto è consentito.

---

## 🔴 MUST-MIGRATE: payload pubblici senza `Location` alternativo

### 1. `assets/clips/bulk_upload.go:27` — `BulkUploadYouTubeClipsRequest.DriveFolderID`
```go
DriveFolderID string `json:"drive_folder_id,omitempty"`
```
- **Endpoint**: `POST /api/media/bulk-upload-youtube-clips`
- **Ruolo**: Target Drive folder per bulk upload locale→Drive
- **Forward-pointer**: `PR-BULK-UPLOAD-LOCATION` (deadline 2026-08-15)
- **Nota**: Accetta anche `drive_folder_name` come alternativa human-readable; entrambi vanno migrati a `location` DTO

### 2. `assets/clips/bulk_upload_transport.go:211` — `BulkUploadRequest.DriveFolderID`
- **Endpoint**: `POST /api/media/bulk-upload` (variante transport)
- **Forward-pointer**: `PR-BULK-UPLOAD-TRANSPORT-LOCATION` (condiviso con #1)

### 3. `channels/handler.go:117` — `UpsertRequest.DriveFolderID`
```go
DriveFolderID string `json:"drive_folder_id,omitempty"`
```
- **Endpoint**: `POST /api/channels` + `POST /api/channels/bulk-upsert`
- **Ruolo**: Folder dove salvare i clip del canale monitorato
- **Forward-pointer**: `PR-CHANNELS-LOCATION` (deadline 2026-08-15)
- **Nota**: È un endpoint di **configurazione** (non di publishing), ma è comunque pubblico

### 4. `assets/handler_searchqueries.go:99` — `SearchQueryUpsertRequest.DriveFolderID`
```go
DriveFolderID string `json:"drive_folder_id,omitempty"`
```
- **Endpoint**: `POST /api/search-queries`
- **Ruolo**: Folder Drive associata alla query di ricerca programmata
- **Forward-pointer**: `PR-SEARCH-QUERIES-LOCATION` (deadline 2026-08-15)
- **Nota**: Endpoint di configurazione scheduling; non è publishing diretto

### 5. `assets/artlist/artlist_handlers.go:136` — `RootFolderID` (run tag)
```go
if strings.TrimSpace(req.RootFolderID) == "" {
```
- **Endpoint**: `POST /api/artlist/run` (via `RunTagRequest`)
- **Ruolo**: Root folder per i risultati Artlist
- **Forward-pointer**: `PR-ARTLIST-RUN-LOCATION` (deadline 2026-08-15)
- **Nota**: L'handler ha già `ArtlistRootFolderID()` come fallback da config; il field payload è l'override per-run

---

## ⏳ DEPRECATED: ha già `Location` alternativo (backward-compat)

### 6. `assets/register/handler.go:48` — `RegisterFromYouTubeRequest.FolderID`
```go
FolderID string `json:"folder_id"`
```
- **Endpoint**: `POST /api/media/register-from-youtube`
- **Status**: ✅ Già DEPRECATED — il campo `Location` (Wave 6) è l'alternativa
- **Rimozione**: 2026-12-31 (per FASE-2.1-VOICE-FREEZE)

### 7. `assets/register/handler.go:92` — `BatchRegisterRequest.FolderID`
```go
FolderID string `json:"folder_id"`
```
- **Endpoint**: `POST /api/media/register-batch`
- **Status**: ✅ Come sopra — `Location` già accettato

### 8. `assets/stock/handler.go:97,181` — `StockSearchAndRunRequest.FolderID` / `StockRunPayload.FolderID`
- **Endpoint**: `POST /api/stock/search-and-run` + `POST /api/stock/run`
- **Status**: ⚠️ Il campo `FolderID` esiste nel payload ma NON ha un `Location` DTO accanto. È usato come folder target per l'output. Forward-pointer: `PR-STOCK-FOLDER-LOCATION`
- **Nota**: La risposta ha già `drive` placeholder (DoD #8)

---

## ✅ ALLOWED — Response/output (non violano DoD #12)

| File | Campo | Ruolo |
|------|-------|-------|
| `assets/register/handler.go:250` | `"drive_folder_id": res.DriveFolderID` | Response JSON |
| `assets/register/handler.go:248` | `"drive_link": res.DriveLink` | Response JSON |
| `assets/clips/bulk_upload.go:187` | `"drive_folder_id": targetDriveFolderID` | Response JSON |
| `assets/clips/clip_action.go:146` | `"drive_link": result.DriveLink` | Response JSON |
| `assets/clips/ingest_upload.go:132` | `DriveLink: result.DriveLink` | Response JSON |
| `assets/clips/search.go:274,290-294` | `clip.DriveLink()`, `drive_link`, `folder_id` | Response/search output |
| `assets/clips/handler_reprocess.go:49` | `"drive_link": result.DriveLink` | Response JSON |
| `assets/clips/clip_integrity_handler.go:149-152` | `has_drive_link`, `drive_link`, `drive_link_valid` | Diagnostics response |
| `mediasearch/handler_test.go` | `DriveLink` exclusion test | Test-only |
| `assets/handler_realtime.go:41` | `DriveLink string` | Search response DTO |
| `script/handler_clip_search.go:26` | `DriveLink string` | Search response DTO |
| `assets/artlist/artlist_handlers.go:46` | `DriveLink string` | Search response DTO |
| `assets/soundeffect/handler.go:295,344` | `clip.SetDriveLink`, `clip.DriveLink()` | Response output |

---

## ✅ ALLOWED — Frozen legacy 410-Gone

| File | Campo | Scadenza |
|------|-------|----------|
| `script/handler_legacy_from_clips.go:47` | `DriveFolderID` | 2026-12-31 |
| `script/handler_legacy_with_images.go:29` | `DriveFolderID` | 2026-12-31 |

Entrambi ritornano HTTP 410 Gone. Il payload è congelato per backward-compat del counter Prometheus `legacy_generate_*_total`.

---

## ✅ ALLOWED — Admin/system endpoint

| File | Campo | Ruolo |
|------|-------|-------|
| `system/handler_drive.go:178,207` | `root_folder_id` | Drive admin reconcile |
| `system/handler_drive.go:228-229` | `from_folder_id`, `to_folder_id` | Drive admin move |
| `system/handler_drive.go:385,401` | `folder_id` query param | Drive admin list files |
| `assets/artlist/artlist_handlers.go:133` | `DefaultRootFolderID` | Artlist config port |
| `assets/soundeffect/handler.go:268` | `RootFolderOverride` | Sound effect admin |
| `assets/voiceover/handler.go:140-141` | `Destination.FolderID` | Voiceover destination routing |
| `assets/voiceover/types.go:62,241-242` | `Destination.FolderID` | Voiceover destination DTO |

---

## ✅ ALLOWED — Internal service-layer plumbing

| File | Uso |
|------|-----|
| `script/handler_facade.go:120` | `ResolveDriveFolderID()` — helper method, non è payload JSON |
| `script/handler_deps.go` | Documentazione del facade |
| `assets/register/handler.go:250` | Response field (già classificato sopra) |
| `assets/clips/folder_command_handler.go` | Operazioni interne su folder (delete/trash) |
| `assets/clips/folder_query_handler.go` | Query interne per folder |
| `assets/clips/ingest_update.go:65-66` | `payload["folder_id"]` — internal clip update |
| `assets/youtube/youtube_handlers.go:158-165` | `Destination.FolderID` — internal routing |
| `assets/clips/bulk_upload.go:141-167` | Risoluzione interna `targetDriveFolderID` |

---

## Riepilogo forward-pointers

| PR ID | Target | Deadline |
|-------|--------|----------|
| `PR-BULK-UPLOAD-LOCATION` | `BulkUploadYouTubeClipsRequest` → `AssetLocationInput` | 2026-08-15 |
| `PR-CHANNELS-LOCATION` | `UpsertRequest.DriveFolderID` → `AssetLocationInput` | 2026-08-15 |
| `PR-SEARCH-QUERIES-LOCATION` | `SearchQueryUpsertRequest.DriveFolderID` → `AssetLocationInput` | 2026-08-15 |
| `PR-ARTLIST-RUN-LOCATION` | `RunTagRequest.RootFolderID` → `AssetLocationInput` | 2026-08-15 |
| `PR-STOCK-FOLDER-LOCATION` | `StockRunPayload.FolderID` → `AssetLocationInput` | 2026-08-15 |
| `PR-REGISTER-FOLDERID-REMOVAL` | Rimuovere `FolderID` legacy da register (dopo freeze) | 2026-12-31 |
