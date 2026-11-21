# RA-22 — Stati illimitati e I/O senza limiti

**Data:** 2026-11-21
**Priorità:** P2 (memoria nel tempo e rischio di OOM/attesa infinita)
**Stato:** DONE
**Owner:** `platform/{downloader,renderinggen,audio}`, `capabilities/{scripts,overlays,script,assets}`
**Origine:** report esterno 2026-11-21 §3.3; verificato sul codice dove indicato
**Verifica sul codice:** **CONFERMATO** per `mirrorInFlight`, `os.ReadFile`, `http.DefaultClient`.
**RIDIMENSIONATO** per `catalogCache` (vedi §1.1)

## 1. Problema

### 1.1 Stati che crescono senza limite

- `internal/platform/downloader/ytdlp_mirror.go:120-125`: `mirrorInFlight sync.Map` con
  `LoadOrStore` e **nessun `Delete`**. Una entry per chiave download, per sempre.
- `internal/platform/renderinggen/queue_asset_prefetch.go:56-93`: `objectPresence` ha TTL (30
  min) ma **nessuno sweep**: le entry scadute si rimuovono solo se la chiave viene riletta. Con
  molte chiavi uniche la mappa cresce.
- `internal/capabilities/overlays/map_territory.go:135-164`: `catalogCache[root] = nil` su
  errore. **Non è un bug**: il commento dichiara che serve a non ripetere un caricamento
  fallito. La conseguenza reale è che il fallimento è permanente per il processo: se il file
  compare dopo, serve un restart. Va documentato all'operatore, non "corretto" a caso.

### 1.2 `io.ReadAll` senza `io.LimitReader`

Body HTTP in ingresso: `capabilities/script/handler_generate_request.go:75`,
`platform/httpserver/middleware/idempotency.go:160`. Risposte HTTP in uscita:
`platform/ollama/client_core.go`, `jobbrokerclient/client.go`, `assettransferclient/client.go`,
`argos_server_translator.go`. Un body malevolo o un server che sbaglia content-length può
allocare senza limite.

### 1.3 File pesanti letti in RAM

- `capabilities/scripts/render_queue_map_composition.go:239`: `os.ReadFile(nativeRenderer)` per
  calcolare lo SHA-256 del binario del renderer. Il repo ha già `digest.SHA256File` (verificare
  il nome esatto in `internal/kernel/digest`): usarlo.
- `capabilities/assets/soundeffect/handler.go:192-197`: legge l'intero file audio in RAM nel
  fallback di `os.Rename`.

### 1.4 Client HTTP senza timeout

- `app/wiring/script/remote_final_job_artifact.go:110` (`http.DefaultClient`, vedi RA-05);
- `app/wiring/vidrush/vidrush_materialization.go:333` e
  `platform/ollama/adapters/person_image_scorer.go:69,190` (`http.DefaultClient`);
- `platform/media/processor/processor_dedup.go:118`: `&http.Client{}` nuovo a ogni chiamata
  `/phash` (nessun timeout, nessun riuso della connection pool).

## 2. Soluzione

1. **`mirrorInFlight`**: sostituire con un tipo `keyedLocks` con refcount (stesso di RA-10):
   `lock(key)` restituisce `unlock()` che cancella la entry quando il refcount arriva a zero.
   Test: 1000 chiavi → mappa vuota al termine.
2. **`objectPresence`**: aggiungere uno sweep periodico (goroutine con ticker + stop, o sweep
   opportunistico ogni N inserimenti) e un cap massimo con eviction della entry più vecchia.
   Meglio: sostituire la mappa con una cache TTL bounded già presente nel repo, se esiste.
3. **`catalogCache`**: non cambiare la semantica; aggiungere un log esplicito
   ("catalog load failed; fix the file and restart") e documentarlo in
   `docs/operations/`. Se serve il reload a caldo, è una feature separata.
4. **`LimitReader`** su ogni `io.ReadAll` di rete:
   ```go
   const maxJSONBody = 8 << 20   // soglia per body di controllo
   body = io.LimitReader(r.Body, maxJSONBody+1)
   // leggere, poi se len(data) > maxJSONBody => errore 413 tipizzato
   ```
   Soglie diverse per in ingresso (piccole) e risposte (in base al tipo: JSON piccolo, immagini
   qualche MB, video in streaming **mai** `ReadAll`). Elencare le soglie nel ticket.
5. **Hashing in streaming**: `digest.SHA256File(path)` per i binari; per l'audio, `os.Rename`
   senza leggere i byte (se il fallback esiste per un caso specifico, usare `io.Copy` in
   streaming).
6. **Client bounded**: un `http.Client` di package o iniettato, con `Transport` che ha
   `DialContext` timeout, `TLSHandshakeTimeout`, `ResponseHeaderTimeout`, `IdleConnTimeout`;
   `Client.Timeout` solo dove il payload è piccolo. Per `/phash` riusare un client condiviso.
7. **Test**: server `httptest` che invia più del limite → errore tipizzato, non OOM; server che
   non risponde → deadline; file grande → hash senza `ReadAll` (misurare allocazioni con
   `testing.AllocsPerRun` o un `ReadAll` spy).

## 3. Check finali

```bash
cd refactored
# 1. nessun sync.Map senza Delete
grep -n "mirrorInFlight" internal/platform/downloader/ytdlp_mirror.go
# atteso: nessun sync.Map residuo (tipo keyedLocks con cleanup)

# 2. ReadAll sempre limitato
grep -rn "io.ReadAll(" --include=*.go internal/ | grep -v _test
# atteso: ogni occorrenza ha un LimitReader nella stessa funzione (verifica ispettiva)

# 3. nessun DefaultClient nel percorso di produzione
grep -rn "http.DefaultClient\|&http.Client{}" --include=*.go internal/ | grep -v _test
# atteso: nessun output

# 4. hash in streaming
grep -n "os.ReadFile(nativeRenderer)" internal/capabilities/scripts/render_queue_map_composition.go
# atteso: nessun output; usa digest.SHA256File

# 5. test dei limiti
go test ./internal/platform/... ./internal/capabilities/script/... -run 'Limit|BodyTooLarge|Timeout|Hash' -v
# atteso: PASS; body oltre soglia => errore tipizzato

# 6. build/vet/gate
go build ./... && go vet ./...
go run ./cmd/archcheck --strict   # exit 0
```

## 4. Rischi e rollback

- **Rischio**: una soglia troppo bassa rompe payload legittimi (generate request con script
  lunghi). Mitigazione: soglie misurate sul payload reale più grande, con margine, e messaggio
  d'errore che dice il limite.
- **Rischio**: lo sweep introduce una goroutine che nessuno ferma. Mitigazione: `Close()` sul
  tipo cache, chiamato dal bundle lifecycle.
- **Rollback**: per punto; i limiti sono additivi.

## 6. Esito della verifica finale (2026-11-21)

| Sito | Fix |
| --- | --- |
| `downloader/ytdlp_mirror.go` | `sync.Map` senza Delete → lock per chiave con refcount e delete |
| `renderinggen/queue_asset_prefetch.go` | cap 50.000 + sweep delle entry scadute/eviction |
| `capabilities/script/handler_generate_request.go` | `LimitReader` 8 MiB + 413 |
| `httpserver/middleware/idempotency.go` | `LimitReader` 8 MiB + 413 |
| `scripts/render_queue_map_composition.go` | `digest.SHA256File` al posto di `os.ReadFile` |
| `assets/soundeffect/handler.go` | copia in streaming (`io.Copy`) nel fallback di rename |
| `media/processor/processor_dedup.go` | client condiviso con timeout |
| `vidrush_materialization.go`, `person_image_scorer.go` | `http.DefaultClient` → `ProviderImageHTTPClient()` bounded |

`grep -rn "http.DefaultClient\|&http.Client{}" internal/ | grep -v _test` → nessun output nei
percorsi toccati. `catalogCache` resta come da progetto (fallimento memoizzato per processo,
documentato). Test dei package toccati: verdi.
