# PG-033 — Tipizzare i wiring principali

**Branch:** `codex/pg-033-typed-wiring`
**Dipendenze:** PG-020, PG-029

## Checklist A–Z
A. Sync main. B. Crea solo la branch. C. Genera inventario dei dependency slot non tipizzati. D. Identifica il valore concreto passato. E. Elimina slot di package rimossi. F. Riusa porte esistenti. G. Definisci porte minime nel layer owner. H. Aggiorna costruttori. I. Aggiorna composition. J. Aggiorna handler. K. Rimuovi type switch compensativi. L. Aggiorna fake compile-time. M. Aggiungi gate anti-regressione nei package chiusi. N. Misura count prima/dopo. O. Verifica zero nei file target. P. Gofmt. Q. Test scripts, API e app. R. Test completi. S. Vet/build. T. Archcheck. U. Ripeti inventario. V. Diff. W. Docs canoniche. X. Rebase. Y. Commit/push. Z. Verifica remoto.

## Done
- [ ] Zero dependency slot generici nei file target.
- [ ] Nessun cast compensativo.
- [ ] Porte nel layer corretto.
- [ ] Conteggio globale ridotto senza regressioni.
