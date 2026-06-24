# PG-043 — Spezzare outbox/metadata_export.go

**Branch:** `codex/pg-043-metadata-export`

## Checklist A–Z
A. Sync main. B. Crea solo la branch. C. Conta LOC e mappa selezione, mapping, serializzazione e delivery. D. Definisci DTO export canonico. E. Separa query port. F. Separa mapper. G. Separa serializer. H. Separa destination port. I. Mantieni un solo dispatcher. J. Mantieni formato export stabile. K. Mantieni idempotenza. L. Gestisci retry e partial failure. M. Aggiungi golden test del payload. N. Aggiungi test delivery. O. Porta file sotto 300 righe. P. Gofmt. Q. Test outbox/export. R. Test completi. S. Vet/build. T. Archcheck. U. Riconta LOC. V. Diff. W. Docs canoniche. X. Rebase. Y. Commit/push. Z. Verifica remoto.

## Done
- [ ] File sotto 300 righe.
- [ ] Formato export invariato.
- [ ] Un solo dispatcher.
- [ ] Idempotenza e failure testate.
