# PG-024 — Route script duplicate

**Branch:** `codex/pg-024-script-routes`

## Checklist A–Z
A. Sync main. B. Crea solo la branch. C. Inventaria metodo/path/handler. D. Identifica i consumer interni. E. Scegli i path canonici esistenti. F. Aggiorna consumer in-repo. G. Rimuovi route duplicate. H. Rimuovi wrapper handler. I. Rimuovi flag dedicati ai vecchi path. J. Aggiorna registry route. K. Aggiungi test 404 sui path rimossi. L. Mantieni test funzionali sui path canonici. M. Aggiorna docs. N. Cerca zero vecchi path. O. Non aggiungere nuove versioni route. P. Gofmt. Q. Test script API/application. R. Test completi. S. Vet/build. T. Archcheck. U. Ripeti inventario. V. Diff. W. Docs canoniche. X. Rebase. Y. Commit/push. Z. Verifica remoto.

## Done
- [ ] Un solo path per operazione.
- [ ] Nessun redirect o delega legacy.
- [ ] Wrapper e flag orfani rimossi.
- [ ] Test route verdi.
