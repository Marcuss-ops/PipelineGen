# PG-029 — Spezzare scripts/types.go e rimuovere stub

**Branch:** `codex/pg-029-scripts-types`

## Checklist A–Z
A. Sync main. B. Crea solo la branch. C. Conta LOC e simboli temporanei. D. Mappa ogni simbolo ai consumer. E. Classifica DTO, port, use case, helper e dead code. F. Crea file per concern nel package esistente. G. Riusa contratti canonici. H. Elimina simboli senza consumer. I. Elimina re-export e alias. J. Sostituisci dipendenze non tipizzate con tipi reali. K. Elimina costruttori finti. L. Aggiorna API e composition. M. Aggiungi test di validazione. N. Riduci o elimina types.go. O. Cerca zero stub target. P. Gofmt. Q. Test scripts/API. R. Test completi. S. Vet/build. T. Archcheck. U. Riconta LOC. V. Diff. W. Docs canoniche. X. Rebase. Y. Commit/push. Z. Verifica remoto.

## Done
- [ ] types.go eliminato o sotto 200 righe coese.
- [ ] Costruttori finti rimossi.
- [ ] Nessun alias ponte.
- [ ] Test verdi.
