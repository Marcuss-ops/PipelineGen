# PG-038 — Spezzare YouTube tagutil

**Branch:** `codex/pg-038-youtube-tagutil`

## Checklist A–Z
A. Sync main. B. Crea solo la branch. C. Conta LOC e mappa funzioni. D. Separa parsing, normalizzazione, classificazione errori e regole tag. E. Identifica owner e consumer. F. Mantieni un solo registry delle regole. G. Sposta value object nel package owner. H. Elimina helper duplicati. I. Riduci stato globale. J. Mantieni API interna stabile solo durante il refactor. K. Aggiorna call site nello stesso ticket. L. Aggiungi test per ogni concern. M. Aggiungi test edge case. N. Porta il file sotto 300 righe o eliminalo. O. Verifica nessun package ponte. P. Gofmt. Q. Test YouTube tagutil. R. Test completi. S. Vet/build. T. Archcheck. U. Riconta LOC e duplicazioni. V. Diff. W. Docs canoniche. X. Rebase. Y. Commit/push. Z. Verifica remoto.

## Done
- [ ] File sotto 300 righe o eliminato.
- [ ] Regole centralizzate una volta.
- [ ] Nessun wrapper ponte.
- [ ] Test edge case verdi.
