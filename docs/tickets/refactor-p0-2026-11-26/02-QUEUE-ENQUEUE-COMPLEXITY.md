# P0-02 — `queue.Enqueue`: cyclo 73 → helper nominati

**Data:** 2026-11-26
**Priorità:** P0 (ogni job passa da qui; funzione centrale e poco coperta direttamente)
**Stato:** **DONE**
**Area:** `internal/capabilities/jobs/queue`

## 1. Problema misurato

`gocyclo` su `internal/capabilities/jobs/queue/service.go`:

```
73 queue (*Service).Enqueue service.go:56
```

La funzione mescola in un unico corpo:

- registrazione del run padre (`kernobs` deferred);
- validazione della richiesta;
- risoluzione della correlation ID;
- tre pre-check di idempotenza (active key, `(type, correlation)`,
  `(client, idempotency)`);
- marshal e limite del payload;
- costruzione e validazione dello scheduling descriptor (incluso il confronto
  con `payload.render_phase`);
- costruzione del `job.Job`;
- consumer binding e `MaxRetries`;
- risoluzione `ParentJobID`/`RootJobID`;
- create + recovery post-duplicate.

La suite esistente copre il comportamento (`service_test.go`, `schedule_test.go`,
`enqueue_test.go`), ma non rende leggibile il ramo che fallisce.

## 2. Soluzione

Estrarre, senza cambiare ordine né messaggi:

| Helper | Responsabilità |
| --- | --- |
| `resolveCorrelationID` | richiesta → context → vuoto |
| `findExistingBeforeCreate` | i tre pre-check, con la stessa policy transient |
| `encodeEnqueuePayload` | marshal, `nil`, `MaxPayloadSize` |
| `buildSchedulingDescriptor` | scheduled/status, descriptor, `render_phase`, `Validate` |
| `newJobFromRequest` | costruzione del `job.Job` |
| `applyConsumerAndRetryPolicy` | `RequireConsumer`, `ResolveMaxRetries`, payload `{}` |
| `resolveParentRoot` | `ParentJobID` / `RootJobID` |
| `findExistingAfterDuplicate` | recovery dei tre tipi di chiave dopo `ErrDuplicate` |

`Enqueue` resta l'orchestratore con named return e il `defer` sul run padre.

## 3. Test

La suite esistente è il contratto:

```bash
go test ./internal/capabilities/jobs/queue/... -count=1
```

In aggiunta, il ticket richiede che il comportamento duplicate resti quello
descritto da `TestServiceEnqueue_DuplicateUsesKernelSentinelAndReturnsExisting`
e `TestServiceEnqueue_DuplicateWithoutRecoverableKeyPropagates`.

## 4. Check finali

```bash
gocyclo -over 40 internal/capabilities/jobs/queue   # atteso: nessun output
gocyclo -top 5 internal/capabilities/jobs/queue     # atteso: Enqueue <= 40
go test ./internal/capabilities/jobs/queue/... -count=1
go build ./... && go vet ./...
go run ./cmd/archcheck --strict
```

## 5. Evidenza

**2026-11-26, HEAD `cd43abdb7`.**

- `gocyclo internal/capabilities/jobs/queue/service.go`: `(*Service).Enqueue`
  **73 → 22**; l'helper più complesso è `buildSchedulingDescriptor` a **19**.
- `go test ./internal/capabilities/jobs/queue/... -count=1` →
  `ok github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/queue 1.028s`
- `go build ./...` → exit 0; `go vet ./...` → exit 0.
- Il ramo duplicate conserva la precedenza active-key → correlation →
  client/idempotency e restituisce l'errore grezzo quando nessuna chiave è
  recuperabile (`findExistingAfterDuplicate`).

## 6. Rischi e rollback

- **Rischio:** cambiare l'ordine dei pre-check cambia quale job esistente viene
  restituito. Mitigazione: gli helper chiamano gli stessi metodi nello stesso
  ordine (active key → correlation → client/idempotency).
- **Rischio:** perdere il wrapping dell'errore duplicate. Mitigazione: il ramo
  `ErrDuplicate` restituisce l'errore grezzo quando nessuna chiave è
  recuperabile, come prima.
- **Rollback:** revert del singolo file; nessun contratto cambia.
