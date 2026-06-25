# PG-016 — Completare il boundary application YouTube

**Priorità:** P1  
**Branch unica:** `codex/pg-016-youtube-boundary`  
**Commit suggerito:** `refactor(youtube): remove config and callback facade leakage`

## Missione

Rimuovere dipendenze infrastrutturali e callback generiche dall’orchestratore YouTube. L’application deve ricevere configurazione minimale e porte esplicite.

## Scope

- `internal/application/youtube/**`
- `internal/app/*youtube*`
- `internal/infrastructure/youtube/**`
- `internal/infrastructure/config/**`

## Checklist A–Z

A. Aggiornare `main`.
B. Creare esclusivamente `codex/pg-016-youtube-boundary`.
C. Cercare import config, callback bag e self-injection.
D. Leggere i contratti e i service esistenti.
E. Definire `RuntimeConfig` con soli campi usati.
F. Mappare config infrastrutturale nel composition root.
G. Inventariare tutti i metodi callback.
H. Sostituire callback generiche con porte minime.
I. Rimuovere il passaggio dell’orchestratore come callback implementation.
J. Aggiornare extraction e metadata service.
K. Aggiornare fake e test.
L. Eliminare import infrastructure dall’application YouTube.
M. Pulire commenti storici non necessari.
N. Aggiungere assertion compile-time.
O. Validare nil e typed-nil nel composition root.
P. Eseguire `gofmt`.
Q. Eseguire `go test ./internal/application/youtube/...`.
R. Eseguire `go test ./...`.
S. Eseguire `go vet ./...` e `go build ./...`.
T. Eseguire archcheck ratchet.
U. Ripetere le ricerche iniziali.
V. Controllare diff e scope.
W. Aggiornare solo documentazione canonica.
X. Rebase su `origin/main` e ritestare.
Y. Commit e push.
Z. Verificare log, status e check remoti.

## Criteri di accettazione

- [ ] Zero import infrastructure nell’application YouTube.
- [ ] Config minimale tipizzata.
- [ ] Nessun callback bag generale.
- [ ] Nessuna self-injection.
- [ ] Test, build e gate verdi.
