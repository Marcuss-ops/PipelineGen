# Runbook Wave 19 — Ticket PR4 + PR5 (Direction Hardening + db/sql shrinkage)

Status: pianificato

Data snapshot: 2026-06-26

Repository: `Marcuss-ops/VeloxEditing`

Parent wave: **Wave 19 — Dependency Direction & consumer-owned ports** (June 2026, status `done` + `verified_zero: true`). I due seguenti ticket sono i figli pendenti dichiarati in `architecture/current.yaml::Wave 19::pr4_pr5_followups` (`items[PR4]` + `items[PR5]`, entrambi `verified_zero: false`).

Obiettivo: promuovere le regole `application→infrastructure` e `cross_capability_import` da observation-only a gate hard-fail-closed (ticket **W19-PR4-001**) e ridurre il perimetro `database/sql` in `internal/application/` + `internal/domain/` verso zero via growth-audit monotono (ticket **W19-PR5-001**, chunk db/sql shrinkage soltanto).

---

## 1. Regole di esecuzione

1. Un ticket corrisponde a un solo problema e a una sola PR.
2. Non mescolare refactor, nuove feature e test non correlati nello stesso ticket.
3. Prima di iniziare un ticket, partire da `main` aggiornato e cercare il codice già esistente (entrambi i file `docs/migrations/{application-infrastructure,cross-capability}-imports-allowlist.txt` sono già stati seeded; non duplicarli).
4. Ogni nuova capacità deve entrare nel componente canonico già proprietario del dominio.
5. I test devono esercitare il percorso reale quando il ticket riguarda ratchet, gate, o migrazione di un enforcement da observation a hard-fail.
6. Nessun ticket è chiuso soltanto perché il codice compila.
7. Ogni chiusura deve produrre evidenze verificabili: counters `Checks`, output JSON, edge-list dump, report ratchet before/after.
8. PR5 hardening commit (`1b2da8f1` Codebuff, "Wave 19 PR5 — 4 hardening fixes + capability-discovery counter propagation fix") è GIÀ chiuso upstream; il presente W19-PR5-001 copre SOLO il chunk Path B `databaseSQLLegacyBaseline` shrinkage. NON copre la riga hardening.

---

## 2. Stati dei ticket

- `OPEN`: non iniziato.
- `IN_PROGRESS`: implementazione in corso.
- `BLOCKED`: dipendenza fuori controllo del team.
- `READY_FOR_REVIEW`: codice e test completati, exit codes verificati.
- `DONE`: merge effettuato, evidenze archiviate, `verified_zero: true` aggiornato in YAML.

---

## 3. Definition of Done globale

I due ticket di Wave 19 (PR4 + PR5) sono `DONE` solo quando:

- `scripts/archcheck/main.go` continua a passare `gofmt -w`, `go build ./...`, `go vet ./...`.
- `scripts/ci-architectural-checks.sh` (focused mode) termina con exit `0` e violation list vuota per le famiglie `application_to_infrastructure_*` + `cross_capability_*`.
- `scripts/archcheck --ratchet` termina con exit `0` e `passed = true`.
- `migration_yaml_done_waves_with_verified_zero_true` non regredisce tra i due commit (i ticket non cambiano lo status top-level Wave 19).
- `architecture/current.yaml::Wave 19::pr4_pr5_followups.items[PR4|P5].verified_zero` può essere flippato a `true` in un commit separato (questo è l'extra-credit post-ticket; il ticket stesso chiude quando l'implementazione è su `main`).

---

# Ticket W19-PR4-001 — Hard gate promotion: allowlist subtractSet per application→infrastructure + cross_capability_import

**Priorità:** P0
**Stato:** OPEN
**Dipendenze:** nessuna (Wave 19 PR2-1 grafo completo + PR2-3 filesystem capability discovery + PR5 hardening commit `1b2da8f1` sono già `DONE` upstream; i due allowlist file sono già stati seeded con i 77 + 8 valori reali — gate al boot dello strumento non fallisce).

## Problema

Le regole `application_to_infrastructure` + `cross_capability_import` sono oggi `observation-only` (Wave 19 PR1). Il grafo completo (`Edges["application_to_infrastructure"]` + `Edges["cross_capability_import"]`) è emesso correttamente e `discoverApplicationCapabilities()` è filesystem-driven (PR2-3), ma nessuno dei due blocchi diventa un violation source. Un futuro commit che reintroduce un import piatto `internal/application/<cap>/foo.go -> internal/infrastructure/...` o `internal/application/<capA>/foo.go -> internal/application/<capB>/...` non viene rilevato da `scripts/archcheck`.

## Obiettivo

Promuovere le due regole da `observation-only` a hard gate fail-closed via `subtractSet(allowlist, actual)` come `checkAPIInfrastructureImports` (Wave 14 PR5, già operativo e validato). Comportamento atteso:

- **regression**: un file in `Edges["application_to_infrastructure"]` che non è nel `docs/migrations/application-infrastructure-imports-allowlist.txt` causa `exit 1` con messaggio `new application_to_infrastructure import not in allowlist: <file>`.
- **regression**: una coppia `<srcCap>-><importCap>` in `Checks["cross_capability_import_pairs"]` che non è nel `docs/migrations/cross-capability-imports-allowlist.txt` causa `exit 1` con messaggio `new cross_capability pair not in allowlist: <pair>`.
- **stale**: un'entry negli allowlist che non ha più un current match causa `exit 1` con messaggio `stale allowlist entry with no matching <family> import: <key>`.
- **simmetrico**: entrambe le direzioni del subtractSet sono violation sources (mirror del Wave 14 API allowlist precedent).

## Attività

- [ ] Verificare che `scripts/archcheck/main.go` referenzi correttamente entrambi gli allowlist file via `loadAllowlist("docs/migrations/application-infrastructure-imports-allowlist.txt")` e `loadAllowlist("docs/migrations/cross-capability-imports-allowlist.txt")`.
- [ ] Confermare che `checkApplicationToInfrastructure` emette `stats["violations"] = len(regressions ∪ stale)` (simmetrico) come fa `checkAPIInfrastructureImports`.
- [ ] Confermare che `checkCrossCapabilityImport` emette `stats["violations"] = len(regressions ∪ stale)` simmetricamente.
- [ ] Confermare che `runFocusedChecks` e `runRatchetChecks` propagano correttamente le violation strings nella `Report.Violations` (NON solo nei `Checks[key]` counters).
- [ ] Verificare che il focused mode (`go run ./scripts/archcheck`) termina con exit `0` al HEAD corrente (i due file sono già seeded, actual==allowed, quindi zero violations).
- [ ] Verificare che il ratchet mode (`go run ./scripts/archcheck --ratchet`) termina con exit `0` al HEAD corrente.
- [ ] Se al boot dello strumento uno dei due file manca, il gate fallisce con violazione `failed to load allowlist` (questo è il comportamento desiderato: file mancante = fail-fast, NON consistenza garantita da file vuoto).
- [ ] Rimettere a `verified_zero: false` qualora la promozione a gate hard-fail dovesse rompere CI; il flip a `true` avviene in un commit separato **post-ticket** quando l'operatore conferma che la regola è stabile su `main`.

## Criteri di accettazione

- [ ] `scripts/archcheck` (focused, default mode) termina con exit `0` dopo una run che NON ha modificato i due allowlist file al HEAD corrente.
- [ ] Aggiungere un nuovo file `internal/application/<cap>/<file>.go` con un qualsiasi import `"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/...` causa `exit 1` dal focused mode con almeno una `application_to_infrastructure_*` violation nella report JSON.
- [ ] Aggiungere una nuova coppia `<capA>-><capB>` di import cross-capabilities causa `exit 1` con almeno una `cross_capability_*` violation.
- [ ] Rimuovere un'entry grandfathered legittima (es. via la typed-port cascade W14 PR2-style) E non aggiornare il rispettivo allowlist file causa `exit 1` con messaggio `stale allowlist entry with no matching … import: <key>`.
- [ ] Nessun regression counter `application_to_infrastructure_allowlist_stale`/`cross_capability_allowlist_stale` diverso da `0` al HEAD baseline.
- [ ] `capability_discovery_failed = 0` (filesystem discovery ha successo).

## Test obbligatori

- [ ] Unit test (in `scripts/archcheck`) che legge i due allowlist file e verifica che `loadAllowlist` ritorni la entry attesa per ciascuno.
- [ ] Integration test che simula un nuovo file application→infra (es. creando `internal/application/test/foo.go` con un import `internal/infrastructure/test`) e verifica che la violation list della focused run contiene l'entry con il path canonico.
- [ ] Integration test che simula un allowlist stale (rimozione di un file actual dal allowlist) e verifica che la violation list contiene l'entry stale.
- [ ] Smoke test focused + ratchet (`go run ./scripts/archcheck` + `go run ./scripts/archcheck --ratchet`) — entrambi exit `0` al HEAD corrente.

## Evidenze richieste

- Counter blocks dalla report JSON (focused + ratchet) incollati nella descrizione del commit:
  - `Checks["application_to_infrastructure_imports"] == 0`
  - `Checks["application_to_infrastructure_imports_actual"] == 77`
  - `Checks["application_to_infrastructure_imports_allowed"] == 77`
  - `Checks["application_to_infrastructure_allowlist_stale"] == 0`
  - `Checks["cross_capability_imports"] == 0`
  - `Checks["cross_capability_imports_actual"] == 8`
  - `Checks["cross_capability_imports_allowed"] == 8`
  - `Checks["cross_capability_allowlist_stale"] == 0`
  - `Checks["capability_discovery_failed"] == 0`
- File di seed (commit-separato PR-track esistente):
  - `docs/migrations/application-infrastructure-imports-allowlist.txt` — 77 file paths.
  - `docs/migrations/cross-capability-imports-allowlist.txt` — 8 coppie.
- Cross-link finale: `architecture/current.yaml::Wave 19::pr4_pr5_followups.items[PR4].verified_zero: true` (commit post-ticket dell'operatore).

---

# Ticket W19-PR5-001 — database/sql inheritance shrinkage (Path B baseline-growth audit)

**Priorità:** P1
**Stato:** OPEN
**Dipendenze:** nessuna (è indipendente dal typed-port lift Path A, già documentato come `FollowWave19PR3TypedPortLift`; è possibile operare in parallelo con altri lavori Wave 16/17/18 ma SOLO dopo che W19-PR4-001 è `DONE`).

> **Scope esplicito (2026-06-26)**: PR5 hardening commit `1b2da8f1` (Codebuff, June 2026: "Wave 19 PR5 — 4 hardening fixes + capability-discovery counter propagation fix") è GIÀ chiuso upstream. Il presente **W19-PR5-001 copre SOLO** il chunk Path B `databaseSQLLegacyBaseline` shrinkage. **NON** copre:
>
> - Qualsiasi riga hardening già shippata in `1b2da8f1`.
> - La rimozione di ogni singolo file `database/sql` (Path A — typed-port lift; ticket separato `FollowWave19PR3TypedPortLift`).
> - La costruzione della libreria typed-port-adapter `pkg/dbutil/...` (Path A — scope separato).

## Problema

`scripts/archcheck/main.go::databaseSQLLegacyBaseline` enumera ~52 file che legittimamente importano `database/sql` (sqlite std types + driver adapters). Il ratchet `checkDatabaseSQLGate` fallisce già su ogni nuova aggiunta (`subtractSet(actual, baseline)` symmetric); la policy canonica vorrebbe però **ridurre** la lista nel tempo sostituendo i siti con typed-port-adapter, ma oggi non esiste alcun counter che governi la **discesa monotona** del `len(baseline)` — i siti possono aumentare silenziosamente (operator-acknowledged in Wave 19 PR3) senza contatore visibile. Risultato: l'obiettivo "Path B: ridurre `databaseSQLLegacyBaseline` verso zero tramite ack dell'operatore" non ha un KPI misurabile.

## Obiettivo

Introdurre un counter `Checks["database_sql_baseline_growth_since_seed"]` nel report JSON che mostri il delta `len(baseline_actual) − len(baseline_seedato)`. Combinato con un target di discesa esplicito, il counter diventa il KPI operativo con cui gli operatori tracciano il progresso verso zero.

Path B NON è enforcement. Path B è audit (misurazione + reportistica):

- L'aggiunta di una nuova entry a `databaseSQLLegacyBaseline` NON causa hard-fail (l'enforcement di crescita è un altro ticket; qui ci limitiamo al segnale).
- La rimozione di una entry esistente è un'azione che vogliamo ESLICITARE tramite removal-PR granulari.
- Il target di discesa è un'**iniziativa di ticket** (un removal-PR = un commit), non un blocco automatico.

## Attività

- [ ] Aggiungere `Checks["database_sql_baseline_growth_since_seed"]` al report JSON in `scripts/archcheck/main.go::runRatchetChecks`.
- [ ] Seedare il valore iniziale in modo deterministico: `baseline_growth_since_seed_init = len(databaseSQLLegacyBaseline)` snapshot al commit di apertura del ticket.
- [ ] Ad ogni successiva ratchet run, calcolare `current - init` ed emettere il valore nel counter.
- [ ] Rimuovere **almeno una** entry obsoleta da `databaseSQLLegacyBaseline` come **primo removal step** del Path B shrinkage (es. un file che è già stato migrato a typed-port e non importa più `database/sql`).
- [ ] Documentare nel commit message la entry rimossa + il motivo (es. "internal/domain/asset/store_helpers.go rimosso → migrato a typed-port-adapter in #XXX").
- [ ] Aprire un follow-up ticket granulare per ogni futura rimozione di entry (ratchet monotono). Ogni rimozione incrementale ha il suo ticket dedicato per tenere basso il surface area e minimizzare il rischio di race.

## Criteri di accettazione

- [ ] `Checks["database_sql_baseline_growth_since_seed"]` esiste nel report JSON (chiave-nuova), con valore ≥ `0`.
- [ ] All'apertura del PR del presente ticket, il valore del counter al HEAD è esattamente uguale a `len(databaseSQLLegacyBaseline_before_removal) - init` (NON negativo).
- [ ] Chiunque aggiunga una nuova entry a `databaseSQLLegacyBaseline` causa un valore > `0` → è un segnale operativo di crescita, NON una hard-fail.
- [ ] Il commit di chiusura include almeno una rimozione di entry obsoleta da `databaseSQLLegacyBaseline`, e `len(databaseSQLLegacyBaseline)_post_PR < len(databaseSQLLegacyBaseline)_pre_PR` (monotonicità).
- [ ] `database_sql_baseline` (counter esistente) rimane `>= 0`; nessuna regressione su `database_sql_*` counters famiglia.

## Test obbligatori

- [ ] Unit test che simula una var `databaseSQLLegacyBaseline` con N entry, verifica che `checks["database_sql_baseline_growth_since_seed"]` = `len(actual) - seed_value` (NON negativo).
- [ ] Integration test focused mode (`go run ./scripts/archcheck`) — il counter appare nel report JSON.
- [ ] Integration test ratchet mode (`go run ./scripts/archcheck --ratchet`) — il counter appare anche qui.
- [ ] Snapshot test: prima e dopo una removal PR, la differenza di `len(databaseSQLLegacyBaseline)` è esattamente −1 (o multiplo se l'operatore batcha rimozioni; in tal caso documentare il batch count).

## Evidenze richieste

- `Checks["database_sql_baseline_growth_since_seed"]` nel report JSON (focused + ratchet modes) — incollato nella descrizione del commit.
- Diff commit che mostra la rimozione di ≥ 1 entry obsoleta da `databaseSQLLegacyBaseline` con motivazione esplicita.
- Cross-link finale: `architecture/current.yaml::Wave 19::pr4_pr5_followups.items[PR5].verified_zero: true` (commit post-ticket).

---

## 4. Ordine di implementazione obbligatorio

1. **W19-PR4-001** — Hard gate promotion. Solo DOPO che gate attivo è stabile su main per almeno 24h.
2. **W19-PR5-001** — db/sql shrinkage (Path B growth audit).

Non iniziare W19-PR5-001 finché W19-PR4-001 non è `DONE`. W19-PR5-001 può procedere in parallelo con altri lavori Wave 16/17/18, ma **NON** può partire se W19-PR4-001 è ancora `OPEN` (l'audit del baseline growth ha senso solo DOPO che i due gate hard sono attivi e la reportistica immutabile su `main`).

## 5. Note di correlazione

- Wave 19 PR1 → PR2 → PR3 → PR5 hardening (`1b2da8f1`) → **PR4 PR-track** (questo ticket W19-PR4-001) → **PR5 PR-track** (questo ticket W19-PR5-001, **narrowed** al chunk db/sql shrinkage).
- Ticket `FollowWave19PR3TypedPortLift` (Path A) è indipendente da W19-PR5-001; può procedere in parallelo dopo W19-PR4-001.
- I due ticket NON chiudono Wave 19 (Wave 19 è già `done` + `verified_zero: true` dal PR2+PR3+PR5 hardening work); chiudono i due `pr4_pr5_followups.items[*]` figli (`verified_zero: false → true`).
- **`scripts/archcheck` exit semantic**: `passed=true and (focused_gate_passed=true OR mode=ratchet)` è il segnale canonico del gate. Qualsiasi regression in `Checks["application_to_infrastructure_*"]` o `Checks["cross_capability_*"]` durante il lavoro su questo ticket DEVE essere risolta prima del merge.
