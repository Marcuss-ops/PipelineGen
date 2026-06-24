# PG-027 — Rimuovere legacy cognitiva dai commenti

**Branch:** `codex/pg-027-comment-cleanup`

## Checklist A–Z
A. Sync main. B. Crea solo la branch. C. Cerca riferimenti a PR, wave, branch, date e commit. D. Classifica commenti operativi e cronologia. E. Preserva invarianti correnti. F. Rimuovi branch e hash. G. Rimuovi target già conclusi. H. Sposta decisioni durature in ADR. I. Aggiorna package docs. J. Mantieni warning operativi reali. K. Non cambiare logica. L. Non toccare commenti storici delle migration. M. Correggi commenti export per godoc. N. Cerca zero breadcrumb ingiustificati. O. Review manuale del diff. P. Gofmt. Q. Test internal. R. Test completi. S. Vet/build. T. Archcheck. U. Ripeti ricerca. V. Diff. W. Aggiorna ADR solo se necessario. X. Rebase. Y. Commit/push. Z. Verifica remoto.

## Done
- [ ] Commenti descrivono il contratto corrente.
- [ ] Nessuna cronologia PR nel codice.
- [ ] Nessun cambio comportamentale.
- [ ] Test e gate verdi.
