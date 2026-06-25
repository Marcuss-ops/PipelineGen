# PG-019 — Ownership degli handle storage

**Priorità:** P1
**Branch unica:** `codex/pg-019-storage-ownership`

## Obiettivo

Usare il set storage come unica sorgente degli handle Primary e Observability, eliminando campi duplicati e chiusure concorrenti.

## Checklist A–Z

A. Sincronizzare `main` e verificare working tree pulita.
B. Creare soltanto la branch indicata.
C. Inventariare tutti gli accessi agli handle duplicati.
D. Classificare ogni accesso per owner Primary o Observability.
E. Riutilizzare accessor canonici.
F. Migrare composition.
G. Migrare lifecycle.
H. Migrare shutdown.
I. Migrare il wiring del logging.
J. Correggere cleanup parziale.
K. Confermare un solo owner del close.
L. Eliminare i campi duplicati.
M. Eliminare commenti di compatibilità ormai falsi.
N. Aggiornare test open, migrate e close.
O. Verificare assenza di double-close.
P. Eseguire `gofmt`.
Q. Eseguire test app e storage mirati.
R. Eseguire `go test ./...`.
S. Eseguire `go vet ./...` e `go build ./...`.
T. Eseguire archcheck ratchet.
U. Ripetere la ricerca iniziale.
V. Controllare diff e scope.
W. Aggiornare soltanto fonti canoniche.
X. Rebase su `origin/main` e ritestare.
Y. Commit e push della sola branch.
Z. Verificare history, status e check remoti.

## Criteri di accettazione

- [ ] Un solo owner degli handle.
- [ ] Nessun campo duplicato.
- [ ] Nessun double-close.
- [ ] Test e gate verdi.
