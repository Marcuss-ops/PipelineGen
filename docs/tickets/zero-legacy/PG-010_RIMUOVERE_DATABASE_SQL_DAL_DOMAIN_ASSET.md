# PG-010 — Domain asset senza SQL

**Stato:** APERTO

Workflow: solo `main`, senza branch e senza PR.

Passi: sincronizzare `main`; cercare dipendenze database nel domain asset; spostare query e mapping negli adapter; mantenere nel domain solo modelli e contratti; aggiornare consumer e test; verificare zero import SQL; eseguire test, vet, build e archcheck; rebase su `origin/main`; commit; `git push origin main`.

Done: domain asset indipendente dal database e test verdi.
