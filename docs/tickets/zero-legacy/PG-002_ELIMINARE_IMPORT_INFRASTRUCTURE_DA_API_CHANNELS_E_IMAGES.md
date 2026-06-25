# PG-002 — Eliminare import infrastructure da API Channels e Images

**Priorità:** P0  
**Branch unica:** `codex/pg-002-api-channels-images`  
**Commit suggerito:** `refactor(api): remove infrastructure imports for pg-002`  
**Dipendenze:** PG-001

## Missione

Rendere il transport API dipendente solo da contratti application/domain e da DTO di trasporto.

## Decisione già presa

Gli handler ricevono porte tipizzate. Gli adapter concreti vengono costruiti in `internal/app`. La riga allowlist viene rimossa nello stesso commit.

L'agente non deve ridefinire l'architettura. Deve applicare questa decisione e fermarsi se una stop condition la rende impossibile.

## Risultato atteso

Zero import `internal/infrastructure/*` nei file target e nessuna entry stale.

## Scope consentito

- `internal/api/channels/**`
- `internal/api/images/**`
- `internal/application/channels/**`
- `internal/application/images/**`
- `internal/app/*channel*`
- `internal/app/*image*`
- `docs/migrations/api-infrastructure-imports-allowlist.txt`

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

`git checkout -b codex/pg-002-api-channels-images`. Non creare altre branch.

### C. Catturare la baseline verificabile

- `grep -n "internal/infrastructure" internal/api/channels/impl.go internal/api/images/impl.go || true`
- `grep -n "internal/api/" docs/migrations/api-infrastructure-imports-allowlist.txt || true`

### D. Leggere i contratti prima di modificare

Aprire tutti i file elencati in **Scope consentito** e cercare implementazioni equivalenti. Se esiste già il contratto canonico, riusarlo; non crearne un duplicato.

### E. Identificare ogni dipendenza concreta

Per ogni file target elencare tipo concreto, metodi realmente usati e comportamento d'errore.

### F. Cercare porte esistenti

Riutilizzare porte application già presenti. Vietato creare interfacce duplicate nello stesso dominio.

### G. Definire porte minime mancanti

Creare solo metodi usati dall'handler; niente getter di DB, config concreta o client SDK.

### H. Spostare logica fuori dall'handler

Query, IO, upload, policy e orchestrazione devono vivere in application o infrastructure.

### I. Creare adapter in app

L'adapter implementa la porta e wrappa l'infrastruttura concreta; aggiungere assertion compile-time.

### J. Aggiornare costruttori/moduli

Passare le porte tipizzate all'API dal composition root.

### K. Preservare il contratto HTTP

Route, status code, payload e header devono restare invariati salvo bug già coperto da test.

### L. Eliminare import concreti

Rimuovere tutti gli import infrastructure dai file target.

### M. Rimuovere entry allowlist

Cancellare solo le righe corrispondenti ai file effettivamente puliti.

### N. Aggiungere gate test locale

Il package API interessato deve avere un test statico o usare il gate comune contro import concreti.

### O. Verificare typed-nil e nil wiring

Le dipendenze mancanti devono fallire chiaramente a startup o produrre il comportamento 503 già previsto, non panic casuali.

### P. Formattare

Eseguire `gofmt -w` su tutti e soli i file Go modificati. Per YAML/JSON/Markdown mantenere il formato esistente.

### Q. Eseguire i test mirati

- `go test ./internal/api/...`
- `go test ./internal/application/...`

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

- [ ] Nessuno dei file target importa `internal/infrastructure/*`.
- [ ] Le relative righe sono rimosse dall'allowlist.
- [ ] Nessuna route o payload cambia.
- [ ] Adapter concreti solo in `internal/app`.
- [ ] Test handler e gate verdi.

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
