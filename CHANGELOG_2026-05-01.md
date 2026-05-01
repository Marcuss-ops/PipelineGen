# Changelog - 2026-05-01

## Summary

Sessione dedicata alla risoluzione dei problemi del pipeline Artlist: gestione job zombie, logging granulare, automazione discovery+save, fix upload Drive, e fix critici post-review.

## Bug Fixes (2026-05-01 19:30)

### 1. Fix `dry_run` rotto (Bug #1)
- `dry_run: true` ora simula correttamente senza scaricare, processare o caricare
- Aggiunto blocco anticipato che restituisce `WouldProcess` e `WouldSkip` senza effetti reali
- Prima il codice ignorava `dry_run` dopo la creazione cartella, processando comunque i clip

### 2. Fix `UploadClipToDrive` non carica file reale (Bug #2)
- Ora apre il file locale (`clip.LocalPath` o path costruito) e usa `.Media(f)` per il upload
- Aggiorna `clip.DriveLink`, `clip.DownloadLink`, `clip.FileHash` e salva nel DB
- Prima creava solo i metadata su Drive senza caricare il contenuto video

### 3. Fix `DownloadClip` usa DriveLink come file ID (Bug #3)
- Ora usa `driveFileIDFromClip()` per estrarre correttamente il file ID da DriveLink/DownloadLink
- Fallback: se non c'è DriveLink o download fallisce, scarica da Artlist usando `downloadClip()`
- Prima falliva perché `Files.Get()` vuole un file ID, non un URL completo

### 4. Fix retry job cancella `active_key` troppo presto (Bug #4)
- Creato `finishRunRecordWithActiveKey()` con parametro `clearActiveKey`
- Durante i retry, `active_key` viene preservato per evitare duplicati
- Solo al tentativo finale o completamento viene cancellato `active_key`
- Prima: `finishRunRecord` cancellava sempre `active_key = ''`, permettendo race conditions

### 5. Allineamento cartella principale vs sottocartelle (Bug #5)
- Rimosso `getOrCreateFolder()` per tag in `pipeline.go`
- Le clip vengono caricate direttamente nel folder principale (`rootFolderID`)
- Allineato il codice con la documentazione nel changelog

### 6. Fix `Search` ignora `limit` (Bug #6)
- Applica `req.Limit` dopo aver ottenuto i risultati dal DB
- Default 8, massimo 50 (coerente con l'handler)
- Prima restituiva tutti i risultati ignorando il parametro limit

### 7. Fix `GetClipStatus` non controlla file locale (Bug #7)
- Ora controlla `clip.LocalPath` e usa `os.Stat()` per verificare esistenza file
- Imposta correttamente `HasLocalFile` e `LocalPath` nella response
- Prova a costruire il path locale se `clip.LocalPath` è vuoto

### 8. Fix `RunTag` maschera errori DB (Bug #8)
- Distingue tra "nessun risultato" e "errore DB"
- Se `SearchClips` fallisce, tenta live search come fallback
- Se entrambi falliscono, ritorna errore invece di completare silenziosamente
- Previene falsi positivi "completed" quando il DB non risponde

## What Changed

## What Changed

### 1. Anti-zombie run handling in `run_management.go`

- Aggiunta logica anti-zombie in `StartRunTag`:
  - Se un job è `running`/`queued` da più di 15 minuti → marca `failed`
  - Se un job è stato creato da più di 20 minuti senza progressi → marca `failed`
  - Pulisce `active_key` per permettere nuovo run
  - Previene il riuso di job morti che bloccavano nuove richieste

### 2. Logging granulare in `pipeline.go`

- Aggiunti log dettagliati per ogni fase:
  - DB search (`SearchClips`)
  - Live search (`SearchLiveAndSave`)
  - Download clip
  - FFmpeg processing
  - File hash calculation
  - Google Drive upload
  - Database update (`UpsertClip`)
- Ogni step logga `clip_id`, `name`, `status`, e metriche temporali
- Fix conteggi `resp.Items` e stati skip/error

### 3. Fix upload Drive in `pipeline.go`

- Rimosso blocco `driveClient == nil` che saltava l'upload senza log chiari
- Aggiunto log `driveClient is nil, skipping upload for clip`
- Corretta gestione errori apertura file prima dell'upload
- Mantenuta logica `.Media(f)` per caricamento reale del contenuto

### 4. Versioning e build info in `cmd/server/main.go`

- Aggiunto versione `1.1.0` ai log di startup
- Lettura hash commit da `VERSION.txt` generato al build
- Log di avvio mostra ora: `version`, `commit`, `port`, `data_dir`

### 5. Ricostruzione binario

- `go clean -cache && go build -o server_bin ./cmd/server`
- Generazione `VERSION.txt` con `git log -n 1 --pretty=format:"%h - %cd"`
- Server avviato con redirect log: `./server_bin > server.log 2>&1`

### 6. Fix salvataggio clip nel DB corretto

- Modificato `wire.go` per passare `coreDeps.ArtlistRepo` invece di `coreDeps.ClipsOnlyRepo`
- Le clip Artlist ora vengono salvate in `artlist.db.sqlite` (non più in `clips.db.sqlite`)
- Verificato con query: clip presenti in `artlist.db.sqlite` con `drive_link` corretto

### 7. Fix config `drive_folder_id`

- Spostato `drive_folder_id` nella sezione `harvester` di `config.yaml` (non più sotto `google`)
- La funzione `ResolveArtlistRootFolderID` legge da `cfg.Harvester.DriveFolderID`
- Configurazione corretta: `harvester.drive_folder_id: "1OAAf5dawAppdopsgCq1yHFGPUXCI9Vbk"`

### 8. Fix upload Drive nel folder principale

- Rimossa creazione sottocartelle per tag in `pipeline.go`
- Le clip vengono caricate direttamente nel folder principale Artlist (`1OAAf5dawAppdopsgCq1yHFGPUXCI9Vbk`)
- Log aggiornato: `using main artlist folder for uploads`

### 9. Server persistence fix

- Aggiunto `signal.Ignore(syscall.SIGHUP)` in `main.go` per evitare shutdown accidentali
- Usato `screen` per avviare il server in sessione persistente
- Il server ora rimane attivo anche dopo timeout del bash tool

## Validation

- Server build completata con successo (with SIGHUP ignore)
- Anti-zombie testato: job stale marcati `failed` correttamente
- Nuovo run `pizza_production_test` entra nel pipeline (non riusa zombie)
- Log mostrano: `no clips found in DB for term, performing live search`
- Live search parte (`Running live Artlist search`)
- **Clips ora salvate in `artlist.db.sqlite`** (non più in `clips.db.sqlite`)
- **Config `drive_folder_id` corretta** in sezione `harvester`
- **Upload Drive nel folder principale** (`1OAAf5dawAppdopsgCq1yHFGPUXCI9Vbk`) senza sottocartelle
- **Server rimane attivo** usando `screen` session
- Test end-to-end completato: clip "pizza_screen_test" processata e caricata su Drive
- Verificato `drive_link` presente nelle clip: `https://drive.google.com/file/d/...`

## Next Steps

### 1. Aggiungere `recover` in `executeRunTag`

- Catturare panic nel pipeline per marcare il job `failed`
- Pulire `active_key` in caso di crash
- Evitare che job rimangano `running` per sempre

### 2. Test con DB vuoto

- Svuotare `artlist.db.sqlite` o usare nuovo termine
- Verificare che il flusso completo sia:
  1. `SearchClips` → 0 risultati
  2. `SearchLiveAndSave` → salva nel DB
  3. `SearchClips` (secondo) → carica dal DB
  4. Loop download/process/upload
  5. `UpsertClip` con `drive_link`, `local_path`, `file_hash`

### 3. Pulizia log e rimozione hardcoded

- Verificare che non ci siano riferimenti hardcoded a `pizza`, `cooking`, `kitchen`
- Assicurare che il sistema funzioni con qualsiasi `term`
- Rimuovere log di debug eccessivi se necessario

### 4. Monitoraggio server

- Verificare che il server in `screen` session rimanga attivo nel tempo
- Controllare logs periodicamente per errori
- Considerare l'uso di `systemd` service per avvio automatico

## Endpoint Testing - Artlist API

### Test eseguiti il 2026-05-01 19:00

**1. Search Live Endpoint**
- `POST /api/artlist/search/live`
- Test: `{"term": "kitchen", "limit": 5}`
- Risultato: 5 clip trovate via scraper Node.js
- Clip restituite: showroom/kitchen, scientists, house interior, luxury property, workers

**2. Run Pipeline Endpoint**
- `POST /api/artlist/run`
- Test: `{"term": "kitchen", "limit": 5}`
- Run ID: `e23b8d41-73c5-4be7-a801-86cd25559108`
- Risultato: 10 clip trovate nel DB, 5 processate e caricate su Drive
- Strategy: `verify` (default)
- Drive folder: `19sma3SdHNLwlVY6_5Ozd6OMskDbvr_C_`

**3. Search Database Endpoint**
- `POST /api/artlist/search`
- Test: `{"term": "kitchen", "limit": 5}`
- Risultato: 10 clip nel DB con tag "kitchen"
- 4 clip già caricate su Drive (con `drive_link` e `file_hash`)
- 6 clip senza upload (solo ricerca live salvata)

**4. Run Status Endpoint**
- `GET /api/artlist/runs/:run_id`
- Monitoraggio stato del pipeline in tempo reale
- Transizione: `running` → `completed`
- Statistiche: `found: 10, processed: 5, failed: 0, skipped: 0`

### Documentazione Endpoints

Tutti gli endpoint sono documentati in `internal/api/handlers/artlist/`:
- **Public**: `/run`, `/runs/:id`, `/diagnostics`, `/search/live`
- **Internal** (richiede `X-Internal: true`): `/stats`, `/search`, `/sync-drive-folder`, `/sync-catalogs`, `/import-scraper-db`, `/clips/:id/status`, `/clips/:id/download`, `/clips/:id/upload-drive`, `/clips/process`

## Notes

- Il problema principale era il riuso di job zombie (`artlist run reused` su job morti)
- Il binario precedente non era allineato col codice (log mancanti)
- Ora il server mostra versione/commit all'avvio per evitare confusione
- La live search + save deve essere automatica, senza `node --save` manuale
