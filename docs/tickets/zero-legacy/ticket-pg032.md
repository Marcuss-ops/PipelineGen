# PG-032 — Ridurre mega-package Artlist

**Branch:** `codex/pg-032-artlist-package`
**Dipendenze:** PG-004

## Checklist A–Z
A. Sync main. B. Crea solo la branch. C. Conta file e mappa dipendenze. D. Classifica search, run, ingest, conversion, status e ports. E. Definisci subpackage solo per confini stabili. F. Mantieni un solo run orchestrator. G. Mantieni un solo provider registry. H. Mantieni un solo run state. I. Separa DTO dagli adapter. J. Elimina forwarding wrapper. K. Aggiorna module wiring. L. Aggiorna test per stage. M. Verifica il boundary application. N. Verifica assenza di cicli. O. Porta directory sotto soglia. P. Gofmt. Q. Test Artlist. R. Test completi. S. Vet/build. T. Archcheck. U. Riconta file/import. V. Diff. W. Docs canoniche. X. Rebase. Y. Commit/push. Z. Verifica remoto.

## Done
- [ ] Directory sotto 30 file.
- [ ] Un solo orchestratore e registry.
- [ ] Nessun wrapper pass-through.
- [ ] Test verdi.
