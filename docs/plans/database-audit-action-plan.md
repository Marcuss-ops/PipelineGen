# Database Audit Action Plan — Luglio 2026

Analisi completa del layer database di PipelineGen (89 migration, 149 file infra, 13 package repository SQLite) condotta il 2026-07-06.

**Risultato complessivo**: il layer è solido (transazioni corrette, Pattern 0 applicato, outbox atomico, WAL+busy_timeout, CAS optimistic locking). 6 criticità individuate, 0 blocchi architetturali.

---

## Azione 1 — 🔥 Configurare il connection pool in produzione

**File**: `internal/infrastructure/database/sqlite.go` — `NewSQLiteDB()` e `OpenSQLiteDB()`

**Problema**: In produzione non vengono mai chiamati `SetMaxOpenConns` / `SetMaxIdleConns`. SQLite con pool illimitato può saturare il write token WAL e causare "database is locked" sotto carico concorrente.

**Fix**: Aggiungere dopo `db.Ping()` in entrambi i costruttori:
```go
db.SetMaxOpenConns(1)
db.SetMaxIdleConns(1)
```

**Dipendenza tra azioni**: Nessuna — auto-sufficiente.

---

## Azione 2 — 🔶 Fix `fmt.Sprintf` su colonne dinamiche in `transition.go`

**File**: `internal/infrastructure/database/sqlite/jobs/transition.go`

**Problema**: `query := fmt.Sprintf("UPDATE jobs SET %s %s", setClause, whereClause)` — le colonne arrivano da `req.Updates map[string]any` senza validazione. Non c'è SQL injection (i valori sono parametrizzati) ma è fragile.

**Fix**: Aggiungere una allowlist di colonne accettabili (`id`, `type`, `status`, `priority`, `project`, `video_name`, `active_key`, `correlation_id`, `payload_json`, `result_json`, `progress`, `error`, `retry_count`, `max_retries`, `worker_id`, `lease_id`, `lease_expiry`, `created_at`, `updated_at`, `started_at`, `completed_at`, `cancelled_at`, `revision`) e validare `req.Updates` + `req.ExtraSets` contro la allowlist prima di costruire la query.

**Dipendenza tra azioni**: Nessuna — auto-sufficiente.

---

## Azione 3 — 🔶 Fix `fmt.Sprintf` su colonne dinamiche in `search_queries.go`

**File**: `internal/infrastructure/database/sqlite/assets/search_queries.go`

**Problema**: `dataQuery := fmt.Sprintf("SELECT %s FROM media_assets WHERE %s ORDER BY %s %s ...", MediaAssetColumns, whereClause, sortField, sortDir)` — `sortField` e `sortDir` vengono da input utente (`req.SortBy`, `req.SortAsc`) e interpolati direttamente.

**Fix**: Validare `sortField` contro una allowlist esplicita (`created_at`, `duration_ms`, `name`, `source`) e `sortDir` contro `ASC`/`DESC` prima dell'interpolazione.

**Dipendenza tra azioni**: Nessuna — auto-sufficiente.

---

## Azione 4 — 🔶 Migrare `seed_fixture/main.go` a `storage.OpenSQLiteDB`

**File**: `scripts/seed_fixture/main.go`

**Problema**: Il seed fixture apre il DB con `sql.Open("sqlite3", dbPath)` **senza** `_journal_mode=WAL&_busy_timeout=5000`. Usa il journal mode DELETE (default), che è single-writer bloccante.

**Fix**: Sostituire le due chiamate `sql.Open` con `storage.OpenSQLiteDB` (importando `internal/infrastructure/database`).

**Dipendenza tra azioni**: Nessuna — auto-sufficiente.

---

## Azione 5 — 🔹 Deprecare `SQLiteStore.DB()` pubblico

**File**: `internal/infrastructure/database/sqlite/jobs/repository.go`

**Problema**: `func (r *SQLiteStore) DB() *sql.DB` è pubblico e permette a qualsiasi caller di bypassare il Pattern 0, accedendo direttamente al driver SQLite.

**Fix**: Aggiungere deprecation comment e rimuovere chiamate non-test. Verificare con `rg '\.DB\(\)' internal/` che non ci siano caller production oltre a quelli di test/admin.

**Dipendenza tra azioni**: Nessuna — auto-sufficiente.

---

## Azione 6 — 🔹 Validare `Updates map[string]any` in `TransitionRequest`

**File**: `internal/infrastructure/database/sqlite/jobs/transition.go`

**Problema**: `Updates map[string]any` — le colonne non sono tipizzate. Un typo nel nome colonna esplode solo a runtime in SQLite.

**Fix (due opzioni)**:
- **Opzione A (minimale)**: Aggiungere validazione delle chiavi contro la stessa allowlist dell'Azione 2.
- **Opzione B (completa)**: Introdurre un tipo struct `TransitionUpdates` con campi tipizzati e un metodo `ToSetClauses() ([]string, []any)`.

**Dipendenza tra azioni**: Condivide la allowlist con Azione 2 — eseguire Azione 2 prima.

---

## Riepilogo dipendenze

```
Azione 1 (pool)        → indipendente, eseguibile SUBITO
Azione 2 (transition.go allowlist) → indipendente
Azione 6 (Updates validation)     → DOPO Azione 2 (stessa allowlist)
Azione 3 (search_queries.go)      → indipendente
Azione 4 (seed_fixture)           → indipendente
Azione 5 (deprecare DB())         → indipendente
```

## Regole di esecuzione

- **NO branches** — commit diretto su `main` (AGENTS.md Git-Lesson-2)
- **NO `--force`** — push diretto con `git push origin main`
- Ogni azione = 1 commit auto-sufficiente
- Trailer: `Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>`
- Prima di ogni push: `git fetch origin && git rebase origin/main`
