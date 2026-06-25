# PG-012 — SQL da Books, Images, Voiceover e Scripts

**Stato:** APERTO

Workflow: solo `main`, senza branch e senza PR.

Passi: sincronizzare `main`; inventariare gli import e gli handle SQL nei quattro domini; riutilizzare porte e repository esistenti; spostare query e transazioni negli adapter database; aggiornare composition, fake e test; eliminare escape hatch e mapping duplicati; verificare zero import SQL nei target; eseguire gofmt, test mirati, test completi, vet, build e archcheck; rebase su `origin/main`; commit mirato; `git push origin main`.

Done: i quattro package application non dipendono dal database concreto e tutti i gate passano.
