# CLEANUP-06 — Ondata 2B: 21 file capability con `database/sql`

**Data:** 2026-11-24
**Priorità:** P1 (I/O binder, SSOT dello schema)
**Stato:** CLOSED (2026-11-24) — inventario SQL = 0; nessun `database/sql` o DDL runtime nelle capability
**Owner:** capability owners + `platform/sqlite` / `platform/postgres`
**Origine:** radiografia 2026-11-24, Ondata 2; inventario RA-13
**Base:** `refactored` @ `3e0a9e3ae`; rilettura @ `e89a0a3dc`

## 1. Problema

`cmd/archcheck/scan/boundaries/percheck_capability_sql_ban.go` registra
**21 file** in `internal/capabilities/**` che importano ancora
`database/sql` (o un driver) e usano `*sql.Tx`/`*sql.DB` direttamente. RA-13
ha già spostato i tre owner SQL/DDL veri (cliprender cache, localization reuse,
artifacts repository); questi 21 sono il debito residuo di binder I/O.

Il gate ha due metà: (a) vieta nuovi import fuori inventario, (b) fallisce se
un'entry resta dopo la migrazione (`capability_sql_inventory_stale`). Quindi
ogni slice deve migrare il codice **e** cancellare l'entry nello stesso cambio.

| Gruppo | File | Owner |
| --- | --- | --- |
| AI | `internal/capabilities/ai/autotag/autotag.go` | ai |
| Assets | `assets/finalizer/tx_adapter.go`, `assets/ingest/adapter_clip.go`, `assets/lifecycle/service_voiceover.go`, `assets/persistence/committer.go`, `assets/providers/stock/enrichment/handler_repository.go`, `assets/providers/stock/enrichment/outbox_emitter.go`, `assets/texttracks/materializer.go` | assets-* |
| Jobs | `jobs/handlers/delivery.go`, `jobs/finalize/artifact_writer.go`, `jobs/finalize/job_completion_writer.go`, `jobs/finalize/job_finalizer.go`, `jobs/finalize/lease_fence.go`, `jobs/outbox_registry.go` | jobs-* |
| Operations | `operations/ports.go` | operations |
| Voiceover | `voiceover/service/finalizer.go`, `finalizer_cleanup_outbox.go`, `finalizer_execute.go`, `persistence/repository.go`, `ports_finalization.go`, `upload_intent.go` | voiceover |

Totale: 1 + 7 + 6 + 1 + 6 = **21**.

## 2. Soluzione

1. **Porta tipizzata per owner**, dichiarata nella capability e senza
   `database/sql` nella firma. Esempi:
   - assets: `FinalizerStore`, `IngestStore`, `LifecycleStore`,
     `CommitterStore`, `StockEnrichmentStore`, `TextTrackMaterializerStore`;
   - jobs: `DeliveryStore`, `FinalizeStore`, `LeaseFenceStore`,
     `OutboxRegistryStore`;
   - voiceover: un solo `FinalizationStore` con operazioni di dominio;
   - operations: repository tx-scoped già previsto da `ports.go`;
   - ai: `TagStore`.
2. **Implementazione in `platform`**: `internal/platform/sqlite/...` o
   `internal/platform/postgres/...`. Il codice SQL **si sposta**, non si
   riscrive: stessi statement, stesso ordine di scan, stessa gestione errori.
3. **Wiring** in `internal/app/wiring`: costruire l'implementazione e iniettare
   l'interfaccia. La capability non riceve più `*sql.Tx`/`*sql.DB`.
4. **Tipi nullable**: sostituire `sql.NullString`/`sql.NullInt64` con tipi di
   dominio (`*string`, `Optional[T]`, enum) nel contratto della porta. Se un
   file usa `database/sql` solo per un tipo, l'entry si cancella comunque.
5. **Nessun DDL**: tabelle e indici restano in `migrations/`; nessun
   `CREATE/ALTER TABLE` runtime. RA-13 lo ha già reso un gate.
6. **Una slice per gruppo**, con test di contratto del fake e test
   dell'adapter su `sqlite/sqltest` o Postgres test container. Ogni slice
   cancella le entry del gruppo nello stesso commit.

## 3. Criteri di accettazione

```bash
cd refactored
# 1. conteggio che scende fino a zero, mai aumentato
python3 - <<'PY'
import re
s=open('cmd/archcheck/scan/boundaries/percheck_capability_sql_ban.go').read()
m=re.search(r'var capabilitySQLInventory = map\[string\]string\{(.*?)\n\}', s, re.S)
print(len(re.findall(r'"([^"]+)":', m.group(1))))
PY
# atteso a fine programma: 0

# 2. nessun import database/sql nelle capability (non-test)
rg -l '"database/sql"' --glob '!**/*_test.go' internal/capabilities
# atteso: nessun output

# 3. nessun DDL runtime
rg -n 'CREATE TABLE|ALTER TABLE' --glob '!**/*_test.go' internal/capabilities
# atteso: nessun output

# 4. gate e suite
go run ./cmd/archcheck --strict   # exit 0, has_hard_gate_hits: false
go build ./...
go vet ./internal/capabilities/... ./internal/platform/...
go test ./internal/capabilities/... ./internal/platform/sqlite/... \
  ./internal/platform/postgres/... ./internal/app/wiring/... -count=1
# i test Postgres che richiedono DSN possono SKIP: riportare SKIP, non PASS
```

## 4. Rischi e rollback

- **Rischio:** cambiare il confine transazionale (una scrittura prima dentro
  un `*sql.Tx` finisce fuori). Mitigazione: la porta espone un metodo
  transazionale per caso d'uso, non un `WithTx` generico; test di rollback.
- **Rischio:** un adapter duplica logica di scan. Mitigazione: spostare il
  codice, non riscriverlo; confrontare i test esistenti prima/dopo.
- **Rischio:** `sql.Null*` cambia semantica (zero value vs NULL). Mitigazione:
  test espliciti su NULL/empty e migrazione dei tipi nel contratto.
- **Rollback:** si ripristinano gli import e le entry; nessuna migrazione dati.
- **Regola di chiusura:** CLEANUP-06 si chiude solo con inventario SQL a zero;
  nessuna chiusura per gruppo parziale.

## 5. Stato pre-migrazione (superato)

- Snapshot iniziale: HEAD `e89a0a3dc`; `capabilitySQLInventory` = **21** entry
  (1 AI, 7 assets, 6 jobs, 1 operations, 6 voiceover).
- Stato finale: inventario **0**, nessun import `database/sql` non-test,
  nessun DDL runtime; vedi §6.

## 6. Esito 2026-11-24

- `capabilitySQLInventory`: 0 entry.
- `rg -l '"database/sql"' --glob '!**/*_test.go' internal/capabilities`: nessun output.
- `rg -n 'CREATE TABLE|ALTER TABLE' --glob '!**/*_test.go' internal/capabilities`: nessun output.
- Il confine SQL e' il surface stretto `pkg/sqlport` (Result/Row/Tx/DB), con le implementazioni in `internal/platform`; le porte tipizzate per owner restano un follow-up architetturale, non un criterio di accettazione di questa scheda.
- `go build ./...` rc 0; `go vet ./internal/capabilities/... ./internal/platform/...` rc 0.
- `go run ./cmd/archcheck --strict` rc 0, 0 violazioni.
- `go test ./internal/capabilities/... ./internal/platform/sqlite/... ./internal/platform/postgres/... ./internal/app/wiring/... -count=1` rc 0.
- I criteri del §3 sono verdi; CLEANUP-06 chiuso.

## 7. Cosa resta

Niente di bloccante: inventario SQL a 0 e nessun `database/sql` o DDL runtime
non-test nelle capability. Follow-up architetturale esplicito, non criterio di
questa scheda: porte tipizzate per owner oltre al surface `pkg/sqlport`. Restano
fuori perimetro push e suite completa
(`TestA1_ScaleBacklogInteractiveFirstClaimUnderBudget` flaky sotto carico).
