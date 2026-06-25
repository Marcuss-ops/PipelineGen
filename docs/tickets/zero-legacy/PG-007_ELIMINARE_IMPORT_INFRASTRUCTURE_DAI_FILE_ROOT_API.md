# PG-007 — Root API

**Stato:** APERTO

Solo `main`; nessuna branch e nessuna PR.

Todo: sincronizzare `main`; inventariare gli import concreti nei file root API; separare route, lifecycle e health; riutilizzare interfacce esistenti; costruire adapter in `internal/app`; eliminare cast, setter globali e fallback; aggiornare test; verificare zero import infrastructure; eseguire gofmt, test, vet, build e archcheck; rebase su `origin/main`; commit mirato; `git push origin main`.

Done: root API puro trasporto, nessun adapter concreto, test verdi.
