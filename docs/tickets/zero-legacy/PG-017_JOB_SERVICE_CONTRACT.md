# PG-017 — Contratto canonico del job service

**Priorità:** P0
**Branch unica:** `codex/pg-017-job-service-contract`

## Obiettivo

Usare una sola interfaccia domain per il job service e collegare direttamente l’implementazione application. Rimuovere l’indirezione basata su funzioni delegate e inizializzazione tardiva.

## Checklist A–Z

A. Aggiornare main.
B. Creare la sola branch indicata.
C. Cercare tutti i consumer del contratto job.
D. Leggere ADR-005.
E. Definire l’interfaccia canonica.
F. Limitare i metodi a quelli usati.
G. Aggiungere assertion compile-time.
H. Migrare i field consumer.
I. Migrare i parametri dei costruttori.
J. Aggiornare l’helper di enqueue tipizzato.
K. Rimuovere l’inizializzazione tardiva.
L. Rimuovere i setter di wiring.
M. Rimuovere le funzioni delegate.
N. Validare le dipendenze al bootstrap.
O. Aggiornare fake e test.
P. Eseguire gofmt.
Q. Eseguire test domain, jobs e script API.
R. Eseguire go test completo.
S. Eseguire vet e build.
T. Eseguire archcheck ratchet.
U. Verificare zero vecchi simboli.
V. Controllare diff e scope.
W. Aggiornare ADR allo stato implementato.
X. Rebase e ritestare.
Y. Commit e push.
Z. Verificare log, status e check.

## Criteri di accettazione

- [ ] Una sola interfaccia job service.
- [ ] Nessuna indirezione delegate.
- [ ] Nessun setter di wiring tardivo.
- [ ] Test e gate verdi.
