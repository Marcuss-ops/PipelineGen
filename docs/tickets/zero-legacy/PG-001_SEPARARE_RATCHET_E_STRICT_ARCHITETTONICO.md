# PG-001 — Separare ratchet e strict architettonico

**Priorità:** P0  
**Branch unica:** `codex/pg-001-archcheck-modes`  
**Commit suggerito:** `refactor(archcheck): separate ratchet and strict modes`  
**Dipendenze:** Nessuna

## Missione

Rendere onesto il gate: il controllo che accetta baseline esistenti deve chiamarsi ratchet; strict deve significare zero eccezioni.

## Decisione già presa

Il CI continua temporaneamente a usare `--ratchet`. `--strict` viene implementato come zero assoluto, ma sarà promosso nel CI solo nel ticket finale.

L'agente non deve ridefinire l'architettura. Deve applicare questa decisione e fermarsi se una stop condition la rende impossibile.

## Risultato atteso

Due modalità non ambigue, report JSON distinti e nessuna dichiarazione `legacy_budget: 0` quando esistono baseline.

## Scope consentito

- `scripts/archcheck/**`
- `scripts/ci-architectural-checks.sh`
- `scripts/archcheck/*_test.go`
- `ARCHITECTURE.md`

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

`git checkout -b codex/pg-001-archcheck-modes`. Non creare altre branch.

### C. Catturare la baseline verificabile

- `go run ./scripts/archcheck --strict || true`
- `grep -R "LegacyBudget\|databaseSQLLegacyBaseline\|loadAllowlist" -n scripts/archcheck`

### D. Leggere i contratti prima di modificare

Aprire tutti i file elencati in **Scope consentito** e cercare implementazioni equivalenti. Se esiste già il contratto canonico, riusarlo; non crearne un duplicato.

### E. Definire la semantica dei flag

Aggiungere `--ratchet` per il comportamento attuale e riservare `--strict` a zero allowlist/zero baseline.

### F. Correggere il report JSON

In ratchet esporre `mode=ratchet`; in strict `mode=strict`. Rimuovere campi fuorvianti o valorizzarli con conteggi reali.

### G. Implementare strict zero API

Strict deve fallire se esiste anche un solo import API→infrastructure, indipendentemente dall'allowlist.

### H. Implementare strict zero SQL

Strict deve fallire per qualunque import `database/sql` nei layer vietati, indipendentemente dalla baseline.

### I. Mantenere il ratchet monotono

Ratchet deve fallire su regressioni e su entry stale, senza consentire aumenti.

### J. Aggiornare il wrapper CI

Lo script Bash deve invocare solo `go run ./scripts/archcheck --ratchet` fino a PG-046.

### K. Aggiungere test delle modalità

Testare repository simulati o fixture per zero, baseline, regressione e stale entry.

### L. Testare exit code

Verificare 0 su pass, 1 su violazione, 2 su errore operativo.

### M. Eliminare nomenclatura vecchia

Rimuovere `focused_gate_passed` dai nuovi consumer; mantenere solo se necessario per un test di migrazione e poi cancellarlo.

### N. Documentare promozione finale

ARCHITECTURE deve dire che strict diventa CI obbligatorio solo dopo chiusura baseline.

### O. Non toccare le baseline

Questo ticket non riduce allowlist o SQL baseline; cambia solo la semantica e i test.

### P. Formattare

Eseguire `gofmt -w` su tutti e soli i file Go modificati. Per YAML/JSON/Markdown mantenere il formato esistente.

### Q. Eseguire i test mirati

- `go test ./scripts/archcheck/...`

### R. Eseguire i test di regressione più ampi

- `go test ./...`

### S. Eseguire vet e build

- `go vet ./internal/...`
- `go build ./...`

### T. Eseguire i gate architetturali

- `go run ./scripts/archcheck --ratchet`
- `go run ./scripts/archcheck --strict || true`

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

- [ ] `--ratchet` replica il comportamento corrente senza regressioni.
- [ ] `--strict` fallisce con qualunque eccezione esistente.
- [ ] Il CI wrapper usa `--ratchet`.
- [ ] Test delle due modalità verdi.
- [ ] Nessuna nuova baseline.

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
