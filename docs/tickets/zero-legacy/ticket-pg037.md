# PG-037 — Rimuovere utility e API deprecate

**Branch:** `codex/pg-037-deprecated-utils`

## Checklist A–Z
A. Sync main. B. Crea solo la branch. C. Inventaria tutti i simboli marcati Deprecated nei target. D. Trova il sostituto canonico. E. Mappa consumer e test. F. Migra un simbolo alla volta. G. Aggiungi test al sostituto. H. Elimina la definizione deprecata. I. Elimina test e docs del vecchio nome. J. Non lasciare alias o forwarding wrapper. K. Verifica semantica concorrente. L. Verifica semantica media/FFmpeg. M. Riduci export. N. Cerca zero marker nei target. O. Non toccare deprecazioni esterne. P. Gofmt. Q. Test utility, Drive e media. R. Test completi. S. Vet/build. T. Archcheck. U. Ripeti inventario. V. Diff. W. Docs canoniche. X. Rebase. Y. Commit/push. Z. Verifica remoto.

## Done
- [ ] Tutti i consumer migrati.
- [ ] Vecchie funzioni eliminate.
- [ ] Nessun wrapper compatibile.
- [ ] Test verdi.
