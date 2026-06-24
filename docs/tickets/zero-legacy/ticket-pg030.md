# PG-030 — Splittare internal/domain/asset

**Branch:** `codex/pg-030-domain-asset-split`
**Dipendenze:** PG-009, PG-010

## Checklist A–Z
A. Sync main. B. Crea solo la branch. C. Conta file e costruisci grafo import. D. Mappa owner di tipi e repository. E. Definisci veri sotto-domini. F. Mantieni il modello asset base nel package owner. G. Muovi un concern per commit logico. H. Aggiorna consumer nello stesso passaggio. I. Non lasciare alias nel vecchio package. J. Posiziona le interfacce vicino al modello posseduto. K. Evita cicli. L. Aggiorna adapter storage e Drive. M. Aggiungi test per ogni package nuovo. N. Aggiorna ownership. O. Verifica limiti directory. P. Gofmt. Q. Test domain/assets. R. Test completi. S. Vet/build. T. Archcheck. U. Riconta file e dipendenze. V. Diff. W. Architecture docs. X. Rebase. Y. Commit/push. Z. Verifica remoto.

## Done
- [ ] Package asset sotto 30 file.
- [ ] Nessun package nuovo sopra soglia.
- [ ] Nessun alias compatibile.
- [ ] Nessun ciclo import.
