# PG-013 — Eliminare definitivamente allowlist API e baseline SQL

**Priorità:** P0  
**Branch unica:** `codex/pg-013-remove-legacy-baselines`  
**Commit suggerito:** `chore(archcheck): remove api and sql legacy baselines`  
**Dipendenze:** PG-002–PG-012

## Missione

Portare a zero le eccezioni architetturali dopo la chiusura dei ticket API e SQL.

## Decisione già presa

Il gate finale non sottrae percorsi autorizzati. Quando i prerequisiti sono verdi, i file di allowlist e le baseline incorporate vengono rimossi insieme ai loader e ai test dedicati.

## Scope consentito

- `scripts/archcheck/**`
- `scripts/ci-architectural-checks.sh`
- `docs/migrations/api-infrastructure-imports-allowlist.txt`
- `scripts/archcheck/grandfathered_allowlist.json`
- `ARCHITECTURE.md`

## Checklist A–Z

### A. Sincronizzare `main`
`git fetch origin && git checkout main && git pull --ff-only origin main`.

### B. Creare la sola branch
`git checkout -b codex/pg-013-remove-legacy-baselines`.

### C. Catturare la baseline
Eseguire `go run ./scripts/archcheck --strict || true` e cercare loader, baseline e allowlist.

### D. Verificare i prerequisiti
Confermare zero import API→infrastructure e zero `database/sql` nei layer vietati.

### E. Controllare le dipendenze
PG-002–PG-012 devono risultare chiusi e integrati in `main`.

### F. Rimuovere l’allowlist API
Rimuovere file, loader, fixture e riferimenti documentali collegati.

### G. Rimuovere la baseline SQL
Rimuovere la collezione di percorsi e la logica che sottrae violazioni note.

### H. Rimuovere il JSON transitorio
Rimuovere `grandfathered_allowlist.json` se non ha più consumer.

### I. Semplificare strict
`--strict` deve valutare tutte le violazioni reali senza eccezioni.

### J. Semplificare ratchet
Con baseline zero, delegare a strict o preparare la rimozione in PG-046.

### K. Aggiornare i test
Mantenere test per zero violazioni, regressione e codice di uscita.

### L. Aggiornare la documentazione
Descrivere soltanto il contratto corrente.

### M. Verificare riferimenti residui
`git grep` non deve trovare i nomi dei file e simboli rimossi.

### N. Eseguire strict
`go run ./scripts/archcheck --strict` deve terminare con exit code 0.

### O. Conservare le migration SQL storiche
Non modificare né rimuovere migration già applicabili.

### P. Formattare
Eseguire `gofmt` sui file Go modificati.

### Q. Test mirati
`go test ./scripts/archcheck/...`.

### R. Test completi
`go test ./...`.

### S. Vet e build
`go vet ./...` e `go build ./...`.

### T. Gate finale
Eseguire nuovamente `go run ./scripts/archcheck --strict`.

### U. Ripetere la baseline
Tutte le ricerche del punto C devono restituire zero residui rilevanti.

### V. Controllare il diff
`git diff --check` e `git diff origin/main...HEAD`.

### W. Aggiornare solo fonti canoniche
Nessun nuovo tracker transitorio.

### X. Rebase
`git fetch origin && git rebase origin/main`, poi ripetere test e gate.

### Y. Commit e push
Usare il messaggio indicato e `git push -u origin HEAD`.

### Z. Verifica post-push
Controllare `git log -n 5 --oneline`, `git status -sb` e check remoti.

## Criteri di accettazione

- [ ] Allowlist API assente.
- [ ] Baseline SQL assente.
- [ ] JSON transitorio assente.
- [ ] Nessun loader o riferimento residuo.
- [ ] `archcheck --strict` passa.
- [ ] Nessuna branch secondaria creata.
