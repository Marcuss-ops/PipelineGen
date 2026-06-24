# PG-035 — Eliminare GemmaMemory fittizio

**Branch:** `codex/pg-035-gemmamemory`

## Decisione
Collegare una sola implementazione reale già esistente oppure rimuovere feature, port, wiring, config e call site. Nessun metodo può restituire successi finti o no-op.

## Checklist A–Z
A. Sync main. B. Crea solo la branch. C. Cerca package, metodi e call site. D. Cerca implementazioni reali. E. Mappa il comportamento atteso. F. Applica il decision tree. G. Collega l’implementazione reale oppure elimina la feature. H. Elimina default permissivi. I. Aggiorna la pipeline. J. Aggiorna config. K. Aggiorna composition. L. Aggiorna test hit/miss o assenza feature. M. Rimuovi package se vuoto. N. Aggiorna docs. O. Cerca zero simboli fittizi. P. Gofmt. Q. Test scripts. R. Test completi. S. Vet/build. T. Archcheck. U. Ripeti baseline. V. Diff. W. Docs canoniche. X. Rebase. Y. Commit/push. Z. Verifica remoto.

## Done
- [ ] Nessun metodo fittizio.
- [ ] Feature reale o completamente assente.
- [ ] Nessun flag o wiring orfano.
- [ ] Test verdi.
