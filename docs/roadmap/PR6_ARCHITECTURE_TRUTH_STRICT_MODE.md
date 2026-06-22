# PR6 — Repository truth e strict architecture mode

## Obiettivo

Rendere coerenti codice, migration tracker, baseline, guardrail e documentazione, quindi introdurre una modalità `archcheck --strict` che rifiuti qualsiasi debito architetturale residuo non esplicitamente ammesso.

Questa PR chiude le contraddizioni attuali tra:

- `architecture/migration.yaml`;
- `scripts/archcheck/baseline.json`;
- `scripts/ci-architectural-checks.sh`;
- `docs/roadmap/*.md`;
- struttura reale del repository.

Il risultato deve impedire falsi verdi dovuti a baseline stale, directory legacy non protette o documentazione che dichiara ancora aperto lavoro già completato.

## Branch e commit

Branch:

```text
codex/architecture-truth-strict-mode
```

Sequenza suggerita:

```text
docs(architecture): snapshot repository truth
fix(architecture): align migration tracker with main
feat(archcheck): add strict mode
fix(ci): enforce strict architecture gate
chore(architecture): remove stale aliases wrappers and exceptions
```

## Scope consentito

```text
architecture/migration.yaml
architecture/ownership.yaml
scripts/archcheck/**
scripts/ci-architectural-checks.sh
scripts/wave15_exit_gate.sh
.github/workflows/ci.yml
docs/roadmap/**
docs/migration-maps/**
docs/POST_CASCADE_OPERATIONAL_READINESS.md
REFACTOR_COMPLETE.md
AGENTS.md
README.md
```

Codice Go production può essere modificato soltanto per rimuovere alias, wrapper o violazioni identificate dal gate strict. Non aggiungere nuove feature.

## Fuori scope

- nuove route;
- nuovi job;
- refactor di business logic;
- modifica schema database;
- cambio provider;
- migrazione PostgreSQL;
- load test;
- modifiche ai payload.

## Stato iniziale da correggere

Problemi noti:

1. `architecture/migration.yaml` mantiene Wave 10 come parziale anche dopo migrazione Qdrant/storage.
2. Wave 13 dichiara ancora numerosi file sotto `internal/media`; il dato deve essere ricalcolato.
3. `ci-architectural-checks.sh` ha `LEGACY_DIRS=()` e quindi il controllo legacy è un no-op.
4. `archcheck` supporta `--update` ma non `--strict`.
5. La baseline contiene alias, wrapper e violazioni tollerate.
6. Alcuni documenti storici sono ancora presentati come fonte operativa.
7. I controlli “tracked” non falliscono anche quando il target dichiarato è zero.

## Fase 0 — Snapshot verificato

Eseguire su `origin/main` pulito:

```bash
git fetch origin
git checkout main
git pull --ff-only origin main
find internal -type d | sort > /tmp/pipelinegen-internal-dirs.txt
find internal -type f -name '*.go' | sort > /tmp/pipelinegen-go-files.txt
rg 'internal/media' --type go > /tmp/pipelinegen-internal-media-refs.txt || true
rg '^\s*type\s+\w+\s*=' internal pkg --type go > /tmp/pipelinegen-aliases.txt || true
rg 't\.Skip\(' --type go > /tmp/pipelinegen-skips.txt || true
```

Salvare nel corpo della PR:

- numero directory;
- numero file Go;
- numero import attivi verso `internal/media`;
- numero alias;
- numero wrapper;
- conteggio violazioni per regola archcheck;
- elenco directory API ancora legacy;
- elenco package YouTube root.

Checklist:

- [ ] dati ottenuti da file reali, non commit message;
- [ ] commenti separati da import attivi;
- [ ] directory vuote separate da directory production;
- [ ] test e fixture esclusi dai conteggi production dove appropriato.

## Fase 1 — Riallineare `architecture/migration.yaml`

Per ogni wave:

- [ ] verificare `from` realmente presente;
- [ ] verificare `to` realmente presente;
- [ ] ricalcolare `active_files_remaining`;
- [ ] ricalcolare `active_importers_remaining`;
- [ ] correggere `status`;
- [ ] correggere `blocked_by`;
- [ ] correggere `completed` e `pending`;
- [ ] eliminare riferimenti a file non esistenti;
- [ ] aggiornare exit gate con comandi eseguibili.

Regole di stato:

```text
pending      = nessun lavoro iniziato
in_progress  = codice residuo verificato
blocked      = dipendenza nominata e reale
done         = exit gate eseguito
verified     = exit gate rieseguito su main
```

Aggiornamenti minimi richiesti:

- Wave 10 deve riflettere migrazione Qdrant/storage realmente completata;
- Wave 13 deve contenere conteggi reali di `internal/media`;
- Wave 14 deve riflettere directory API reali;
- Wave 15 deve riflettere stato reale di composition root;
- Wave 16 resta pending finché strict mode non passa;
- Wave 17 resta pending fino alla certificazione finale.

## Fase 2 — Riallineare ownership

Per ogni package attivo verificare un solo owner:

| Categoria | Owner canonico |
|---|---|
| dominio asset | `internal/domain/asset` |
| use case asset | `internal/application/assets` |
| provider | `internal/application/assets/providers` |
| processi/media | `internal/infrastructure/media` o package focused |
| filesystem | `internal/infrastructure/files` |
| database | `internal/infrastructure/database/sqlite` |
| API | `internal/api/<capability>` |
| wiring | `internal/app` |

TODO:

- [ ] rimuovere ownership duplicate;
- [ ] eliminare path non esistenti;
- [ ] associare ogni eccezione a una wave aperta;
- [ ] vietare root generici come owner finale.

## Fase 3 — Progettare `archcheck --strict`

CLI richiesta:

```bash
go run ./scripts/archcheck
go run ./scripts/archcheck --update
go run ./scripts/archcheck --strict
```

Semantica:

### Modalità default

- ratchet rispetto alla baseline;
- nessuna nuova violazione;
- utile durante migrazioni in corso.

### Modalità update

- aggiorna baseline;
- consentita soltanto con diff revisionato;
- non deve essere eseguita automaticamente dalla CI.

### Modalità strict

Deve fallire se esiste almeno uno dei seguenti:

```text
forbidden_internal_roots > 0
legacy_imports > 0
type_aliases > 0
compatibility_wrappers > 0
pass_through_services > 0
go_files_at_infrastructure_root > 0
sql_outside_infrastructure_database > 0
gin_outside_api > 0
os_getenv_outside_config_app > 0
os_exec_outside_infrastructure_process > 0
map_string_any_in_domain_application > 0
provider_switch_outside_registry > 0
duplicate_blocks_above_threshold > 0
architecture_exceptions > 0
```

Il flag strict non deve leggere soglie permissive dalla baseline.

## Fase 4 — Implementazione strict

TODO:

- [ ] aggiungere flag `strict`;
- [ ] separare analyzer da rendering output;
- [ ] produrre conteggi deterministici;
- [ ] ordinare risultati;
- [ ] distinguere commenti da codice via AST quando necessario;
- [ ] non usare solo regex per alias e wrapper critici;
- [ ] aggiungere exit code documentati;
- [ ] aggiungere test unitari del CLI;
- [ ] aggiungere fixture con violazione per ogni regola;
- [ ] testare baseline assente;
- [ ] testare `--strict --update` come combinazione invalida.

Exit code consigliati:

```text
0 = pass
1 = violazioni trovate
2 = configurazione o argomenti invalidi
3 = errore analyzer o IO
```

## Fase 5 — Legacy directories guard

`LEGACY_DIRS=()` non può restare vuoto se esistono directory ancora destinate alla rimozione.

TODO:

- [ ] generare elenco da `architecture/migration.yaml` o file canonico;
- [ ] evitare doppia lista manuale;
- [ ] vietare nuovi file production nelle directory in migrazione;
- [ ] consentire rimozioni e rename verso target;
- [ ] consentire test soltanto quando coprono migrazione;
- [ ] testare add, rename interno, rename in ingresso e rename in uscita;
- [ ] fallire CI su nuovo file legacy.

Preferenza:

```text
migration.yaml → parser guardrail → CI
```

Non mantenere una seconda allowlist hardcoded nello script.

## Fase 6 — Promuovere i check tracked

Analizzare i check attualmente solo informativi:

- `map[string]any` in API;
- import root infrastructure;
- transport-layer boundary;
- dimensioni directory;
- eventuali provider switch.

Per ciascuno scegliere:

1. chiudere il debito e renderlo hard fail;
2. mantenerlo tracked con issue, owner e data limite;
3. rimuoverlo se non misura un rischio reale.

Nessun check può restare indefinitamente “tracked” senza owner.

## Fase 7 — Eliminare alias e wrapper residui

Comandi:

```bash
rg '^\s*type\s+\w+\s*=' internal pkg --type go
rg 'Deprecated:|compat|legacy|wrapper|pass-through' internal pkg --type go
```

Per ogni risultato:

- [ ] verificare se è alias standard legittimo o compatibilità;
- [ ] migrare caller al tipo canonico;
- [ ] eliminare alias;
- [ ] eliminare wrapper senza logica;
- [ ] non sostituire con un altro wrapper;
- [ ] aggiornare test.

## Fase 8 — Riallineare documentazione

Aggiornare:

```text
docs/roadmap/README.md
docs/roadmap/PR5_*.md
docs/roadmap/PR6_*.md
architecture/migration.yaml
docs/migration-maps/*.md
AGENTS.md
README.md
```

Archiviare chiaramente:

```text
REFACTOR_COMPLETE.md
docs/POST_CASCADE_OPERATIONAL_READINESS.md
docs/roadmap/PR0_* fino a PR4_*
```

Non eliminare lo storico utile, ma aggiungere intestazione:

```text
STATUS: HISTORICAL — non usare come source of truth operativa
```

## Fase 9 — CI strict

Aggiungere alla CI:

```yaml
- name: Run strict architecture checks
  run: go run ./scripts/archcheck --strict
```

Regole:

- [ ] eseguito su push e pull request verso main;
- [ ] nessun `continue-on-error`;
- [ ] nessun auto-update baseline;
- [ ] output leggibile;
- [ ] artefatto allegato se l'output è lungo;
- [ ] branch protection richiede il check.

## Test obbligatori

```bash
go test ./scripts/archcheck/...
go run ./scripts/archcheck
go run ./scripts/archcheck --strict
bash scripts/ci-architectural-checks.sh
go vet ./...
go build ./...
```

Test negativi:

- aggiungere temporaneamente alias → strict fallisce;
- aggiungere import SQL fuori infra DB → strict fallisce;
- aggiungere file in directory legacy → CI fallisce;
- aggiungere wrapper → strict fallisce;
- aggiungere provider switch fuori registry → strict fallisce.

Rimuovere sempre le fixture temporanee prima del commit, salvo fixture test dedicate.

## Exit gate finale

PR6 è `done` quando:

- [ ] migration tracker descrive il repository reale;
- [ ] ownership descrive owner reali;
- [ ] baseline non contiene directory eliminate;
- [ ] guardrail legacy non è un no-op;
- [ ] `archcheck --strict` esiste;
- [ ] strict non usa baseline permissiva;
- [ ] strict ha test positivi e negativi;
- [ ] CI esegue strict;
- [ ] zero alias compatibility;
- [ ] zero wrapper pass-through;
- [ ] documenti storici marcati come tali;
- [ ] `go vet ./...` verde;
- [ ] `go build ./...` verde;
- [ ] CI verde;
- [ ] verifica rieseguita su main.

## Rollback

Se strict blocca il repository per debito preesistente:

1. non disabilitare strict;
2. non gonfiare baseline;
3. dividere il debito in PR più piccole;
4. mantenere la PR strict aperta finché i target sono zero;
5. usare modalità default durante le PR preparatorie;
6. abilitare strict come required check soltanto quando passa su main.

Il rollback della PR deve ripristinare codice e documenti insieme. Non lasciare CI e tracker con semantiche differenti.
