# PG-018 — Boundary application/jobs

**Stato:** PARZIALE

Workflow: solo `main`, senza branch e senza PR.

PG-017 ha già eliminato la facade. Non ricrearla.

Todo: sincronizzare `main`; verificare dipendenze concrete nel service jobs; completare il contratto Store minimo; introdurre un generatore ID testabile; rendere canonico il DTO statistiche; spostare dettagli SQLite negli adapter; aggiornare costruttore, composition, fake e test; aggiungere assertion compile-time; verificare nessun dettaglio adapter nell’application; eseguire gofmt, test jobs, test completi, vet, build e archcheck; rebase su `origin/main`; commit mirato; `git push origin main`.

Done: service jobs basato solo su porte domain, ID testabile, nessun adapter concreto.
