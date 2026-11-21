# RA-08 — Retry: spin loop, sleep finale, jitter e contesto

**Data:** 2026-11-21
**Priorità:** P1 (carico DB e latenza inutile sotto errore)
**Stato:** DONE
**Owner:** `capabilities/jobs/worker`, `platform/{audio,sqlite}`, `scripts`
**Origine:** report esterno 2026-11-21 §4.2; verificato parzialmente
**Verifica sul codice:** **CONFERMATO** per `runner_lease.go:114-150`, `audio/processor.go:202-230`,
`retention.go` (sleep finale). **CORRETTO**: il ctx **è** controllato in `audio/processor.go`
e in `retention.go` (il report dice il contrario).

## 1. Problema

### 1.1 Spin loop sul claim — `capabilities/jobs/worker/runner_lease.go:114-150`

```go
err := retry.Do(ctx, func() error { … r.broker.Claim(...) … }, retry.Options{
    MaxAttempts: 5, InitialBackoff: 200*time.Millisecond, MaxBackoff: 2*time.Second,
    BackoffFactor: 2.0, JitterFraction: 0, DisableJitter: true, IsRetryable: retry.IsTransient,
})
if err != nil {
    if errors.Is(err, jobs.ErrNoWorkerCapabilities) { return err }
    r.log.Warn("claim failed (retry budget exhausted)", zap.Error(err))
    continue          // ← nessuna attesa: nuovo giro immediato
}
```

Se `Claim` fallisce con un errore **non transiente diverso da `ErrNoWorkerCapabilities`**
(es. DB chiuso, schema errato, permessi), `retry.Do` esce al primo tentativo e il `continue`
riparte a 0 ms: loop caldo che martella il broker/DB e satura i log.

### 1.2 Sleep dopo l'ultimo tentativo

- `platform/audio/processor.go:202-230`: il `select` con `time.After(300ms)` è alla fine del
  corpo del `for`, quindi scatta **anche** dopo il terzo fallimento, prima di uscire. 300 ms
  buttati per richiesta fallita.
- `platform/sqlite/retention.go:210-225` (`execRetentionWithRetry`): dopo l'ultimo `attempt`
  dorme `(attempt+1)*2s` e poi esce; `checkpointWALWithRetry` (righe 236-260) dorme 3 s anche
  dopo il ventesimo `busy`.

### 1.3 Altri difetti della stessa classe

- `platform/sqlite/jobs/result_store.go:106-206`: fino a 12 tentativi CAS consecutivi senza
  attesa né `ctx.Err()`.
- `capabilities/scripts/final_job_image_preflight.go:120-147`: ritenta anche 403/404 con
  backoff lineare e senza jitter.
- `DisableJitter: true` su operazioni contese multi-worker:
  `worker_finalize_paths.go:203` e `runner_lease.go:136` — sincronizza i retry di N worker
  sullo stesso istante (thundering herd).

## 2. Soluzione

1. **Classificare prima di ritentare.** Per ogni retry esistente, definire esplicitamente:
   transitorio (rete, 5xx, busy, timeout), terminale (4xx di validazione/auth, 404 su asset
   immutabile), misconfig (`ErrNoWorkerCapabilities`). La classificazione va in una funzione
   con test, non in un `if` sparso.
2. **`runner_lease`**: sostituire il `continue` nudo con un'attesa limitata e cancellabile:
   ```go
   if err != nil {
       if errors.Is(err, jobs.ErrNoWorkerCapabilities) { return err }
       select {
       case <-ctx.Done(): return ctx.Err()
       case <-time.After(claimFailureBackoff):   // es. 1s, con jitter
       }
       continue
   }
   ```
   In alternativa, classificare gli errori non transitori come fatali e uscire dal `Run`
   (la scelta va dichiarata nel ticket: un DB irraggiungibile all'avvio è fatale, un blip no).
3. **Eliminare lo sleep finale** in `audio/processor.go` e `retention.go`: spostare l'attesa
   all'inizio della sola iterazione successiva (`if attempt == last { break }` prima del
   `select`), oppure usare `for attempt := 0; attempt < max; attempt++ { …; if attempt < max-1 { sleep } }`.
4. **Jitter**: `JitterFraction > 0` (es. 0.2–0.5) e `DisableJitter: false` su claim e finalize.
   Se un punto *deve* essere deterministico, documentarlo nel codice e nel test.
5. **CAS loop** in `result_store.go`: aggiungere `ctx.Err()` a ogni giro e un'attesa breve con
   jitter fra i tentativi (o passare a `UPDATE … WHERE version = ? RETURNING` con retry
   esterno). Il numero di tentativi resta, ma non deve essere un busy loop.
6. **Preflight immagini**: non ritentare 4xx. Solo 408/429/5xx e errori di trasporto.
7. **Test con clock iniettato** (se il repo ha già un `clock` interface — verificare
   `internal/kernel`): asserire numero di tentativi, tempo di attesa totale e assenza di sleep
   dopo l'ultimo. Se non c'è un clock iniettabile, usare un `retry.Sleeper` iniettato nella
   funzione.

## 3. Check finali

```bash
cd refactored
# 1. niente attese dopo l'ultimo tentativo (test con sleeper finto che conta le chiamate)
go test ./internal/platform/audio/ -run 'Retry|Backoff' -v
go test ./internal/platform/sqlite/ -run 'Retention|Retry' -v
# atteso: PASS; sleep count == attempts-1

# 2. claim non produce spin (test con broker fake che fallisce N volte)
go test ./internal/capabilities/jobs/worker/ -run 'Claim|Run.*Backoff|NoWorkerCapabilities' -v
# atteso: PASS; il numero di chiamate in 1s è limitato, non migliaia

# 3. CAS rispetta il ctx
go test ./internal/platform/sqlite/jobs/ -run 'ResultStore|CAS|Context' -v
# atteso: PASS; ctx cancellato interrompe il loop

# 4. jitter attivo dove dichiarato
grep -rn "DisableJitter: *true" internal/ | grep -v _test
# atteso: nessun output, oppure solo siti con motivo documentato accanto

# 5. preflight non ritenta 4xx
go test ./internal/capabilities/scripts/ -run 'ImagePreflight|Preflight' -v
# atteso: PASS; 403/404 => 1 tentativo

# 6. build/vet/gate
go build ./... && go vet ./...
go run ./cmd/archcheck --strict   # exit 0
```

## 4. Rischi e rollback

- **Rischio**: introdurre attese peggiora la latenza del claim quando il broker è davvero
  vuoto. Mitigazione: il caso "nessun job" deve restare veloce (`WaitSeconds: 20` già blocca
  lato server); l'attesa aggiunta riguarda solo il ramo di errore.
- **Rischio**: rendere fatale un errore transitorio uccide il worker. Mitigazione: uscire solo
  su errori classificati misconfig/fatali, con test dedicato.
- **Rollback**: revert per file; nessun dato coinvolto.

## 6. Esito della verifica finale (2026-11-21)

| Sito | Fix |
| --- | --- |
| `jobs/worker/runner_lease.go` | jitter 0.3, `DisableJitter:false`, attesa 500–1500 ms cancellabile dopo un claim fallito non transiente (niente spin) |
| `platform/audio/processor.go` | nessuno sleep dopo l'ultimo tentativo |
| `platform/sqlite/retention.go` | nessuno sleep dopo l'ultimo tentativo (entrambi i loop) |
| `platform/sqlite/jobs/result_store.go` | `ctx.Err()` a ogni giro + attesa jitterata sul CAS perso |
| `scripts/final_job_image_preflight.go` | 4xx (tranne 408/429) termina subito |
| `jobs/worker_finalize_paths.go` | jitter attivo |

Test: `go test ./internal/platform/sqlite/... ./internal/platform/audio/ ./internal/capabilities/jobs/worker/` → tutti ok.
