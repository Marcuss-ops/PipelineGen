# PR0 — Repository truth

## Obiettivo

Rendere coerenti codice, tracker architetturali, baseline e documentazione. Questa PR non modifica il comportamento runtime.

## Perimetro

File ammessi:

- `architecture/migration.yaml`
- `architecture/ownership.yaml`
- `scripts/archcheck/baseline.json`
- `docs/migration-maps/*.md`
- `docs/roadmap/*.md`
- `README.md`
- `AGENTS.md`
- eventuali documenti follow-up già risolti

File esclusi:

- codice Go di produzione;
- migration SQL;
- route HTTP;
- configurazione runtime.

## Checklist operativa

### PR0.0 — Fotografare lo stato reale

- [x] Eseguire `go list ./...` e salvare l'elenco temporaneamente fuori dal repository. *(Sostituito in PR #26+ da `find internal -type d` + `find internal -name '*.go'`; snapshot in `/tmp/pr0-snapshot/internal-dirs.txt` + `eliminated-refs.txt`. Vedi sezione Condotto.)*
- [x] Eseguire `find internal -type d | sort` e confrontarlo con `scripts/archcheck/baseline.json`. *(119 sotto-dir in `internal/`; baseline.json hash `f4bba57f…` invariato dopo `--update` = già accurato.)*
- [x] Cercare i namespace eliminati:

```bash
rg 'internal/(core|assets|artifacts|sources|upload|domain/media|application/scriptflow)' --type go
```

- [x] Classificare ogni risultato come import reale, commento storico o documentazione. *(Vedi `/tmp/pr0-snapshot/eliminated-refs.txt`.)*
- [x] Verificare quali sottodirectory di `internal/media` esistono ancora realmente. *(19 sotto-dir + `deletion.go` = 102 file `.go` attivi.)*
- [x] Verificare quali directory API legacy esistono ancora realmente. *(16 sotto-dir in `internal/api/` — tutti e 7 i "legacy" PR3 ancora presenti: drive, realtime, searchqueries, sources, fullimages, workers, script.)*

**Accettazione PR0.0**

- esiste una lista verificata di directory presenti;
- nessuno stato viene dedotto solo dai commit message;
- ogni wave successiva usa questa fotografia come riferimento.

### PR0.1 — Correggere `architecture/migration.yaml`

- [x] *(Parziale)* Marcare Wave 4A/4C/6/7 già `done` — verificato che i path sono ancora rimossi. **Da fare in PR-round successivo**: aggiungere link Wave → PR per rendere la cross-link matrix navigabile.
- [x] *(Parziale)* Wave 10/11/12/13 riflettono già i conteggi `active_files_remaining` aggiornati; nessuna rimozione file-level necessaria. **Da fare successivamente**: refresh dopo PR1+PR2.
- [x] *(Parziale)* Wave 14 resta `in_progress` finché esistono i 7 path (verificato in PR0.0).
- [x] Wave 15 resta `in_progress` (services struct già rimosso da PR4d-final — verificato).
- [ ] Separare chiaramente “directory spostata” da “layering completato” per YouTube e Artlist.
- [ ] Aggiornare Wave 10–13 sulla base delle sottodirectory `internal/media` ancora presenti.
- [ ] Lasciare Wave 14 `in_progress` finché esistono `api/drive`, `api/realtime`, `api/searchqueries`, `api/sources`, `api/fullimages`, `api/workers` o `api/script`.
- [ ] Lasciare Wave 15 `pending` finché esiste `type services struct`.
- [ ] Aggiornare i campi `completed`, `pending`, `blocked_by` ed `exit_gate` con path reali.
- [ ] Rimuovere note che citano file o package non più esistenti.

**Accettazione PR0.1**

```bash
rg 'status: pending|status: in_progress' architecture/migration.yaml
```

Ogni stato restante deve corrispondere a codice realmente presente.

### PR0.2 — Rigenerare la baseline architetturale

- [x] Eseguire il comando canonico di aggiornamento (`go run ./scripts/archcheck --update`). *(Eseguito in PR #26+1: SHA-256 di `scripts/archcheck/baseline.json` invariato = baseline già accurata al commit corrente.)*

```bash
go run ./scripts/archcheck --update
```

- [ ] Verificare che la nuova baseline non contenga directory eliminate.
- [ ] Verificare che la baseline contenga tutte le directory nuove realmente presenti.
- [ ] Controllare il diff delle sezioni `directories`, `aliases`, `wrappers` e violazioni.
- [ ] Non rimuovere violazioni dalla baseline se il codice che le genera esiste ancora.
- [ ] Eseguire nuovamente `go run ./scripts/archcheck` senza `--update`.

**Accettazione PR0.2**

- `archcheck` passa baseline-on-baseline;
- nessun path inesistente rimane in `directories`;
- nessuna violazione reale viene nascosta manualmente.

### PR0.3 — Allineare le migration map

- [x] *(Parziale)* Aggiunto disclaimer a `docs/migration-maps/README.md` che distingue trasferimento fisico da layering chiuso. **Da fare successivamente** (issue esplicita del code-reviewer PR #26+1): aggiungere footer disclaimer anche ai singoli file (`internal-media.md`, `internal-sources.md`, `internal-assets.md`, `internal-core.md`, `internal-artifacts.md`, `internal-upload.md`) i cui body contengono ancora "Status: pending" sotto un'intestazione `✅ COMPLETED`.
  - directory eliminata;
  - package fisicamente spostato;
  - layering ancora da completare.
- [ ] Correggere `internal-media.md`: non dichiarare eliminato l'intero namespace se restano sottopackage attivi.
- [ ] Correggere `internal-sources.md`: indicare che `internal/sources` è eliminato, ma che YouTube/Artlist richiedono ancora estrazione degli adapter concreti.
- [ ] Correggere `internal-assets.md`, `internal-core.md`, `internal-artifacts.md` e `internal-upload.md` con path finali reali.
- [ ] Mantenere le sezioni storiche soltanto se etichettate chiaramente come archivio.

**Accettazione PR0.3**

Nessun documento usa “all complete” per indicare lavoro architetturale ancora aperto.

### PR0.4 — Pulire README e AGENTS

- [ ] Verificare che il README punti a `ARCHITECTURE.md` nel path corretto.
- [ ] Aggiungere il link a `docs/roadmap/README.md` nella sezione documentazione.
- [ ] Verificare che la struttura del progetto nel README contenga solo directory reali.
- [ ] Rimuovere da AGENTS regole riferite a namespace non più presenti.
- [ ] Conservare le regole Git generali, ma non duplicarle nei documenti PR0–PR4.
- [ ] Correggere separatori Markdown malformati e intestazioni concatenate.

### PR0.5 — Eliminare follow-up risolti

- [ ] Cercare `docs/followups/` e classificare ogni file come aperto o risolto.
- [ ] Eliminare o archiviare solo i follow-up il cui exit gate è già verificato.
- [ ] Non creare nuovi follow-up per problemi inclusi nelle checklist PR1–PR4.
- [ ] Aggiornare eventuali link che puntano a file eliminati.

### PR0.6 — Validazione finale

- [ ] Eseguire:

```bash
go run ./scripts/archcheck
bash scripts/ci-architectural-checks.sh
go vet ./...
go build ./...
```

- [ ] Controllare link Markdown locali nei file modificati.
- [ ] Verificare che il diff non contenga codice Go o SQL.

## Exit gate finale

PR0 è completata quando:

- roadmap, migration map e baseline descrivono lo stesso albero;
- nessun namespace eliminato appare come attivo;
- nessun namespace attivo appare come eliminato;
- README collega questa roadmap;
- i controlli architetturali e la build sono verdi.
