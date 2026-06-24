# PG-022 — Provisioning folder Drive fuori dal bootstrap

**Branch:** `codex/pg-022-drive-folder-provisioning`
**Dipendenze:** PG-019, PG-021

## Checklist A–Z
A. Sync main. B. Crea solo la branch. C. Cerca query folder e chiamate SDK nel bootstrap. D. Leggi registry folder. E. Definisci FolderTree DTO. F. Definisci FolderRepository. G. Definisci DriveFolder port. H. Implementa use case idempotente. I. Implementa adapter storage. J. Implementa adapter Drive. K. Crea wiring in app. L. Elimina mutazione opportunistica della config. M. Rimuovi SQL dal bootstrap. N. Centralizza nomi e source. O. Testa errori Drive e storage. P. Gofmt. Q. Test mirati. R. Test completi. S. Vet/build. T. Archcheck. U. Ripeti baseline. V. Diff. W. Docs canoniche. X. Rebase. Y. Commit/push. Z. Verifica remoto.

## Done
- [ ] Bootstrap senza query o SDK per provisioning.
- [ ] Use case tipizzato.
- [ ] Registry nomi unico.
- [ ] Test e gate verdi.
