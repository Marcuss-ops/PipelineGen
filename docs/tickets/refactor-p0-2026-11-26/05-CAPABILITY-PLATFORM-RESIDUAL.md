# P1-05 — Archi `capabilities → platform` e coppie bidirezionali residue

**Data:** 2026-11-26
**Priorità:** P1 (strutturale; nessuna violazione hard, ma il core dipendeva da adapter concreti)
**Stato:** **DONE (2026-11-26)** — 2 famiglie di capability migrate con porte + fake test, 1 verificata già pulita; archi core 138 → 127, coppie bidirezionali 18 → 17.
**Area:** `internal/capabilities`, `internal/kernel`, `internal/platform`
**Relazione:** prosegue RA-11 (`docs/tickets/refactor-audit-2026-11-21/11-CAPABILITY-PLATFORM-CYCLES.md`), che ha chiuso 19→18 coppie.

## 1. Misura di partenza (2026-11-26, pre-intervento)

`go list -f '{{.ImportPath}}|{{join .Imports " "}}' ./internal/capabilities/...`
e filtro su `/internal/platform/`:

- **157 archi** totali da **81 package** di capability; **138** escludendo i
  sottopackage `/adapters`;
- `capabilities/jobs` e i suoi sottopackage importavano 9 volte
  `platform/sqlite/outboxevents` (`jobs`, `jobs/finalize`, `jobs/handlers`,
  `jobs/indexrestore`, più `publish_drive`, `publish_outbox`, `operations`,
  `texttracks`, `assets/providers/stock/enrichment`);
- **6** package di core non-adapter importavano `platform/drive`, fra cui
  `assets/assetop`, `assets/assettree`, `assets/lifecycle`.

## 2. Perché è un problema

Un cambio di schema SQLite, di provider Drive o di trasporto HTTP attraversa il
core della capability. Il test isolato richiede l'adapter concreto. Non è un
errore di compilazione (Go vieta i cicli di package), è un errore di
**direzione**: il contratto non ha un proprietario neutro.

## 3. Acceptance e esito

1. **Nessun nuovo arco core non-adapter** verso
   `platform/{sqlite,postgres,drive,httpserver}`: nessun arco aggiunto;
   `percheck_platform_port_boundary` e `percheck_capability_sql_ban` restano
   verdi (archcheck strict 0 violazioni). **OK**
2. **Ridurre di almeno 3 famiglie** partendo da:
   - `capabilities/jobs` → `sqlite/{outboxevents,assets/channels}`: la famiglia
     outbox è **azzerata** per `jobs`, `jobs/finalize`, `jobs/handlers`,
     `jobs/indexrestore` (9 → 1 archi `outboxevents` in tutto il core, residuo
     `assets/providers/stock/enrichment` che costruisce il repository);
   - `capabilities/youtube/usecase`: **verificata già pulita** (soli archi
     `ai/classifier` e `portutil`, fuori dal set nominato);
   - `capabilities/assets/{assetop,assettree,lifecycle}` → `drive`: **migrata**
     (drive 6 → 3, e la coppia bidirezionale `capabilities/assets ↔
     platform/drive` è **scomparsa**: 18 → 17 coppie).
   Famiglie con archi core ridotti: `capabilities/jobs` (4 package),
   `capabilities/assets` (assetop, assettree, lifecycle, texttracks),
   `capabilities/{publish_drive,publish_outbox,operations}`. **OK**
3. **Ogni famiglia migrata ha un test con un fake della porta**, non l'adapter:
   vedi §5. **OK**
4. **Misura finale in calo o invariata** su archi core e coppie: 138 → **127**
   archi core (157 → 146 totali), 18 → **17** coppie. **OK**

## 4. Check

```bash
# archi core (esclude /adapters)
go list -f '{{.ImportPath}}|{{join .Imports " "}}' ./internal/capabilities/... \
  | awk -F'|' '$1 !~ /\/adapters(\/|$)/ {n=split($2,a," "); for(i=1;i<=n;i++) if(a[i] ~ /\/internal\/platform\//) print $1" -> "a[i]}' \
  | wc -l
go run ./cmd/archcheck --strict
make verify-cyclo
go build ./...
go test ./internal/capabilities/... -count=1
```

## 5. Intervento (2026-11-26)

### 5.1 Contratto outbox nel kernel — `internal/kernel/outbox`

Spostato nel kernel tutto il contratto **engine-independent** che le capability
usavano:

- `Event` (DTO riga), `EnqueueResult` (feedback di enqueue);
- `Handler`, `HandlerRegistry`, `NewHandlerRegistry`;
- `Envelope`, `ViolationKind`/`Violation`, `DecodePolicy`, `DecodeV1`,
  `DefaultViolationError`, `EventLogFields`;
- alias di `TerminalError`/`NewTerminalError`/`IsTerminal` (owner:
  `internal/kernel/event`);
- alias delle costanti evento e degli schema version (owner:
  `internal/kernel/event`, nessun literal duplicato) e `JobCompletedEventKey`.

`internal/platform/sqlite/outboxevents` **delega**: alias di tipo, wrapper per
le funzioni, costanti invariate. L'adapter continua a possedere SQL, pool,
supersede e repository; non possiede più il contratto. Il package PostgreSQL e
i test storici compilano senza modifiche perché il surface pubblico è invariato.

Import di produzione migrati a `kernel/outbox` (nessun adapter nel grafo):

- `capabilities/jobs` (`outbox_registry.go`);
- `capabilities/jobs/finalize` (+ nuova porta `OutboxEnqueuer` in
  `outbox_port.go`; `*outboxevents.Repository` la soddisfa strutturalmente);
- `capabilities/jobs/handlers` (12 file), `capabilities/jobs/indexrestore`;
- `capabilities/publish_drive`, `capabilities/publish_outbox`;
- `capabilities/operations` (`OutboxEmitter` ora usa `outbox.EnqueueResult`);
- `capabilities/assets/texttracks` (materializer + seam).

Nota di comportamento: `finalize.New` accetta una **porta**, quindi un `nil`
untyped è un errore di wiring (panic al primo Enqueue). I test byte-equivalent /
e2e costruivano il finalizer con `nil` e sfruttavano il ramo `tx` del
repository; ora passano `outboxevents.NewRepository(sqlport.WrapDB(db))`, che
scrive esattamente nella stessa tx di prima.

### 5.2 Helper URL Drive puri — `internal/kernel/driveurl`

`FileIDFromLink`, `FileURLFromID`, `FolderURLFromID` sono tre string builder
senza I/O; sono passati nel kernel e `platform/drive` li riesporta.
`assets/assettree` li usa direttamente, `assets/assetop` per
`extractFileIDFromLink`.

### 5.3 Porte Drive della capability

- `assets/assetop`: nuova porta `DriveFileState`
  (`FileIsNotTrashed(ctx, fileID) (bool, error)`), il reconciler non dipende più
  da `drive.Reader`; `drive.Reader` la soddisfa strutturalmente.
- `assets/lifecycle`: `ServiceDeps.DriveReader` è `assetop.DriveFileState`,
  rimosso il campo `driveReader` mai letto; `platform/drive` fuori dal grafo.
- `assets/assettree`: solo `kernel/driveurl`.

### 5.4 Test con fake (acceptance 3)

| Porta | Fake test |
| --- | --- |
| `outbox.HandlerRegistry` / `DecodeV1` | `internal/kernel/outbox/contract_test.go` — handler fake, registrazione/dispatch, panic su idempotency key vuota, violazione terminale |
| `finalize.OutboxEnqueuer` | `internal/capabilities/jobs/finalize/outbox_port_test.go` — fake che registra gli eventi, fallback `{}`, propagazione errori, `var _ OutboxEnqueuer = (*outboxevents.Repository)(nil)` |
| `assetop.DriveFileState` | `internal/capabilities/assets/assetop/drive_port_test.go` — fake trashed/errore, `var _ DriveFileState = (drive.Reader)(nil)` |
| `kernel/driveurl` | `internal/kernel/driveurl/driveurl_test.go` + `assets/assettree/service_test.go` (link canonici senza adapter) |

## 6. Misura finale (2026-11-26, post-intervento)

```text
archi core capabilities→platform (no /adapters)   138 → 127   (−11)
archi totali capabilities→platform                157 → 146   (−11)
  platform/sqlite/outboxevents                      9 → 1     (residuo: assets/providers/stock/enrichment)
  platform/drive                                    6 → 3     (residuo: clips, jobs, voiceover/sync)
coppie bidirezionali fra famiglie                  18 → 17    (−1: assets ↔ drive)
```

Check eseguiti:

```text
go build ./...                                  → exit 0
go vet <package toccati>                        → exit 0
go test ./internal/capabilities/... -count=1    → exit 0 (tutti ok)
go test ./internal/platform/sqlite/outboxevents ./internal/platform/drive → ok
go run ./cmd/archcheck --strict                 → exit 0, 0 violazioni, 69/77 warning
make verify-cyclo                               → exit 0 (40/40)
```

## 7. Residuo dichiarato (fuori dal perimetro di questa chiusura)

- `capabilities/assets/providers/stock/enrichment/outbox_emitter.go` costruisce
  `outboxevents.NewRepository(db)` per evento: è composition-root work dentro
  la capability, va spostato in wiring con una porta `Enqueue` (stessa forma di
  `finalize.OutboxEnqueuer`).
- `capabilities/jobs/policy_service.go` (`AssetTransferServiceImpl`) usa
  `assetindex.Service`, `channels.VoiceoversRepository`,
  `imagesrepo.ImagesRepository` e `drive.FileLifecycle`: è un servizio
  SQLite-bound nel core; richiede l'estrazione in un sottopackage `adapters`
  (dove l'import è fisiologico) o tre porte dedicate.
- `capabilities/clips`, `capabilities/jobs`, `capabilities/voiceover/sync`
  importano ancora `platform/drive` (3 archi).
- Non toccati: schema V3, warning editoriali del corpus, P2-06, P2-08.

## 8. Note

Non è un refactor meccanico: ogni famiglia migrata ha invertito la dipendenza
(contratto nel kernel, oppure porta posseduta dal consumer) e ha un fake test.
Nessun budget alzato: `architecture/policy.yaml` dichiara i nuovi kernel
subzone `outbox` e `driveurl`; baseline cyclo e hot-spot invariati.
