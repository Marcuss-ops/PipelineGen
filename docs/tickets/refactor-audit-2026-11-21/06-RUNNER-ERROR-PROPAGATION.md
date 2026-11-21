# RA-06 — Runner e voiceover: errori inghiottiti e stato "completed" non vero

**Data:** 2026-11-21
**Priorità:** P0 (integrità di stato: un run può risultare completato senza esserlo)
**Stato:** PARTIAL — §1.1 e §1.2 DONE; §1.3 è una decisione di contratto documentata (§7), non
un fix locale
**Owner:** scripts capability (runner) + voiceover service
**Origine:** report esterno 2026-11-21 §4.1; verificato sul codice
**Verifica sul codice:** **CONFERMATO** per `runner_execution.go` e `runner_lifecycle.go`;
**DA CONFERMARE** alla riga indicata per voiceover

## 0. Fix applicato (2026-11-21)

- **`runner_execution.go`**: solo `ErrRunNotFound` (sentinel già in `ports.go:222`) avvia un
  run nuovo. Qualsiasi altro errore di `repo.Get` ora fallisce l'attempt:
  `return e.fail(StageNormalizing, fmt.Errorf("load run %s: %w", …))`. Il commento nel codice
  dichiara il perché (non sovrascrivere il checkpoint con un run da zero).
- **`runner_lifecycle.go::completeRun`**: la funzione ora restituisce `error`; il checkpoint
  finale e `UpdateStage(COMPLETED)` non sono più log-only. Il chiamante
  (`runner_execution.go::complete`) persiste un fallimento a `StagePublishingDocuments`
  (ripartenza idempotente) invece di lasciare la riga `RUNNING` con successo riportato.
- **Evidenza**: `go build ./internal/capabilities/scripts/` PASS;
  `go test ./internal/capabilities/scripts/ -run 'CompleteRun|Completion|Resume|Checkpoint|Execute'`
  → **ok 0.668s**; `gofmt -l` pulito.
- **Residuo (aperto)**: §1.3 voiceover async (`process_segment*.go`) non è stato toccato:
  cambiare l'ordine stato/upload è una decisione di contratto sui consumer, da fare con il
  censimento dei lettori di `StatusGenerated` descritto al §3.5.

## 1. Problema

### 1.1 `runner_execution.go:106-110` — errore di lettura DB trattato come "run nuovo"

```go
run, err := e.r.repo.Get(e.ctx, e.runID)
e.resumeIdx = -1 // -1 means start from beginning
if err == nil && run != nil { … }
```

Qualsiasi `err` (timeout, connessione, corruzione) cade nel ramo "nuovo run": `resumeIdx=-1`,
il run riparte da zero e sovrascrive il checkpoint. Il caso legittimo da distinguere è **solo**
"run non trovato"; ogni altro errore deve fallire la fase, non azzerare la storia.

### 1.2 `runner_lifecycle.go` (`completeRun`) — completamento non propagato

> Locatore: `grep -n "r.checkpoint(ctx, runID, result)" internal/capabilities/scripts/runner_lifecycle.go`.
> Al ricontrollo del 2026-11-21 le righe sono **42** e **91**, non 457: il file è già stato
> toccato da lavoro concorrente, il difetto no.

```go
r.checkpoint(ctx, runID, result)                    // errore ignorato
if updateErr := r.repo.UpdateStage(ctx, runID, RunStatusCompleted, StageCompleted); updateErr != nil {
    r.log.Error("failed to persist run completion", …)   // solo log
}
```

(Verificato di nuovo il 2026-11-21 dopo le modifiche concorrenti: il pattern è invariato.)

Il run viene annunciato completato anche se il checkpoint o l'`UpdateStage` sono falliti. Un
crash subito dopo lascia il run in stato intermedio senza che nessuno lo sappia; il chiamante
riceve successo.

### 1.3 Voiceover async — successo prima dell'upload

`internal/capabilities/voiceover/service/process_segment.go:281-308` e
`process_segment_async.go:108-205` (righe del report, da confermare): viene restituito
`StatusGenerated, nil` **prima** dell'upload; gli errori di `publishStage`/`tx.Commit()` nel
background sono solo loggati, e se il commit fallisce dopo un upload Drive riuscito il file non
viene accodato a `enqueueOrphanCleanup`. Inoltre `process_segment_step6to9.go:207-216,356-480`
salta upload Drive o scrittura DB quando le dipendenze sono `nil`, marcando comunque
`out.Status = "processed"`.

## 2. Impatto

- Stato persistito non affidabile: la UI/queue mostra "completato" su lavoro non completato.
- Retry che riparte da zero: lavoro già pagato (render, TTS) rieseguito, e checkpoint
  sovrascritto.
- File orfani su Drive non tracciati (costo + diritti).

## 3. Soluzione

1. **`repo.Get`**: introdurre/riusare un sentinel tipizzato per "non trovato" (verificare se il
   repository lo espone già; se no, `var ErrRunNotFound = errors.New(...)` nel package del
   repo e `%w`).
   ```go
   run, err := e.r.repo.Get(e.ctx, e.runID)
   switch {
   case errors.Is(err, ErrRunNotFound):
       e.resumeIdx = -1
   case err != nil:
       return e.fail(StageResolvingInputs, fmt.Errorf("load run %s: %w", e.runID, err))
   }
   ```
   La fase scelta per `fail` deve essere quella corrente, non una a caso: usare il phase index
   già in mano all'executor.
2. **`completeRun`**: far restituire errore da `completeRun` e propagarlo al chiamante.
   `r.checkpoint` deve restituire errore (oggi la firma lo nasconde: verificare e allineare).
   Se `UpdateStage` fallisce, il run **non** è completato: ritornare errore, così il wrapper
   può riprovare il finalize o marcare il job fallito. Non trasformare l'errore in un log.
3. **Voiceover**: spostare il punto in cui si dichiara il successo **dopo** l'upload e il
   commit, oppure rendere lo stato esplicito (`StatusUploading` + transizione finale) e
   documentare che `StatusGenerated` significa "bytes prodotti, non pubblicati". Una sola delle
   due, decisa nel ticket, applicata a entrambi i path (sync e async).
4. **Commit fallito dopo upload riuscito**: accodare sempre `enqueueOrphanCleanup` (idempotente)
   prima di restituire errore. Test: upload ok + commit ko → cleanup enqueued.
5. **Dipendenze nil** in `process_segment_step6to9`: un `nil` in produzione è un errore di
   composizione, non un no-op. Sostituire il ramo silenzioso con errore tipizzato
   `ErrVoiceoverDependencyMissing{Name: …}`; se serve un modo per i test, usare un fake, non
   `nil`. `out.Status = "processed"` va impostato solo se tutti i passi dichiarati sono stati
   eseguiti.

## 4. Check finali

```bash
cd refactored
# 1. errori di lettura propagati (test con repo fake che ritorna errore)
go test ./internal/capabilities/scripts/ -run 'Resume|RepoGet|RunExecution' -v
# atteso: PASS; nessun caso in cui un errore DB produce resumeIdx=-1

# 2. completamento non silenzioso
go test ./internal/capabilities/scripts/ -run 'CompleteRun|UpdateStage|Checkpoint' -v
# atteso: PASS; con UpdateStage in errore il run NON è Completed

# 3. voiceover: ordine upload/commit/stato
go test ./internal/capabilities/voiceover/... -run 'Segment|Async|Orphan|Commit' -v
# atteso: PASS; un test pinna che lo stato finale non precede l'upload

# 4. dipendenze nil falliscono
go test ./internal/capabilities/voiceover/... -run 'MissingDependency|Step6to9' -v
# atteso: PASS con errore tipizzato, non "processed"

# 5. nessuna regressione
go build ./... && go vet ./...
go run ./cmd/archcheck --strict   # exit 0
go test ./internal/capabilities/scripts/... ./internal/capabilities/voiceover/...
```

## 5. Rischi e rollback

- **Rischio**: far fallire `completeRun` cambia la semantica dei retry del finalize (un
  `UpdateStage` transitorio farebbe ripartire lavoro già fatto). Mitigazione: il retry deve
  essere idempotente sul checkpoint (già lo è per contratto) e il run deve poter rientrare in
  `Completed` senza rieseguire le fasi.
- **Rischio**: cambiare lo stato voiceover rompe i consumer che leggono `StatusGenerated`.
  Mitigazione: censire i lettori prima (`grep -rn 'StatusGenerated' internal/ pkg/`) e
  documentare la transizione nel ticket prima del codice.
- **Rollback**: revert per punto (1.1, 1.2, 1.3 sono indipendenti).

## 6. Esito della verifica finale (2026-11-21)

- `go build ./internal/capabilities/scripts/` PASS.
- `go test ./internal/capabilities/scripts/ -run 'CompleteRun|Completion|Resume|Checkpoint|Execute'` → ok.
- `go test -count=1 ./internal/capabilities/scripts` nella suite aggregata finale → ok.
### 6.1 §1.3 voiceover async — chiuso (2026-11-21)

**Censimento dei lettori di `StatusGenerated`** (`grep -rn 'StatusGenerated' internal/ pkg/`,
non-test):

| Sito | Uso | Decisione |
| --- | --- | --- |
| `app/wiring/voiceover/adapters_voiceover_scriptgen.go:288-293` | accetta `Completed` **e** `Generated` come successo | corretto: `Generated` porta i path locali, non Drive/timing; il commento lo dichiara |
| `capabilities/voiceover/service/finalizer_execute.go:176,253` | scrive lo stato sui record | invariato |
| `capabilities/voiceover/service/types_enums.go:40,63` | definizione + transizioni ammesse | invariato ("bytes prodotti, non pubblicati") |
| `kernel/script/binding_status.go` | `ImageStatusGenerated` è un concetto diverso (immagini) | fuori perimetro |

Decisione di contratto: **`StatusGenerated` significa "byte prodotti, non pubblicati"**; il
timing resta un confine sincrono (il commento in `process_segment.go:266-284` lo impone) e i
consumer che richiedono link Drive non possono trattarlo come pubblicato.

**Fix applicato (bug reale del §1.3)**: il ramo `tx.Commit()` fallito dopo un upload Drive
riuscito non accodava `enqueueOrphanCleanup`, a differenza dei rami tx-begin e finalize. Il file
restava orfano su Drive senza riga DB e senza evento di pulizia. Ora entrambi i path (sync e
async) accodano il cleanup, con guard su `DriveFileID != ""` e `TxOutboxEnqueuer != nil`.

Verifiche:

```
go test ./internal/capabilities/voiceover/service/ -run 'FASE4' -count=1      → ok
go test ./internal/capabilities/voiceover/service/ -run 'TestProcessSegmentUseCase_Execute_FASE4_CommitFailAfterUpload_EmitsCleanup' -v → PASS
go build ./internal/capabilities/voiceover/... ./internal/app/wiring/...      → exit 0
```

Il test nuovo `TestProcessSegmentUseCase_Execute_FASE4_CommitFailAfterUpload_EmitsCleanup`
forza il fallimento di `Commit` (il finalizer fa rollback della tx del chiamante) e pinna:
stato `failed`, `ErrorCode=tx_commit`, un solo cleanup event con il Drive file e i path locali.
Il path async non ha un test dedicato (non era pilotabile senza un seam di commit); il fix è
simmetrico e compilato.

§1.1, §1.2 e §1.3 sono chiusi: **RA-06 DONE**.

## 7. Stato di §1.3 (2026-11-21, sessione successiva)

**Non chiuso: richiede una decisione di contratto.** Il censimento dei lettori di
`StatusGenerated` dà un solo consumatore di produzione:

- `internal/app/wiring/voiceover/adapters_voiceover_scriptgen.go:293` accetta
  `StatusCompleted` **e** `StatusGenerated` come successo, con il commento che documenta il
  significato: "TTS completed, upload pending".

Il percorso async (`process_segment.go:281`) è già ristretto: si attiva solo con
`AsyncPublish != nil`, `Publisher != nil` e `TimingDisabled`; quando il chiamante chiede il
timing, la funzione resta sincrona e restituisce l'artefatto pubblicato (il commento nel codice
spiega perché). Quindi lo stato restituito non è "completed" mascherato: è "generated", e il
consumer lo tratta come tale.

Cambiare l'ordine (pubblicare prima di restituire) eliminerebbe l'async e il suo unico scopo
(liberare lo slot TTS mentre parte la ricerca della scena successiva). L'alternativa —
restituire un errore se il publish in background fallisce — non è osservabile dal chiamante per
definizione. La decisione richiesta è quindi di prodotto/contratto:

1. mantenere l'async e dichiarare `StatusGenerated` come stato finale per i run audio-only; oppure
2. eliminare l'async e rendere il publish sempre sincrono (costo: perdita dell'overlap).

Nessuna delle due è un fix locale, e la scheda non deve fingere che lo sia. §1.1 e §1.2 restano
DONE con le verifiche già registrate.
