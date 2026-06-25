# PG-016 — Completare il boundary application YouTube

**Stato:** PARZIALE

Workflow: solo `main`, senza branch e senza PR.

Il RuntimeConfig e la rimozione dell’import config concreto sono già completati. Non rifarli.

Todo: sincronizzare `main`; verificare callback facade, self-injection e dipendenze residue; sostituire callback bag con porte esplicite; mantenere un solo writer; eliminare stato e mapping duplicati; aggiornare composition e test; cercare zero import infrastructure nell’application YouTube; eseguire gofmt, test YouTube, test completi, vet, build e archcheck; rebase su `origin/main`; commit mirato; `git push origin main`.

Done: application YouTube dipende solo da tipi e porte proprie, un solo writer, nessuna callback facade.
