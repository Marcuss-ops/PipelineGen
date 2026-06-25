# PG-021 — Migrazione documenti Drive come comando one-shot

**Branch:** `codex/pg-021-drive-doc-migration`

## Checklist A–Z
A. Sync main. B. Crea solo la branch. C. Cerca la migrazione nel bootstrap. D. Leggi port Drive esistenti. E. Estrai un use case. F. Definisci input root/target. G. Implementa dry-run predefinito. H. Implementa execute esplicito. I. Pagina la scansione. J. Rendi il move idempotente. K. Produci report found/moved/skipped/failed. L. Aggiungi comando admin. M. Rimuovi la chiamata startup. N. Testa partial failure. O. Testa rerun a zero. P. Gofmt. Q. Test admin/application/Drive. R. Test completi. S. Vet e build. T. Archcheck. U. Verifica zero migrazione a startup. V. Controlla diff. W. Aggiorna runbook. X. Rebase e ritesta. Y. Commit e push. Z. Verifica remoto.

## Done
- [ ] Startup senza migrazione legacy.
- [ ] Comando dry-run/execute testato.
- [ ] Report deterministico.
- [ ] Rerun idempotente.
