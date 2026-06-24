# PG-014 — Canonicalizzare la configurazione del worker

**Priorità:** P1  
**Branch unica:** `codex/pg-014-worker-url`  
**Commit suggerito:** `refactor(worker): remove legacy url fallbacks`

## Missione

Mantenere una sola sorgente di configurazione dell’URL usato dal worker per collegarsi al servizio coordinatore. Rimuovere il precedente alias, i fallback duplicati e gli esempi obsoleti.

## Scope

- `cmd/worker/**`
- `internal/infrastructure/config/**`
- `config.example.yaml`
- `docker-compose.yml`
- `Makefile`
- documentazione worker

## Checklist A–Z

A. Aggiornare `main`.
B. Creare esclusivamente la branch indicata.
C. Cercare resolver, source logger, campi environment e YAML.
D. Leggere il resolver esistente senza crearne un secondo.
E. Fissare una sola precedenza: environment canonico, YAML canonico, default locale.
F. Rimuovere il precedente alias environment.
G. Rimuovere il vecchio nome dai log source.
H. Consolidare il campo YAML.
I. Aggiornare Docker Compose.
J. Aggiornare Makefile e script.
K. Aggiornare documentazione ed esempi.
L. Testare l’override environment.
M. Testare il fallback YAML.
N. Testare il default locale.
O. Testare valori vuoti o non validi.
P. Eseguire `gofmt`.
Q. Eseguire test worker e config.
R. Eseguire `go test ./...`.
S. Eseguire `go vet ./...` e `go build ./...`.
T. Eseguire archcheck ratchet.
U. Ripetere la ricerca iniziale e verificare zero alias.
V. Controllare diff e file fuori scope.
W. Aggiornare solo fonti canoniche.
X. Rebase su `origin/main` e ritestare.
Y. Commit e push.
Z. Verificare log, status e check remoti.

## Criteri di accettazione

- [ ] Una sola sorgente environment canonica.
- [ ] Nessun alias precedente.
- [ ] Config, Compose, test e documentazione coerenti.
- [ ] Test, build e gate verdi.
- [ ] Nessuna branch secondaria creata.
