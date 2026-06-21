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

- [ ] Eseguire `go list ./...` e salvare l'elenco temporaneamente fuori dal repository.
- [ ] Eseguire `find internal -type d | sort` e confrontarlo con `scripts/archcheck/baseline.json`.
- [ ] Cercare i namespace eliminati:

```bash
rg 'internal/(core|assets|artifacts|sources|upload|domain/media|application/scriptflow)' --type go
```

- [ ] Classificare ogni risultato come import reale, commento storico o documentazione.
- [ ] Verificare quali sottodirectory di `internal/media` esistono ancora realmente.
- [ ] Verificare quali directory API legacy esistono ancora realmente.

**Accettazione PR0.0**

- esiste una lista verificata di directory presenti;
- nessuno stato viene dedotto solo dai commit message;
- ogni wave successiva usa questa fotografia come riferimento.

### PR0.1 — Correggere `architecture/migration.yaml`

- [ ] Marcare Wave 4A completata se `internal/assets` non esiste e gli import sono zero.
- [ ] Marcare Wave 4C completata se `internal/core` e `internal/domain/media` non esistono.
- [ ] Marcare Wave 6 completata se `internal/application/scriptflow` e `internal/scripts` non hanno import reali.
- [ ] Marcare Wave 7 completata se `internal/artifacts` e `internal/assets` non esistono.
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

- [ ] Eseguire il comando canonico di aggiornamento:

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

- [ ] Correggere `docs/migration-maps/README.md` per distinguere:
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
