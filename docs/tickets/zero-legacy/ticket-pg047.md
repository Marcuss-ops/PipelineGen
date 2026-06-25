# PG-047 — Compattare il registry dei comandi admin

**Branch:** `codex/pg-047-admin-registry`
**Dipendenze:** PG-026

## Checklist A–Z
A. Sync main. B. Crea solo la branch. C. Inventaria comandi, handler, help e categorie. D. Definisci Command con Name, Help e Run. E. Crea un registry statico unico. F. Genera help dal registry. G. Esegui dispatch dal registry. H. Rimuovi switch duplicati. I. Raggruppa file per capability. J. Mantieni i nomi correnti. K. Non introdurre reflection o plugin. L. Integra i comandi ritirati da PG-026. M. Aggiungi test nomi unici. N. Aggiungi test handler non nil e help completo. O. Porta cmd/admin sotto 20 file o documenta un limite misurabile. P. Gofmt. Q. Test admin. R. Test completi. S. Vet/build. T. Archcheck. U. Riconta file e fonti command list. V. Diff. W. Docs canoniche. X. Rebase. Y. Commit/push. Z. Verifica remoto.

## Done
- [ ] Registry unico.
- [ ] Help e dispatch derivati dal registry.
- [ ] Nessun nome duplicato.
- [ ] Test verdi.
