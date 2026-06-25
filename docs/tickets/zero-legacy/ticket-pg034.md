# PG-034 — Eliminare il servizio Qdrant fittizio

**Branch:** `codex/pg-034-qdrant-capability`

## Decisione
Se esiste un adapter reale in-repo, collegare tutti i consumer a quello. Altrimenti rimuovere capability, config, startup step, health check e route. Non lasciare un servizio che restituisce sempre unavailable.

## Checklist A–Z
A. Sync main. B. Crea solo la branch. C. Cerca client, stub, route e config. D. Mappa consumer required/optional. E. Cerca implementazione reale. F. Applica il decision tree. G. Collega l’adapter reale oppure rimuovi la capability. H. Rimuovi il servizio fittizio. I. Aggiorna startup. J. Aggiorna health. K. Aggiorna config e Compose. L. Rimuovi port orfani. M. Aggiorna test integration o route-not-mounted. N. Aggiorna docs. O. Cerca zero messaggi stub. P. Gofmt. Q. Test Qdrant/app. R. Test completi. S. Vet/build. T. Archcheck. U. Ripeti baseline. V. Diff. W. Docs canoniche. X. Rebase. Y. Commit/push. Z. Verifica remoto.

## Done
- [ ] Feature interamente reale o interamente rimossa.
- [ ] Nessun servizio fittizio.
- [ ] Nessuna config o route orfana.
- [ ] Test verdi.
