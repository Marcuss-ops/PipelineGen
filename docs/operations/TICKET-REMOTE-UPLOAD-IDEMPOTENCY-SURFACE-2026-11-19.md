# TICKET — Remote: upload session state machine and idempotency key builders without a caller (2026-11-19)

- **Status:** CLOSED (2026-11-19) — 0 unreachable functions under `internal/capabilities/remote`; guard 445 → 363
- **Severity:** P2 — protocol-looking helpers that no Go path and no contract file uses
- **Owner:** remote execution / protocol owner
- **Source:** `deadcode -test=false ./...` on `refactored@17752ccfb` (445 unreachable functions); frozen list in [`deadcode-2026-11-19.txt`](deadcode-2026-11-19.txt)
- **Rule:** delete only after confirming no interface, reflection or build tag reaches the symbol; no test assertion weakened

## Problem

`artifact_uploader.go` declares an upload session state machine
(`NewUploadSession`, `CanonicalUploadStateValues`, `NewIllegalTransitionError`,
`IllegalTransitionError.Error/Is`) with no caller.
`complete_job_idempotency.go` and `idempotency.go` declare the key builders
`CompleteJobIdempotencyKey`, `CompleteJobIdempotencyKeyDiagnostic`,
`IsValidCompleteJobIdempotencyKey`, `ArtifactIdempotencyKey` and
`IsValidIdempotencyKey`.

Nothing in `contracts/` or the docs names these keys; the only mention is the
dead-code plan. That does not prove they are unused on the wire: they may be the
producer half of a protocol whose consumer lives in the worker. The ticket must
settle that before deleting, because a key whose validation is dropped is a
duplicate-delivery hole, not just dead weight.

## Scope (measured)

| File | # |
|---|---:|
| `internal/capabilities/remote/artifact_uploader.go` | 5 |
| `internal/capabilities/remote/complete_job_idempotency.go` | 3 |
| `internal/capabilities/remote/idempotency.go` | 2 |
| **Total** | **10** |

Key symbols: `NewUploadSession`, `CompleteJobIdempotencyKey`, `IsValidCompleteJobIdempotencyKey`

## Plan

1. Search the Rust worker, the job payload schema and `contracts/` for the key
   format and the upload state names.
2. If the protocol needs them: add the production caller, validate at the boundary
   and add a test for the invalid-key and illegal-transition branches.
3. If not: delete the two files' dead declarations together with their tests and
   record that idempotency is owned by the worker.

## Acceptance criteria

- [ ] The key format is either consumed by a caller or proven absent from the
      protocol, with the search recorded here.
- [ ] 0 unreachable functions under `internal/capabilities/remote`.
- [ ] `go build ./...` and the package tests are green.
- [ ] The global deadcode guard records the new count.

## Verification

```sh
rg -n 'CompleteJobIdempotencyKey|ArtifactIdempotencyKey|upload_session' --glob '!*.md'
deadcode -test=false ./... | grep '^internal/capabilities/remote/'
go test ./internal/capabilities/remote -count=1
```

## Esito (2026-11-19)

**Ricerca di protocollo (registrata).** `rg -ni 'idempotenc|upload_session|X-Idempotency-Key'`
su `RenderingGen/`, `Chronon3d/`, `BrainRust/` (esclusi `target/`) e su
`contracts/` (solo `overlay-v2`) → nessuna occorrenza del formato chiave né
dell'header. Nel client Go vivo (`internal/platform/remote/jobbrokerclient`)
`PrepareArtifactUpload` consuma `remote.UploadSession` ma nessuna chiamata
imposta `X-Idempotency-Key`; i commenti nei due file citavano "8+ production
callers" che non esistono più. Il formato non è quindi consumato da nessun lato
del protocollo in questo repository.

**Ritirati (10):** `NewUploadSession`, `CanonicalUploadStateValues`
(`NewIllegalTransitionError`, `IllegalTransitionError.Error`,
`IllegalTransitionError.Is`), `CompleteJobIdempotencyKey`,
`CompleteJobIdempotencyKeyDiagnostic`, `IsValidCompleteJobIdempotencyKey`,
`ArtifactIdempotencyKey`, `IsValidIdempotencyKey`, con i file
`idempotency.go` e `complete_job_idempotency.go` interi, la factory
`idempotency_factory_test.go` e i test dei simboli rimossi. Ritirati anche
`ErrArtifactIdempotencyKeyConflict`/`ErrCompleteJobIdempotencyKeyConflict`
(sentinelle senza produttore) e `ErrIllegalUploadStateTransition` con il suo
envelope tipizzato: nessun rejection site lo costruiva, quindi
`errors.As(*IllegalTransitionError)` non poteva mai scattare.

**Cosa resta (vivo, invariato):** `UploadSession`, `UploadState`,
`UploadState.Valid`, `UploadState.IsValidTransition`, la porta
`ArtifactUploader`, `UploadSessionStore` e le sentinelle di upload
(`ErrArtifactUploaderNotConfigured`, `...SessionExpired`, `...SessionNotFound`,
`...RemoteSchemaVersionUnsupported`, `ErrArtifactCtxRequired`). L'idempotenza di
consegna resta di proprietà del worker/ricevitore, come registrato sopra.

Test: `TestUploadState_*` restano; i due punti che usavano
`CanonicalUploadStateValues()` ora elencano gli stati canonici esplicitamente
(stessa asserzione, nessun skip).

**Verifica:** `deadcode -test=false ./... | grep 'capabilities/remote/'` → vuoto;
`go build ./...`, `go test ./internal/capabilities/remote ./internal/platform/remote/...` verdi.
