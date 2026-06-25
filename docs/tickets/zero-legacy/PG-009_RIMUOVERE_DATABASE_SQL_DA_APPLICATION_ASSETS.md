# PG-009 — Rimuovere database/sql da application assets

**Stato:** APERTO

Solo `main`; nessuna branch e nessuna PR.

Todo: sincronizzare `main`; inventariare SQL e tipi concreti in application assets; riutilizzare repository e porte canoniche; spostare query e transazioni negli adapter database; aggiornare costruttori, composition, fake e test; eliminare escape hatch e duplicazioni; verificare zero import `database/sql`; eseguire gofmt, test assets, test completi, vet, build e archcheck; rebase su `origin/main`; commit mirato; `git push origin main`.

Done: application assets senza SQL, adapter unici, test verdi.
