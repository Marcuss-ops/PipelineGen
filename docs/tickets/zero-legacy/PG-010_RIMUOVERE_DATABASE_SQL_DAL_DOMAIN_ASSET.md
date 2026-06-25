# PG-010 — Rimuovere database/sql dal domain asset

**Priorità:** P0  
**Branch unica:** `codex/pg-010-sql-domain-asset`  
**Commit suggerito:** `refactor(boundary): remove database sql residue for pg-010`  
**Dipendenze:** PG-001

## Missione

Eliminare `database/sql` dai layer non infrastrutturali per il gruppo assegnato.

## Decisione già presa

Ogni query diventa un metodo di una porta/repository tipizzata. SQL e scan rimangono nell'adapter SQLite. La baseline viene ridotta nello stesso commit.

L'agente non deve ridefinire l'architettura. Deve applicare questa decisione e fermarsi se una stop condition la rende impossibile.

## Risultato atteso

Zero import SQL nei file di produzione target; test aggiornati senza esporre raw DB quando non strettamente infrastrutturali.

## Scope consentito

- `internal/domain/asset/**`
- `internal/infrastructure/database/sqlite/assets/**`
- `internal/application/assets/**`
- `internal/app/**`
- `scripts/archcheck/main.go`

## File e aree vietate

- Qualsiasi file non elencato nello scope, salvo test direttamente associati.

## Non-obiettivi

- Nessuna nuova feature.
- Nessun cambiamento pubblico fuori scope.
- Nessuna compatibility layer.

## Checklist A–Z

### A. Sincronizzare `main`

`git fetch origin && git checkout main && git pull --ff-only origin main`; confermare working tree pulita con `git status -sb`.

### B. Creare la sola branch del ticket

`git checkout -b codex/pg-010-sql-domain-asset`. Non creare altre branch.

### C. Catturare la baseline verificabile

- `rg -ln '"database/sql"' internal/app internal/application/assets internal/domain/asset internal/infrastructure/database/sqlite/assets || true`
- `grep -n "databaseSQLLegacyBaseline" -A80 scripts/archcheck/main.go`

### D. Leggere i contratti prima di modificare

Aprire tutti i file elencati in **Scope consentito** e cercare implementazioni equivalenti. Se esiste già il contratto canonico, riusarlo; non crearne un duplicato.

### E. Inventariare query e transazioni

Elencare query, input, output, errori `sql.ErrNoRows` e confini transazionali.

### F. Mappare repository canonici

Usare `job.Store`, `asset.Repository` o porte capability già esistenti.

### G. Aggiungere metodi orientati al caso d'uso

Niente `DB()`, `Query()`, `Exec()` o callback SQL nei port.

### H. Implementare adapter SQLite

Spostare query, scan e transaction handling sotto infrastructure/database.

### I. Aggiornare application service

Iniettare la porta tipizzata e preservare semantica errori/idempotenza.

### J. Aggiornare composition root

Costruire l'adapter una volta e registrarlo nel bundle corretto.

### K. Aggiornare test

I test application usano fake/in-memory port; i test SQL vivono nel package infrastructure.

### L. Rimuovere raw DB getter

Eliminare getter e wrapper che espongono `*sql.DB` al layer superiore.

### M. Ridurre baseline

Cancellare dal `databaseSQLLegacyBaseline` ogni path realmente pulito.

### N. Aggiungere assertion compile-time

Verificare che l'adapter concreto implementi la porta.

### O. Verificare transazioni e concorrenza

Conservare atomicità, locking e gestione `ErrNoRows`; aggiungere test mirati.

### P. Formattare

Eseguire `gofmt -w` su tutti e soli i file Go modificati. Per YAML/JSON/Markdown mantenere il formato esistente.

### Q. Eseguire i test mirati

- `go test ./internal/application/...`
- `go test ./internal/infrastructure/database/...`

### R. Eseguire i test di regressione più ampi

- `go test ./...`

### S. Eseguire vet e build

- `go vet ./internal/...`
- `go build ./...`

### T. Eseguire i gate architetturali

- `go run ./scripts/archcheck --ratchet`

### U. Ripetere le ricerche di baseline

Tutti i comandi del punto C devono mostrare la riduzione prevista o zero match, secondo i criteri di accettazione.

### V. Controllare il diff

- `git diff --check`
- `git diff origin/main...HEAD`
- Confermare che non esistano modifiche fuori scope.

### W. Aggiornare solo la documentazione canonica necessaria

Aggiornare ADR, architecture tracker o commenti solo quando descrivono il comportamento corrente. Non aggiungere cronache della PR nei commenti di produzione.

### X. Riallineare la branch

`git fetch origin && git rebase origin/main`; rieseguire almeno test mirati, build e gate dopo il rebase.

### Y. Commit e push

Creare un commit piccolo e descrittivo con il messaggio indicato nel ticket; poi `git push -u origin HEAD`.

### Z. Verifica post-push

- `git log -n 5 --oneline`
- `git status -sb`
- Verificare la PR e i check remoti. Non chiedere merge finché i criteri di accettazione non sono tutti soddisfatti.

## Criteri di accettazione

- [ ] Zero `database/sql` nei file di produzione target.
- [ ] Nessun port espone `*sql.DB`.
- [ ] Baseline ridotta esattamente dei path puliti.
- [ ] Query presenti solo in infrastructure/database.
- [ ] Test funzionali invariati.

## Evidenze da allegare alla PR

- Output dei comandi di baseline iniziali e finali.
- Elenco esatto dei file modificati.
- Output dei test mirati.
- Output di `go build ./...`.
- Output del gate architetturale.
- `git log -n 5 --oneline` dopo il push.
- Nota esplicita: “nessuna branch secondaria creata”.

## Rollback

Il rollback deve consistere nel revert del singolo commit/PR. Non introdurre flag temporanei, alias o doppio percorso per rendere reversibile la modifica.
