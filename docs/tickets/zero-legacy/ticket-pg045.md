# PG-045 — Spezzare YouTube metadata service

**Branch:** `codex/pg-045-youtube-metadata`

## Checklist A–Z
A. Sync main. B. Crea solo la branch. C. Conta LOC e mappa fetch, cache, enrich, classify e persist. D. Definisci DTO metadata canonico. E. Separa fetch port. F. Separa cache policy. G. Separa enrichment. H. Separa classification. I. Separa persistence port. J. Mantieni un solo mapping metadata. K. Mantieni timeout e retry correnti. L. Aggiorna orchestrator. M. Aggiungi test cache hit/miss. N. Aggiungi test failure e persistence. O. Porta service sotto 300 righe. P. Gofmt. Q. Test YouTube metadata. R. Test completi. S. Vet/build. T. Archcheck. U. Riconta LOC. V. Diff. W. Docs canoniche. X. Rebase. Y. Commit/push. Z. Verifica remoto.

## Done
- [ ] Service sotto 300 righe.
- [ ] Un solo DTO e mapping metadata.
- [ ] Cache policy esplicita.
- [ ] Test verdi.
