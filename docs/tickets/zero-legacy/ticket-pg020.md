# PG-020 — AppDeps tipizzato e teardown unico

**Branch:** `codex/pg-020-appdeps-types`

## Checklist A–Z
A. Sync main. B. Crea solo la branch indicata. C. Cerca slot generici, Cleanup e cast. D. Leggi i consumer. E. Definisci RouteRegistrar. F. Definisci il contratto health minimo. G. Valuta ReadyChecker. H. Migra costruttori. I. Migra server wiring. J. Migra fake e test. K. Elimina il campo Cleanup compatibile. L. Usa Lifecycle.Stop come unico teardown. M. Aggiungi assertion compile-time. N. Testa startup. O. Testa shutdown idempotente. P. Gofmt. Q. Test app/API. R. Test completi. S. Vet e build. T. Archcheck ratchet. U. Ripeti baseline. V. Controlla diff. W. Aggiorna solo docs canoniche. X. Rebase e ritesta. Y. Commit e push. Z. Verifica log, status e check.

## Done
- [ ] AppDeps senza slot generici.
- [ ] Nessun cast compensativo.
- [ ] Lifecycle unico teardown.
- [ ] Test e gate verdi.
