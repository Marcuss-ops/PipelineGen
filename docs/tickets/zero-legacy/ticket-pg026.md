# PG-026 — Ritirare comandi admin migratori conclusi

**Branch:** `codex/pg-026-admin-retirement`

## Checklist A–Z
A. Sync main. B. Crea solo la branch. C. Inventaria comandi backfill/migrate/unify/reset. D. Mappa dati e ambienti toccati. E. Definisci query zero-residui. F. Esegui dry-run documentato. G. Conferma tutti gli ambienti applicabili. H. Elimina comando concluso. I. Elimina registrazione CLI. J. Elimina help e test dedicati. K. Elimina helper orfani. L. Mantieni solo comandi con owner e criterio exit. M. Aggiorna help admin. N. Aggiungi test della command list. O. Non cancellare migration SQL storiche. P. Gofmt. Q. Test admin. R. Test completi. S. Vet/build. T. Archcheck. U. Ricerca zero simboli ritirati. V. Diff. W. Runbook canonico. X. Rebase. Y. Commit/push. Z. Verifica remoto.

## Done
- [ ] Ogni comando ha decisione verificabile.
- [ ] Comandi conclusi rimossi completamente.
- [ ] Help e registry coerenti.
- [ ] Nessuna migration storica cancellata.
