# RA-10 — `overlaystatus`: un mutex globale serializza tutti i runID

**Data:** 2026-11-21
**Priorità:** P1 (serializzazione di I/O su disco fra run indipendenti)
**Stato:** DONE
**Owner:** `capabilities/scripts/overlaystatus`
**Origine:** report esterno 2026-11-21 §2.2; verificato sul codice
**Verifica sul codice:** **CONFERMATO** — `internal/capabilities/scripts/overlaystatus/status.go:78,103-104`

## 1. Problema

```go
var writeMu sync.Mutex        // riga 78
…
writeMu.Lock()                // riga 103
defer writeMu.Unlock()
```

Il lock è tenuto per l'intera sequenza `os.MkdirAll` → `os.ReadFile` → `json.MarshalIndent` →
`os.CreateTemp` → `Write` → `Close` → `Chmod` → `Rename` → `zap.L()`. Ogni aggiornamento di
stato di **qualsiasi** run blocca gli altri, anche se scrivono in directory diverse: il
parallelismo fra lane/job viene ridotto a uno. In più il lock globale è un punto unico di
contesa che maschera i problemi di concorrenza veri (due scritture sullo **stesso** runID).

## 2. Soluzione

1. **Lock per chiave** (runID o path normalizzato del file di stato):
   ```go
   type keyedMutex struct {
       mu    sync.Mutex
       locks map[string]*lockEntry   // entry con refcount per la rimozione
   }
   func (k *keyedMutex) lock(key string) func()
   ```
   Il refcount è necessario: senza, la mappa cresce come `mirrorInFlight` (RA-22).
   In alternativa, `singleflight.Group` con chiave = path, se la semantica è "una scrittura
   alla volta per file".
2. **Scrittura atomica per file**: `CreateTemp` nella **stessa directory** del target (già così,
   verificare) + `Rename`. Il lock per chiave copre solo il tratto read-modify-write dello
   stesso file.
3. **Ridurre la sezione critica**: serializzare l'JSON **fuori** dal lock (calcolo puro), e
   tenere il lock solo su read + write + rename. Se la lettura serve per il merge, mantenere
   read+write sotto lock ma spostare `MkdirAll` fuori (idempotente).
4. **`zap.L()` fuori dal lock**: il logging non deve dipendere dal lock di scrittura.
5. **Fallback**: se il numero di runID concorrenti è basso (3–6), la mappa per chiave è
   sufficiente; non introdurre una coda asincrona in questo ticket (cambierebbe la garanzia di
   lettura immediata dopo la scrittura).
6. **Test**: N goroutine su runID distinti devono completare in tempo ~1 scrittura, non N
   (misurare con un hook di test sul lock, non con un timer fragile); N goroutine sullo stesso
   runID devono produrre un file valido e nessun rename parziale.

## 3. Check finali

```bash
cd refactored
# 1. nessun mutex globale di scrittura
grep -rn "var writeMu sync.Mutex" internal/capabilities/scripts/overlaystatus/
# atteso: nessun output

# 2. concorrenza fra runID (test con barriera + timeout)
go test -race ./internal/capabilities/scripts/overlaystatus/ -run 'Concurrent|PerRun|Keyed' -v
# atteso: PASS; il test con 8 runID distinti non va in timeout stretto

# 3. stesso runID: file sempre valido
go test -race ./internal/capabilities/scripts/overlaystatus/ -run 'SameRun|Atomic' -v
# atteso: PASS; ogni lettura decodifica un JSON completo

# 4. nessun lock attorno al logging
grep -n "zap.L()" internal/capabilities/scripts/overlaystatus/status.go
# atteso: nessuna occorrenza dentro la sezione critica (verifica ispettiva)

# 5. build/vet/gate
go build ./... && go vet ./...
go run ./cmd/archcheck --strict   # exit 0
```

## 4. Rischi e rollback

- **Rischio**: la mappa dei lock per chiave cresce senza limite. Mitigazione: refcount e
  cancellazione alla release, con test che la mappa torna vuota.
- **Rischio**: due processi (non goroutine) sullo stesso file non sono coperti da un mutex in
  memoria. Se il file è condiviso fra processi, serve un lock su file (`flock`) — dichiararlo
  nel ticket: oggi la garanzia è solo intra-processo.
- **Rollback**: revert; il comportamento torna serializzato ma corretto.

## 6. Esito della verifica finale (2026-11-21)

- `writeMu` rimosso; `lockRun(key)` con `runLockEntry{mu, refs}` e cancellazione alla release.
- Nuovo test `TestUpdateConcurrentRunsStayIsolated`: 8 runID × 20 update concorrenti, ogni
  documento finisce sull'ultimo stato, la tabella dei lock torna vuota.
- `go test -race ./internal/capabilities/scripts/overlaystatus/` → ok.
