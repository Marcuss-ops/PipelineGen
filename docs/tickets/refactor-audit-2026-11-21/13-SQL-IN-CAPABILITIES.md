# RA-13 — SQL e DDL dentro le capability (violazione dell'I/O binder)

**Data:** 2026-11-21
**Priorità:** P1 (strutturale, schema accoppiato al dominio)
**Stato:** **DONE (2026-11-21)** — implementazioni SQL/DDL spostate in `platform`, migrazioni canoniche aggiunte, percheck `percheck_capability_sql_ban` attivo
**Owner:** capability owners + platform owners (postgres/sqlite)
**Origine:** report esterno 2026-11-21 §1.2; verificato sul codice
**Verifica sul codice:** **CONFERMATO** — siti elencati sotto, righe misurate

## 1. Problema

Le capability eseguono SQL e, in due casi, **DDL** direttamente contro il database:

| File | Righe | Cosa fa |
| --- | --- | --- |
| `internal/capabilities/cliprender/render_cache.go` | 142, 230 | `CREATE TABLE IF NOT EXISTS clip_render_cache`, `INSERT … ON CONFLICT` (con `JOIN media_assets` a monte) |
| `internal/capabilities/localization/adapters/render_reuse.go` | 62, 150 | `CREATE TABLE`, `ALTER TABLE`, `SELECT`, `INSERT`, `UPDATE` su `localized_render_reuse` |
| `internal/capabilities/assets/artifacts/repository.go` | 31, 175, 195 | `INSERT INTO artifacts / artifact_sources / job_artifacts` (SQLite hardcoded) |

Conseguenze:
- lo schema è posseduto dalla capability, quindi una migrazione tocca il dominio;
- i due DDL "if not exists" creano tabelle a runtime, fuori dal sistema di migrazioni
  (`migrations/`): lo schema reale dipende da quale codice è stato eseguito;
- il package `capabilities` importa `database/sql` e i driver, quindi non è testabile senza DB.

Il repo ha già il precedente giusto: `percheck_sqlite_media_reader_ban` classifica le letture
su `media_assets` e la sua documentazione parla di "debt register vuoto". Questo ticket usa lo
stesso meccanismo per le capability.

## 2. Soluzione

1. **Definire una porta per ciascun repository** nella capability (interfaccia con metodi di
   dominio, nessun `*sql.DB`, nessuna stringa SQL):
   - `cliprender.RenderCacheStore { Get(ctx, key) (Entry, bool, error); Put(ctx, key, Entry) error }`
   - `localization.RenderReuseStore { Lookup/Bind/Update… }`
   - `artifacts.Repository` (se non esiste già un'interfaccia nel package `ports`)
2. **Implementare in `platform`**: `internal/platform/postgres/cliprender` e
   `…/localization`, `internal/platform/sqlite/artifacts`. Il codice SQL si **sposta**, non si
   riscrive: stesso statement, stesso ordine di scan, così il comportamento non cambia.
3. **DDL in `migrations/`**:
   - creare le migrazioni per `clip_render_cache` e `localized_render_reuse` con lo schema
     corrente (leggere il DDL dal codice e copiarlo verbatim, inclusi indici e `ON CONFLICT`
     target);
   - rimuovere `CREATE TABLE`/`ALTER TABLE` dal runtime;
   - se il deployment ha bisogno di bootstrap idempotente, il proprietario è
     `platform/postgres/<x>` con una funzione `EnsureSchema` **chiamata dal composition root**,
     non dalla capability.
4. **Wiring**: costruire le implementazioni in `app/wiring` e iniettare l'interfaccia.
5. **Percheck**: `percheck_capability_sql_ban` — `internal/capabilities/**` non può importare
   `database/sql` né i driver (`lib/pq`, `modernc.org/sqlite`, `mattn/go-sqlite3`). Allowlist
   iniziale = i file attuali, con owner e trigger; ratchet verso il basso. Attenzione: alcune
   capability potrebbero legittimamente usare `database/sql` per **tipi** (`sql.NullString`) —
   in quel caso l'allowlist è per file e il trigger è "sostituire con tipo di dominio".
6. **Test**: i test delle capability usano fake dell'interfaccia (nessun DB). I test
   dell'implementazione usano il harness già presente (`docker-compose.test-postgres.yml`,
   `sqlite/sqltest`).

## 3. Check finali

```bash
cd refactored
# 1. nessun SQL/DDL nelle capability
grep -rn "CREATE TABLE\|ALTER TABLE\|INSERT INTO\|SELECT .* FROM" --include=*.go \
  internal/capabilities/ | grep -v _test
# atteso: nessun output oltre l'allowlist dichiarata (idealmente zero)

# 2. nessun import di database/sql nelle capability (salvo allowlist)
grep -rln '"database/sql"' --include=*.go internal/capabilities/ | grep -v _test
# atteso: nessun output

# 3. le migrazioni esistono e sono applicabili
ls migrations/ | tail -20
# atteso: due nuove migrazioni; applicarle sul DB di test e verificare le tabelle

# 4. il percheck è attivo
go run ./cmd/archcheck --strict | grep -i 'capability_sql_ban'
# atteso: nessuna violazione

# 5. comportamento invariato
go test ./internal/platform/postgres/... ./internal/platform/sqlite/... ./internal/capabilities/cliprender/... \
  ./internal/capabilities/localization/... ./internal/capabilities/assets/artifacts/...
# atteso: PASS (o SKIP dichiarato se il DB non è disponibile)

# 6. build/vet/gate
go build ./... && go vet ./...
go run ./cmd/archcheck --strict   # exit 0
```

## 4. Rischi e rollback

- **Rischio**: la migrazione DDL diverge dal `CREATE TABLE IF NOT EXISTS` storico (indici,
  collation, default). Mitigazione: copiare il DDL verbatim e confrontare lo schema con
  `\d+` sul DB di test prima/dopo.
- **Rischio**: un deployment ha già la tabella creata a runtime e la migrazione fallisce.
  Mitigazione: migrazione idempotente (`IF NOT EXISTS`), e verifica su un DB esistente.
- **Rollback**: le tabelle restano; si ripristina il codice precedente. Nessuna perdita dati.

## 5. Chiusura (2026-11-21)

**DONE.** Implementazioni spostate, contratti restati nelle capability, schema nel ledger.

- **`cliprender`** — il contratto canonico `RenderCache`, `RenderCacheRecord`,
  `RenderBackend` ed `ErrCacheMiss` vive in `internal/kernel/media/render_cache.go`
  (spostato lì da RA-11 per non creare la coppia `capabilities/cliprender ↔
  platform/postgres`); la capability mantiene alias, quindi l'API pubblica non cambia.
  `PostgresRenderCache`, il DDL e `EnsureRenderCacheTable` vivono in
  `internal/platform/postgres/cliprender`; il test live è stato spostato lì.
- **`localization`** — `LocalizedRenderReuse` (lookup/store/Drive identity) vive in
  `internal/platform/postgres/localizationreuse`; `capabilities/localization/adapters/render_reuse.go`
  è stato eliminato e il wiring inietta il nuovo adapter.
- **`artifacts`** — `SQLiteRepository` vive in `internal/platform/sqlite/artifacts`; la
  capability conserva il contratto `Repository` e i tipi di dominio.
- **Migrazioni** — `migrations/postgres/015_clip_render_cache.sql` e
  `016_localized_render_reuse.sql`, registrate in `MediaSelfBootstrapMigrations` e pinnate
  da `embed_ddl_contract_test.go`. Il DDL dei due adapter è identico alle migrazioni.
- **Percheck** — `percheck_capability_sql_ban` vieta DDL e import di `database/sql`/driver
  nelle capability; `percheck_capability_sql_inventory_stale` impedisce che l'inventario
  resti più largo del debito reale. L'inventario registra **21 file** residui con owner e
  removal trigger (erano 24 prima di questo ticket).

Check eseguiti:

| Check | Esito |
| --- | --- |
| `go build ./...` | exit 0 |
| `go test ./cmd/archcheck/scan/boundaries ./migrations/postgres ./internal/platform/postgres/cliprender ./internal/capabilities/cliprender ./internal/capabilities/localization/... ./internal/capabilities/assets/artifacts ./internal/app/wiring` | PASS |
| `go run ./cmd/archcheck --strict` | exit 0, `passed=true`, 0 violazioni, 67 warning |
| `grep -rn 'CREATE TABLE\|ALTER TABLE' internal/capabilities` (non-test) | nessun output |
| `grep -rl '"database/sql"' internal/capabilities` (non-test) | 21 file, tutti in `capabilitySQLInventory` |
| `TEST_POSTGRES_DSN=… go test ./internal/platform/postgres/media -run TestRunMediaMigrations_LedgerIsChecksumPinned` | **PASS** — prima esecuzione 13 migrazioni (014 + 12 domain, incluse 015/016), ledger `schema_migrations` con 13 righe; seconda esecuzione 0 applicate / 13 skip; checksum tamper → errore fail-closed |
