# Plane Inventory — i tre piani migrazione di PipelineGen

Scopo: **una sola doc narrativa** dei piani migrazione e delle divergenze note tra
dialetti (SQLite operativi, Postgres media SSOT, Jobs plane), così che agente e
umani leggano lo stesso documento invece di dedurre la topologia da tre dirs con
numerazioni diverse.

La matrice dei **vocabolari di stato** (SSOT machine-consumed) resta in
[`STATE_MATRIX.md`](STATE_MATRIX.md). Questo file NON la replica: copre
topologia piani, runner, ledger e divergenze strutturali.

## Piani

| Piano | Dir | File | Runner / modalità applicazione | Ledger storico | Authority |
|---|---|---|---|---|---|
| `primary` / `observability` / `cache` | `migrations/sqlite/` | 241 .sql (versioni 000–289, gap inclusi) | `internal/platform/sqlite` — `RunMigrations` / `migrateAll` con header di scope `-- database:` | `schema_migrations` (version, migration_id, filename, checksum sha256, checksum_sha256, duration_ms, app_git_sha) | SQLite = authority per i domini operativi (AGENTS.md, cutover media 09/2026) |
| `jobs` | `migrations/sqlite_jobs/` | 6 .sql (001–006), tutti scoped `jobs` | jobs store self-owned (wiring in `internal/platform/config/jobs.go`) | `schema_migrations` dello stesso runner (targetDB=`jobs`) | SQLite jobs plane |
| `postgres` (media) | `migrations/postgres/` | 13 .sql (001–013) | deployment-managed step + `MediaSelfBootstrapDDL` ordine pinnato (`embed_ddl.go` + `embed_ddl_contract_test.go`); one-shot 010/011 NON idempotenti fuori dal self-bootstrap | **NESSUNO** — vedi divergenza D2 | PostgreSQL = SSOT del dominio media (_binary percheck gate `percheck_media_assets_writer_canonical`) |

Census scope-header su `migrations/sqlite` (rilevato 2026-10-28, informativo):

- file con direttiva: **94 su 241**; token dichiarati: `primary` 78,
  `observability` 8, `cache` 6, `all` esplicito 2. Il resto usa il default
  "assenza di direttiva → all".
- I token are case-insensitive e normalizzati dal parser runtime
  (`parseMigrationScope`, `migrations_discovery.go`).

## Regola di scope e fail-closed

Il parser runtime applica il fallback documentato `token sconosciuto → all`
("a typo can't quietly exclude a migration"). Questo dance è sicuro contro
l'esclusione accidentale ma renderebbe un TYPO (`-- database: primario`) un
apply ad ogni DB senza segnale. Il gate di scoperta è:

- **`internal/platform/sqlite/migrations_scope_headers_test.go`** — ogni
  direttiva nei tre piani deve essere lessicalmente valida (token → errore di
  build, non apply silenzioso), il parser runtime deve concordare con l'header,
  il piano jobs non può dichiarare token estranei a `jobs`, il piano postgres
  non può avere direttive di scope (non ha un targetDB selector: header morto).

## Divergenze note e criteri di accettazione

### D1 — Scope vocab chiuso (DONE)
Il vocabolario dei token di scope è (`primary|observability|cache|jobs|all`),
gated dal test sopra. Qualsiasi nuovo token richiede: parser runtime +
doc inventory (questo file) + audit test nella stessa modifica.

### D2 — Ledger/audit parity sul piano Postgres media (OPEN)
Le migrazioni del piano `postgres` non hanno un ledger (`schema_migrations`):
fanno fede l'ordine pinnato del self-bootstrap embed_ddl e la commessa di
deployment one-shot 010/011. Conseguenza: checksum, durata e provenienza
(`app_git_sha`) esistono solo per i piani SQLite.
**Criterio di accettazione**: il piano o dichiara l'assenza di ledger come
contratto (riga in questa tabella + sezione in `AGENTS.md`) o introduce una
pagella ledger PG; in entrambi i casi il triage di "quale migration ha
prodotto questa colonnaPG" deve poter PASSARE da un artefatto tracciato, non
da archeologia manuale su git log. Retrofit silenzioso vietato: è un cambio di
deployment contract (expand → backfill → cutover).

### D3 — Numerazioni per-piano (BY DESIGN, da citare sempre)
Tre serie numeriche indipendenti: versione 000–289 in `migrations/sqlite`,
001–006 in `sqlite_jobs`, 001–013 in `postgres`. Non è errore né collisione:
sono tre storie. **Criterio di accettazione**: ogni report/script/tool che
traversa i piani deve portare il nome del piano accanto al numero di versione
("sqlite 131", "sqlite_jobs 003", "postgres 009"); il diff-only del numero è
un bug di reporting.

### D4 — Splitter SQL duplicato cross-repo (OPEN, fuori repo)
PipelineGen (`migrations_splitter.go`, hand-tested, BEGIN/END depth tracking
con CASE depth e riconoscimento BEGIN TRANSACTION/IMMEDIATE/EXCLUSIVE/DEFERRED)
e RenderingGen queue (`internal/migrate`, con propri fuzz
`split_statements_fuzz_test.go`) implementano due volte lo stesso problema.
**Criterio di accettazione**: pacchetto splitter condiviso (o contratto di
equivalenza esplicito testato in entrambe le repo), con suite fuzz che copre
tutti i call site; nessuna delle due implementazioni hand-rolled può restare
l'unica difesa contro il rigetto di trigger body.

### D5 —Mirror artifactdb in RenderingGen è schema-by-struct (OPEN, fuori repo)
`RenderingGen/renderinggen/internal/artifactdb` deriva il nome colonna con
`snakeCase(fields)` dai struct Go; il pin è un parity test sui nomi, non sulla
schema. Se uno struct cambia senza regenerare il mirror, la divergenza è
silenziosa. **Criterio di accettazione**: mirror generato dalle migrazioni
(oppure della schema attesa, pinato), con gate che fallisce su drift struct→
schema; "patch till match" vietato.

### D6 — Drift vocabolario visibile per design (riferimento)
Il drift su `media_assets.lifecycle_state` (SQLite default `'ready'` lowercase,
Postgres `'ACTIVE'` UPPER) è dichiarato in `STATE_MATRIX.md` ("la divergenza è
visibile, non nascosta") ed è gated dai test live. Non è un mondo da chiudere
qui: la sua repair è un cambio semantico di dominio, non una pulizia doc.
