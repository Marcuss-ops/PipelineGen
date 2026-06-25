# PG-004 — API Artlist

**Stato:** COMPLETATO. Non rieseguire salvo regressione.

Workflow unico: lavorare su `main`, senza branch e senza PR.

Verifica: sincronizzare `main`, controllare che l’handler Artlist usi porte application, eseguire test mirati, committare solo una regressione reale, fare rebase su `origin/main` e usare `git push origin main`.

- [x] Handler separato dagli adapter concreti.
- [x] Wiring concreto in `internal/app`.
