# PG-002 — Eliminare import infrastructure da API Channels e Images

**Stato:** COMPLETATO — NON RIESEGUIRE

**Workflow:** solo `main`; nessuna branch e nessuna PR.

Il lavoro è già presente su `main`: gli handler usano porte tipizzate e gli adapter concreti sono costruiti nel composition root.

## Verifica

1. Sincronizzare `main`.
2. Cercare import infrastructure nei package target.
3. Confermare zero regressioni.
4. Eseguire i test mirati.
5. Non modificare il codice se è già conforme.
6. Committare solo una regressione dimostrata.
7. Fare rebase su `origin/main`.
8. Eseguire `git push origin main` e verificare il commit remoto.

## Done verificato

- [x] Handler Channels separato dagli adapter concreti.
- [x] Handler Images separato dagli adapter concreti.
- [x] Porte tipizzate e wiring in `internal/app`.
