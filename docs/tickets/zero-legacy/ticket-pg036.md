# PG-036 — Normalizzare ownership del dispatcher outbox

**Branch:** `codex/pg-036-outbox-dispatcher`

## Checklist A–Z
A. Sync main. B. Crea solo la branch. C. Cerca tipi Dispatcher, import e commenti removed. D. Distingui package storico e package attivo. E. Definisci l’owner canonico. F. Mappa metodi dispatch/enqueue/publish. G. Definisci un port minimo se cross-capability. H. Migra i service application. I. Costruisci una sola istanza in app. J. Elimina import vecchi. K. Elimina wrapper pass-through. L. Correggi commenti contraddittori. M. Aggiungi assertion compile-time. N. Testa delivery e failure. O. Cerca un solo tipo canonico. P. Gofmt. Q. Test outbox/assets. R. Test completi. S. Vet/build. T. Archcheck. U. Ripeti ricerca. V. Diff. W. Ownership docs. X. Rebase. Y. Commit/push. Z. Verifica remoto.

## Done
- [ ] Un solo owner dispatcher.
- [ ] Nessun path o commento fantasma.
- [ ] Una sola istanza runtime.
- [ ] Test verdi.
