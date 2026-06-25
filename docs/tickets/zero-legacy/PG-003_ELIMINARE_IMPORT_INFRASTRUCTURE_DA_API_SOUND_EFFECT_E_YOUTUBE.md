# PG-003 — API Sound Effect e YouTube

**Stato:** COMPLETATO. Non rieseguire salvo regressione.

Workflow unico: lavorare su `main`, senza branch e senza PR.

Verifica: sincronizzare `main`, controllare che gli handler non importino adapter concreti, eseguire test mirati, committare solo una regressione reale, fare rebase su `origin/main` e usare `git push origin main`.

- [x] Porte application tipizzate.
- [x] Adapter costruiti in `internal/app`.
