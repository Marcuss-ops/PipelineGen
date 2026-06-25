# PG-031 — Ridurre il mega-package internal/app

**Branch:** `codex/pg-031-app-package-compaction`
**Dipendenze:** PG-019, PG-020, PG-028

## Checklist A–Z
A. Sync main. B. Crea solo la branch. C. Conta file top-level e LOC. D. Mappa responsabilità e consumer. E. Unisci micro-file realmente coesi. F. Consolida adapter per capability. G. Elimina file orfani. H. Mantieni un solo composition root. I. Evita wiring duplicato. J. Consolida helper test. K. Rimuovi file solo-commento inutili. L. Riduci export. M. Aggiorna ownership. N. Verifica una sola costruzione per adapter. O. Porta il package sotto soglia. P. Gofmt. Q. Test app. R. Test completi. S. Vet/build. T. Archcheck. U. Riconta file. V. Diff. W. Docs canoniche. X. Rebase. Y. Commit/push. Z. Verifica remoto.

## Done
- [ ] internal/app massimo 30 file Go.
- [ ] Un solo composition root.
- [ ] Nessun registry o adapter duplicato.
- [ ] Test verdi.
