# PG-028 — Spezzare internal/app/composition.go

**Branch:** `codex/pg-028-composition-split`

## Checklist A–Z
A. Sync main. B. Crea solo la branch. C. Conta LOC e BuildBundle. D. Mappa builder, input e output. E. Definisci confini per capability. F. Mantieni package app come unico root. G. Sposta un builder alla volta. H. Mantieni registry e resolver condivisi unici. I. Usa bundle tipizzati. J. Evita cicli import. K. Elimina helper duplicati. L. Aggiungi test builder mirati. M. Riduci composition.go a root e ordine. N. Aggiorna ownership. O. Verifica nessun cambio runtime. P. Gofmt. Q. Test app. R. Test completi. S. Vet/build. T. Archcheck. U. Riconta LOC e builder. V. Diff semantico. W. Docs canoniche. X. Rebase. Y. Commit/push. Z. Verifica remoto.

## Done
- [ ] composition.go sotto 300 righe.
- [ ] Un builder owner per capability.
- [ ] Nessun registry duplicato.
- [ ] Test e build invariati.
