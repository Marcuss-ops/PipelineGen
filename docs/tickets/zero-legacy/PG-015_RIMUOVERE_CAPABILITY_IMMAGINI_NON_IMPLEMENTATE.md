# PG-015 — Rimuovere capability immagini non implementate

**Priorità:** P1  
**Branch unica:** `codex/pg-015-images-drop-stubs`  
**Commit suggerito:** `refactor(images): remove unimplemented public capabilities`

## Missione

Eliminare capability pubbliche che non hanno una implementazione reale. Il contratto immagini deve pubblicizzare solo operazioni eseguibili.

## Scope

- `internal/application/images/**`
- `internal/api/images/**`
- `internal/app/*image*`
- `internal/infrastructure/config/**`
- `config/**`
- `docs/**`

## Checklist A–Z

A. Aggiornare `main`.
B. Creare esclusivamente la branch indicata.
C. Cercare capability, status, route, metodi stub e config collegati.
D. Leggere registry e resolver esistenti.
E. Inventariare route e consumer.
F. Rimuovere le costanti capability senza implementazione.
G. Rimuovere lo status non implementato se diventa inutilizzato.
H. Rimuovere metodi e helper stub.
I. Rimuovere route che restituiscono deliberatamente 501.
J. Rimuovere flag e campi config orfani.
K. Aggiornare diagnostica e lista capability.
L. Aggiornare il composition root.
M. Aggiornare test API e application.
N. Aggiornare documentazione.
O. Verificare zero simboli rimossi.
P. Eseguire `gofmt`.
Q. Eseguire test immagini mirati.
R. Eseguire `go test ./...`.
S. Eseguire `go vet ./...` e `go build ./...`.
T. Eseguire archcheck ratchet.
U. Ripetere la ricerca iniziale.
V. Controllare diff e file fuori scope.
W. Aggiornare solo fonti canoniche.
X. Rebase su `origin/main` e ritestare.
Y. Commit e push.
Z. Verificare log, status e check remoti.

## Criteri di accettazione

- [ ] Nessuna capability non implementata esposta.
- [ ] Nessun endpoint 501 deliberato collegato.
- [ ] Nessun campo config orfano.
- [ ] Diagnostica coerente.
- [ ] Test, build e gate verdi.
