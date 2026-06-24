# PG-042 — Tipizzare scripts/pipeline_usecase.go

**Branch:** `codex/pg-042-script-pipeline`
**Dipendenze:** PG-017, PG-033

## Checklist A–Z
A. Sync main. B. Crea solo la branch. C. Cerca jobsSvc non tipizzato e fasi aggregate. D. Mappa il flusso generate-from-clips. E. Definisci contratto job canonico. F. Definisci collaboratori per hydrate, pack, plan, source, memory, write, scenes e docs. G. Mantieni ordine dei nove step. H. Mantieni due chiamate LLM canoniche. I. Mantieni regola una clip uguale una scena. J. Mantieni fingerprint e force refresh. K. Elimina cast runtime. L. Aggiorna composition. M. Aggiungi test per ogni fase. N. Aggiungi test end-to-end del use case. O. Porta file sotto 300 righe. P. Gofmt. Q. Test scripts pipeline. R. Test completi. S. Vet/build. T. Archcheck. U. Cerca zero slot non tipizzati. V. Diff. W. Docs pipeline. X. Rebase. Y. Commit/push. Z. Verifica remoto.

## Done
- [ ] Job service tipizzato.
- [ ] Nove step preservati.
- [ ] Nessun cast runtime.
- [ ] File sotto 300 righe e test verdi.
