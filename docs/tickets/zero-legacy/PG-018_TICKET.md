# PG-018 — Boundary del servizio job

**Priorità:** P0
**Branch unica:** `codex/pg-018-jobs-boundary`

## Obiettivo

Il servizio applicativo usa contratti domain e collaboratori tipizzati. I dettagli concreti restano nei rispettivi adapter.

## Checklist A–Z

A. Sincronizzare main.
B. Creare soltanto la branch indicata.
C. Inventariare dipendenze concrete e generatori ID.
D. Leggere Store, DTO e composition correnti.
E. Completare il contratto Store minimo.
F. Definire statistiche canoniche.
G. Iniettare il contratto Store.
H. Definire un generatore ID testabile.
I. Aggiornare il costruttore.
J. Aggiornare l’adapter di persistenza.
K. Aggiornare il composition root.
L. Aggiungere assertion compile-time.
M. Aggiornare fake e fixture.
N. Testare active key e correlation ID.
O. Verificare assenza di dettagli concreti nel service.
P. Eseguire gofmt.
Q. Eseguire test mirati.
R. Eseguire tutti i test.
S. Eseguire vet e build.
T. Eseguire archcheck ratchet.
U. Ripetere l’inventario.
V. Controllare diff e scope.
W. Aggiornare solo documentazione canonica.
X. Rebase e ritestare.
Y. Commit e push.
Z. Verificare log, status e check.

## Criteri di accettazione

- [ ] Store domain unico.
- [ ] Generatore ID testabile.
- [ ] Statistiche canoniche.
- [ ] Nessun dettaglio adapter nel service.
- [ ] Test e gate verdi.
