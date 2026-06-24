# PG-044 — Ridurre service_orchestrator YouTube

**Branch:** `codex/pg-044-youtube-orchestrator`
**Dipendenze:** PG-016

## Checklist A–Z
A. Sync main. B. Crea solo la branch. C. Conta LOC e mappa metodi. D. Identifica service già owner di extraction, metadata, tagging e cache. E. Sposta metodi nel service owner. F. Mantieni orchestrator come facade applicativa minima. G. Elimina stato duplicato. H. Elimina callback bag residui. I. Mantieni config runtime minima. J. Aggiorna costruttore. K. Aggiorna composition. L. Aggiorna handler consumer. M. Aggiungi test orchestrator. N. Aggiungi test dei service estratti. O. Porta file sotto 300 righe. P. Gofmt. Q. Test YouTube. R. Test completi. S. Vet/build. T. Archcheck. U. Riconta LOC e dipendenze. V. Diff. W. Docs canoniche. X. Rebase. Y. Commit/push. Z. Verifica remoto.

## Done
- [ ] Orchestrator sotto 300 righe.
- [ ] Nessuno stato o writer duplicato.
- [ ] Ogni metodo nel service owner.
- [ ] Test verdi.
