# PR0 — Repository truth residua

## Obiettivo

Rendere coerenti codice, tracker architetturali, baseline e documentazione. Questo documento elenca soltanto il lavoro ancora aperto e non modifica il comportamento runtime.

## Stato verificato

La fotografia iniziale, il confronto directory/baseline e la classificazione dei namespace eliminati sono già stati eseguiti. Restano incoerenze tra documentazione, migration tracker e codice corrente.

## Checklist residua

### PR0.0 — Correggere la documentazione canonica

- [ ] Riscrivere `ARCHITECTURE.md` sulla struttura reale attuale.
- [ ] Rimuovere riferimenti attivi a `CoreDeps`, `services`, `internal/module`, `internal/service`, `internal/sources` e `internal/upload` quando non esistono più.
- [ ] Distinguere chiaramente directory eliminate, spostamento fisico e layering ancora aperto.
- [ ] Aggiornare il diagramma del composition root con `ComposeRoot`, bundle, registry e lifecycle reali.
- [ ] Verificare che README, AGENTS e documenti architetturali non forniscano istruzioni contraddittorie.

### PR0.1 — Allineare `architecture/migration.yaml`

- [ ] Correggere gli stati Wave 0, 2, 10, 11, 12, 13, 14, 15, 16 e 17 usando esclusivamente path e simboli realmente presenti.
- [ ] Correggere il riferimento a `internal/sources`: il namespace fisico è eliminato, mentre il cutover YouTube/Artlist resta aperto nei package application/infrastructure.
- [ ] Aggiornare i conteggi `active_files_remaining` e `sub_directories_remaining` di `internal/media` con un comando riproducibile.
- [ ] Marcare Wave 15 coerentemente con la rimozione di `services` e `CoreDeps`, lasciando aperti solo alias, lifecycle e moduli capability-owned residui.
- [ ] Aggiornare `blocked_by`, `completed`, `pending` ed `exit_gate` senza descrizioni storiche ormai superate.
- [ ] Aggiungere collegamenti chiari tra Wave 12–17 e i documenti PR1–PR5/SOT.

### PR0.2 — Validare la baseline architetturale

- [ ] Eseguire `go run ./scripts/archcheck --update` sulla revisione definitiva della PR.
- [ ] Controllare il diff di `directories`, `aliases`, `wrappers` e `violations`.
- [ ] Rimuovere dalla baseline soltanto violazioni che non esistono più nel codice.
- [ ] Verificare che non compaiano directory eliminate e che tutte le directory reali siano presenti.
- [ ] Eseguire nuovamente `go run ./scripts/archcheck` senza `--update`.
- [ ] Documentare temporaneamente le eccezioni ancora attive con owner e blocco roadmap; nessuna eccezione anonima.

### PR0.3 — Pulire migration map e follow-up

- [ ] Rendere coerenti `docs/migration-maps/internal-media.md` e il conteggio reale dei package residui.
- [ ] Rendere coerente `internal-sources.md`: namespace eliminato, separazione degli adapter ancora incompleta.
- [ ] Verificare i path finali in `internal-assets.md`, `internal-core.md`, `internal-artifacts.md` e `internal-upload.md`.
- [ ] Spostare le sezioni storiche in una parte esplicitamente marcata come archivio.
- [ ] Classificare ogni file in `docs/followups/` come aperto, risolto o storico.
- [ ] Eliminare follow-up risolti e aggiornare i link locali.
- [ ] Conservare il problema della migration 053 soltanto se ancora riproducibile, con owner ed exit gate reali.

### PR0.4 — Pulire README e AGENTS

- [ ] Aggiornare il README a PR0–PR5 e collegare i guardrail single-source-of-truth.
- [ ] Verificare che i comandi build/run usino gli entrypoint realmente presenti.
- [ ] Verificare che la struttura del progetto contenga soltanto directory reali.
- [ ] Eliminare da AGENTS regole riferite a namespace eliminati o sostituirle con gli owner correnti.
- [ ] Assicurare che AGENTS non autorizzi alias, wrapper o fallback vietati dalla roadmap corrente.

### PR0.5 — Validazione finale

- [ ] Eseguire:

```bash
go run ./scripts/archcheck
bash scripts/ci-architectural-checks.sh
go vet ./...
go build ./...
```

- [ ] Controllare tutti i link Markdown locali modificati.
- [ ] Verificare che il diff PR0 non contenga codice Go di produzione, SQL o cambiamenti runtime.
- [ ] Verificare che ogni stato `pending` o `in_progress` del tracker corrisponda a codice realmente presente.

## Exit gate

PR0 è chiusa quando roadmap, migration tracker, baseline, README, AGENTS e `ARCHITECTURE.md` descrivono lo stesso repository e tutti i controlli documentali/architetturali sono verdi.
