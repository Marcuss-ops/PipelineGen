# PG-041 — Eliminare stub locali da scripts/flow_helpers.go

**Branch:** `codex/pg-041-script-flow-helpers`
**Dipendenze:** PG-029, PG-033

## Checklist A–Z
A. Sync main. B. Crea solo la branch. C. Cerca local type stubs e package rimossi. D. Mappa consumer e comportamento. E. Cerca contratti reali esistenti. F. Sostituisci con porte canoniche oppure elimina path dead. G. Non creare alias locali. H. Non ricreare package rimossi. I. Aggiorna flow constructor. J. Aggiorna handler. K. Aggiorna composition. L. Elimina metodi no-op. M. Aggiorna fake tipizzati. N. Testa flow completo. O. Porta il file sotto 250 righe. P. Gofmt. Q. Test scripts/flow. R. Test completi. S. Vet/build. T. Archcheck. U. Cerca zero stub locali. V. Diff. W. Docs canoniche. X. Rebase. Y. Commit/push. Z. Verifica remoto.

## Done
- [ ] Zero local type stubs.
- [ ] Nessun package rimosso ricreato.
- [ ] File sotto 250 righe.
- [ ] Flow testato.
