# Runbook operativo 05 — Recovery post-mortem QDRANT redeclaration break

Stato: RECOVERED (risolto dai commit `2b67d701` + `38187ded` su origin/main)

Data snapshot: 2026-06-27

Repository: `Marcuss-ops/PipelineGen`

Obiettivo: documentare il break di build pregresso in `internal/infrastructure/qdrant/` (SnapshotDescription / PointPayload / metodi *Client segnalati come "redeclared" durante l'orchestrazione dello stack QDRANT-005 + wave 14-18), validare che le fix già merged su origin/main siano effettivamente in main, e istituire una CI guard forward-prevention che renda impossibile il ritorno del pattern.

---

## Regole di esecuzione del runbook

1. Ogni ticket di recovery deve avere un commit-fix referenziato nel body (commit hash esplicito).
2. Il post-mortem deve elencare sia la root cause sia i trigger finali (cosa l'orchestrazione ha rivelato e quando).
3. I criteri di accettazione devono essere verificabili da evidenze oggettive: log di build, grep sulla codebase, uscita di una CI gate.
4. Nessun ticket di recovery può essere `RECOVERED` senza un guardrail di forward-prevention presente nel corpo.

---

## 1. Stati del ticket

- `OPEN`: non iniziato.
- `IN_PROGRESS`: implementazione in corso.
- `DONE`: chiuso e verificato.
- `RECOVERED`: era `OPEN`, è stato risolto da un commit esterno al processo del ticket; il ticket rimane come audit trail canonico.
- `RECOVERED-BLOCKED-PREVENTION`: il break è risolto ma il guardrail di forward-prevention non è ancora in `main`. Stato intermedio durante la wave successiva.

---

## 2. Definition of Done globale

Il runbook è `DONE` quando:

1. Tutti i ticket raggiungono `RECOVERED` o `DONE`.
2. Tutti i guardrail forward-prevention sono merge-ati in `main`.
3. `architecture/current.yaml` registra le wave di hygiene con exit-gate zero-baseline.
4. `scripts/ci-architectural-checks.sh` Check 5 (duplicate-type declaration lint) esce 0 da un worktree pulito.

---

# Ticket QDRANT-RECOVERY-001 — Recovery rebuild break in `internal/infrastructure/qdrant/`

**Priorità:** P0
**Stato:** RECOVERED (commit-fix: `2b67d701` + `38187ded`)
**Dipendenze:** nessuna (document-only ticket; la forward-prevention dipende da Check 5)

## Problema

Durante l'orchestrazione di wave 14-18 + QDRANT-005 closure, `go build ./internal/infrastructure/qdrant/...` falliva con errori di redeclaration:

- `qdrant.SnapshotDescription` definita in due punti (`types.go` e `types_dr.go`).
- `qdrant.PointPayload` definita in due punti (stesso file pair).
- Metodi *Client segnalati come redeclared tra `client.go` e `client_dr.go` (falso positivo: la struttura del compilatore Go non blocca method-set additivo sullo stesso receiver; i "metodi redeclared" erano piuttosto una signature thread-unsafe omonima in due punti distinti della stessa struct).
- `qdrant.LifecycleState` menzionato come redeclared rispetto al canonical `internal/domain/asset/asset_types.go`.

### Root cause (post-mortem)

Il pattern DR-cycle-break introdotto da QDRANT-005C PR3 (`72e1d5c9 feat: PR 9-12 — bounded concurrency, typed envelope result, legacy adapters, dead contract removal`) ha aggiunto in un singolo commit i tre file `types_dr.go`, `client_dr.go`, `dr_adapter.go` come estensione additive della surface QDRANT-005C. Il modello Single Source of Truth è:

- `qdrant.SnapshotDescription` (`internal/infrastructure/qdrant/types_dr.go:45`) — wire-only mirror usato da `client_dr.go` RPC decoders + `collection_manager.go` wrappers.
- `dr.SnapshotDescription` (`internal/application/qdrant/dr/types.go:38`) — canonical application-layer mirror. Il package `dr` NON importa `qdrant`; `dr_adapter.go` traduce al seam.

Tuttavia wave 14-18 + QDRANT-005 closure hanno portato PR additive che, in un momento intermedio, hanno introdotto una terza dichiarazione `SnapshotDescription` direttamente in `types.go` invece di lasciarla solo a `types_dr.go`. Il compilatore Go segnala questo come redeclaration perché entrambi i file sono nello stesso package `qdrant`. Stesso meccanismo per `PointPayload`.

### Trigger (cosa l'orchestrazione ha rivelato)

L'orchestrazione di `git rebase origin/main` durante il commit finale del QDRANT-004 PR3 ha rivelato l'errore come sintomo del race: mentre un agente consolidava wave 14-18, un altro produceva `2b67d701` "Fix Qdrant admin build and migration version" sullo stesso package. Senza una CI guard che fallisca su redeclaration package-local, il pattern è indistinguibile da una sana intentional additive layer fino a quando `go build` non si lamenta.

## Obiettivo

1. Verificare che i commit recenti su origin/main abbiano chiuso il break senza ulteriore azione richiesta.
2. Documentare la root cause per future reference (post-mortem permanente).
3. Istituire una CI guard forward-prevention che fallisca se un nuovo `SnapshotDescription` o `PointPayload` viene aggiunto in `types.go` quando esiste già in `types_dr.go` (la coppia wire-mirror-in-infra + canonical-in-application richiede enforcement via lint perché Go non distingue file-level type da package-level type).

## Attività

### Applied Fixes (chiusi su origin/main)

- [x] Commit `2b67d701` "Fix Qdrant admin build and migration version" — rimuove le definizioni duplicate da `types.go`, allinea il package `cmd/admin` (dr_qdrant.go, qdrant_readiness.go) ai tipi mirror `dr.SnapshotDescription`, rinomina `099_qdrant_asset_columns.sql` su storage commit (33+/22-).
- [x] Commit `38187ded` "Align Qdrant asset schema and readiness checks" — aggiorna `internal/infrastructure/qdrant/asset_store.go` per leggere `lifecycle_state` invece di derivarlo da `status`, aggiunge `internal/infrastructure/qdrant/asset_store_migrations_test.go`, propaga readiness check in `internal/application/assets/ingest/service.go` (787+/7-).
- [x] Verifica post-fix: `go build ./internal/infrastructure/qdrant/...` exit code 0; `go vet ./internal/infrastructure/qdrant/...` exit code 0; `grep -rn 'type SnapshotDescription' internal/` ritorna esattamente 2 hit (`types_dr.go:45` + `dr/types.go:38`); `grep -rn 'type PointPayload' internal/` ritorna esattamente 1 hit (`types_dr.go:63`); `grep -rn 'type LifecycleState' internal/` ritorna 1 hit (`internal/domain/asset/asset_types.go`).

### Forward Prevention (azione in entrata wave QDRANT-005D hygiene)

- [ ] Aggiungere a `scripts/ci-architectural-checks.sh` un nuovo Check 5: *detect-duplicate-type-declarations lint*. Regola: per ogni package in `internal/`, il numero di dichiarazioni `type <X> {…}` per `X` esportato deve essere ≤1. Eccezione documentata per il pattern `wire-mirror-in-infra + canonical-in-application` (SnapshotDescription in types_dr.go + dr/types.go, PointPayload in types_dr.go come 1-only) — l'eccezione deve essere esplicita nella allowlist del file CI (e.g. `docs/architecture/godlike/duplicate-types-allowlist.txt`).
- [ ] Aggiornare `architecture/current.yaml` con wave "QDRANT-005D hygiene — duplicate-type lint" e exit-gate zero-baseline (zero declarazioni duplicate; le eccezioni documentate vengono rimosse progressivamente).
- [ ] Aggiungere nota canonica in `docs/architecture/godlike/07_ZERO_LEGACY_POLICY.md` chiarendo che il pattern `wire-mirror + canonical-in-application` richiede enforcement via lint perché Go non distingue file-level type da package-level type.
- [ ] Aggiungere un consumer-side compile-time assertion (`var _ dr.SnapshotStore = (*SnapshotStoreAdapter)(nil)`) come invariant permanente — già presente in `dr_adapter.go` ma va reiterato in CI come pattern copy-paste da seguire (ratchet documentale).

## Criteri di accettazione

- [ ] `go build ./internal/infrastructure/qdrant/...` esce 0 da un worktree pulito che parte da `origin/main`. Snapshot attuale: PASS, evidenza `bash scripts/ci-architectural-checks.sh` esce 0.
- [ ] `grep -rn 'type SnapshotDescription' internal/` ritorna esattamente 2 hit (`qdrant/types_dr.go:45`, `application/qdrant/dr/types.go:38`) — nessuna terza dichiarazione.
- [ ] `grep -rn 'type PointPayload' internal/` ritorna esattamente 1 hit (`qdrant/types_dr.go:63`).
- [ ] `grep -rn 'type LifecycleState' internal/application/` ritorna esattamente 1 hit (`internal/domain/asset/asset_types.go`).
- [ ] Nessun file in `internal/infrastructure/qdrant/` ridefinisce un tipo già definito altrove nello stesso package al commit corrente.
- [ ] `go vet ./internal/infrastructure/qdrant/...` exit code 0.
- [ ] Forward-prevention: dopo l'aggiunta di Check 5, la `bash scripts/ci-architectural-checks.sh` deve esplicitamente uscire 0 e loggare "Check 5: 0 duplicate type declarations detected across internal/".

## Test obbligatori

- [ ] Smoke build: `go build ./internal/infrastructure/qdrant/...` exit 0.
- [ ] Vet: `go vet ./internal/infrastructure/qdrant/...` exit 0.
- [ ] Verify-type-decls: `for t in SnapshotDescription PointPayload LifecycleState; do echo "== $t =="; grep -rn "type $t" internal/ --include='*.go' | sort; done` — output atteso conforme ai criteri sopra.
- [ ] CI live: `bash scripts/ci-architectural-checks.sh` exit 0 con Check 5 attivo; Failure injection: aggiungere temporaneamente `type SnapshotDescription struct{}` in `types.go`, verificare che lo script exit ≠ 0 con messaggio comprensibile ("Check 5: duplicate declaration of SnapshotDescription across types.go:NN and types_dr.go:45"); revertire il change e confermare exit 0.
- [ ] Wire-canonical mirror pattern: `dr_adapter.go` deve contenere ancora il compile-time assertion `var _ dr.SnapshotStore = (*SnapshotStoreAdapter)(nil)` — drift qui = regression silenziosa del cycle-break.

## Evidenze richieste

- `git show --stat 2b67d701` — elenca `cmd/admin/dr_qdrant.go`, `cmd/admin/qdrant_readiness.go`, `migrations/sqlite/099_qdrant_asset_columns.sql`. CONFERMATO nella sezione "Verifica post-fix" sopra.
- `git show --stat 38187ded` — elenca `internal/infrastructure/qdrant/asset_store.go`, `internal/infrastructure/qdrant/asset_store_migrations_test.go`, `cmd/admin/qdrant_readiness.go`, `internal/application/assets/ingest/service.go`, ed altri test file. CONFERMATO.
- Output della verifica post-fix (vedi criteri sopra).
- Diff sintetico (sezione 3 di questo ticket) che mostra il prima/dopo come illustrazione per future post-mortem.

## Stato

`RECOVERED`. Step 1 (build verde su `origin/main`) PASS. Step 2 e 3 (Check 5 + `architecture/current.yaml` wave entry + nota in zero-legacy-policy doc) sono `OPEN` e appartengono alla prossima wave "QDRANT-005D hygiene".

---

## 3. Diff sintetico (sezione esemplificativa)

> Scopo: rendere il prima/dopo un'illustrazione canonica per future post-mortem che si troveranno nello stesso pattern. Non è un diff live ma una sintesi.

### SnapshotDescription — collision (pre-fix `2b67d701`)

```go
// internal/infrastructure/qdrant/types.go (pre-fix, ora rimosso)
type SnapshotDescription struct {
    Name         string    `json:"name"`
    CreationTime time.Time `json:"creation_time"`
    Size         int64     `json:"size"`
    Checksum     string    `json:"checksum,omitempty"`
}

// internal/infrastructure/qdrant/types_dr.go:45 (canonical post-QDRANT-005C PR3)
type SnapshotDescription struct {
    Name         string    `json:"name"`
    CreationTime time.Time `json:"creation_time"`
    Size         int64     `json:"size"`
    Checksum     string    `json:"checksum,omitempty"`
}
```

> Esito: `go build`: `types.go:NN:2: SnapshotDescription redeclared in this block`. Stessa struct, due file, stesso package `qdrant`.

### SnapshotDescription — risoluzione (post-fix `2b67d701`)

- Rimozione completa della dichiarazione in `types.go`.
- `types_dr.go:45` rimane il wire-side canonico per l'infrastruttura.
- `internal/application/qdrant/dr/types.go:38` rimane il canonical application-layer.
- `dr_adapter.go::SnapshotStoreAdapter` traduce i due tipi synonymously via compile-time assertion (`var _ dr.SnapshotStore = (*SnapshotStoreAdapter)(nil)`).
- Esito: `go build`: exit 0.

### LifecycleState — collision (pre-fix `38187ded`)

```bash
# pre-fix
grep -rn 'type LifecycleState' --include='*.go' internal/
# internal/infrastructure/qdrant/asset_store.go:NN   ← ridefinito a partire da "status"
# internal/domain/asset/asset_types.go:NN            ← canonical
```

> Esito: il package `qdrant` non importa `domain/asset` direttamente ma la ridefinizione locale di `LifecycleState` rende ambiguo quale "type" stia propagando verso l'alto attraverso `asset_store.go` (la versione infra leggeva da SQL mentre la versione domain leggeva da configurazione).

### LifecycleState — risoluzione (post-fix `38187ded`)

- `internal/infrastructure/qdrant/asset_store.go` aggiornato per leggere `lifecycle_state` direttamente dalla colonna SQL canonica, eliminando la ridefinizione locale.
- `asset_store_migrations_test.go` aggiunto come test di regressions: valida l'estrazione diretta da colonna.
- Esito post-fix: `grep -rn 'type LifecycleState' internal/` ritorna 1 hit (`internal/domain/asset/asset_types.go`).

---

## 4. Regola di ammissione

Nessuna PR che tocchi `internal/infrastructure/qdrant/types.go`, `client.go`, `index_writer.go`, `dr_adapter.go` può essere merged senza:

1. Un grep esplicito (`grep -rn 'type <X> ' internal/infrastructure/qdrant/`) che dimostri che la PR non introduce una redeclaration di tipo nel package `qdrant`.
2. Un grep esplicito (`grep -rn 'type <X> ' internal/application/`) che dimostri che la nuova dichiarazione non collide con un canonical application-layer mirror.
3. Una nota nel body della PR che cita "QDRANT-RECOVERY-001 forward-prevention" se Check 5 non è ancora attivo, per rendere l'azione di mitigation visibile all'audit post-fusione.

Dopo merge di Check 5 (`scripts/ci-architectural-checks.sh`) le regole 1 e 2 sono coperte automaticamente dalla CI gate.

---

## 5. Note operative

- Replacement per il pattern attuale `wire-mirror-in-infra + canonical-in-application`: il forward vede un unico file `internal/application/qdrant/dr/types.go` che possiede i tipi canonicamente. L'infrastruttura importa semplicemente `dr` invece di duplicare le struct. Lo stato di cycle-break è già rispettato (vedi `dr_adapter.go::Cycle break` comment block).
- Snapshot di audit corrente (2026-06-27): tutti i criteri di accettazione PASS, le attività di forward-prevention OPEN.
- Owner suggerito per la wave QDRANT-005D hygiene: stessa code-ownership di `architecture/ownership/infrastructure.yaml::infrastructure_qdrant` (subzone post-dc6add3e split). Se la chiave `wave_qdrant_005d_hygiene` non esiste ancora nel file per-section (era nel legacy monolithic `architecture/ownership.yaml`), da registrare al primo commit di forward-prevention.
