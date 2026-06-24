# PG-039 — Spezzare internal/app/lifecycle.go

**Branch:** `codex/pg-039-app-lifecycle`

## Checklist A–Z
A. Sync main. B. Crea solo la branch. C. Conta LOC e mappa startup/stop. D. Separa startup plan. E. Separa registrazione worker. F. Separa shutdown orchestration. G. Separa health/lifecycle adapter. H. Mantieni un solo owner del lifecycle. I. Mantieni ordine dipendenze. J. Mantieni job runner ultimo. K. Elimina closure duplicate. L. Tipizza StartupStep e stop callback. M. Aggiungi test ordine startup. N. Aggiungi test rollback su failure. O. Porta lifecycle.go sotto 300 righe. P. Gofmt. Q. Test app lifecycle. R. Test completi. S. Vet/build. T. Archcheck. U. Riconta LOC. V. Diff. W. Docs canoniche. X. Rebase. Y. Commit/push. Z. Verifica remoto.

## Done
- [ ] lifecycle.go sotto 300 righe.
- [ ] Un solo lifecycle owner.
- [ ] Ordine startup preservato.
- [ ] Rollback e shutdown testati.
