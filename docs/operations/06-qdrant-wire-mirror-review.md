# Runbook operativo 06 — Architecture review QDRANT-REVIEW-001: audit del pattern wire-mirror QDRANT-005C

Stato: OPEN (audit + pianificazione EXPAND)
Data snapshot: 2026-06-27
Repository: `Marcuss-ops/PipelineGen`
Ticket id: **QDRANT-REVIEW-001**
Owner suggerito: ops/infra + maintainer del package `internal/application/qdrant/dr/` (con input dal wave entry `architecture/current.yaml::id-20` "QDRANT-005D hygiene").

Obiettivo: auditare il **pattern wire-mirror** introdotto in QDRANT-005C PR3 (Giugno 2026) — quattro coppie di struct dichiarate in due package distinti per aggirare il ciclo di import `infra ↔ dr`. Valutare se il pattern è la forma corretta o se è preferibile un singolo package canonico di tipi condivisi (`internal/application/qdrant/types/`) che entrambi i lati importano. Documentare le conseguenze (manutenzione doppia, compile-time assertion come drift-detector, rimozione near-term via `ExperimentalDeprecation`) per dare ai futuri maintainer una policy chiara e una **data target** per collassare il wire-mirror in un singolo package canonico.

---

## Regole di esecuzione del runbook

1. Ogni ticket di architecture review deve elencare la **decisione alternativa** considerata (in questo caso: singolo package canonico) e la motivazione che porta a mantenere o scartare lo stato attuale.
2. La sezione **consequences** deve essere enumerata (non narrativa), perché è il punto di ingresso che i futuri maintainer consultano quando chiedono "perché non è già collassato?".
3. Il ticket deve registrare un **deprecation_id** in `architecture/deprecations.yaml` anche se la rimozione è pianificata a medio termine — l'EXPAND/BACKFILL/CUTOVER/CONTRACT inizia alla registrazione, non alla rimozione.
4. Ogni data target deve essere conservativa (almeno 2 quarter dall'EXPAND phase) per dare respiro alla fase BACKFILL senza pressione sulle merge.
5. Nessuna modifica di produzione è richiesta da questo ticket — è un audit documentale. La rimozione del pattern arriva nei commit successivi del wave.

---

## Ticket QDRANT-REVIEW-001 — Audit wire-mirror pattern in `internal/infrastructure/qdrant` ↔ `internal/application/qdrant/dr/`

**Priorità:** P1
**Stato:** OPEN
**Dipendenze:** nessuna (audit documentale; l'EXPAND/BACKFILL/CUTOVER/CONTRACT arriva in wave successive)
**Deprecation ID:** `PR-QDRANT-WIRE-MIRROR` (registrato in `architecture/deprecations.yaml`).

## Problema

Il pattern wire-mirror introdotto in QDRANT-005C PR3 (commit `72e1d5c9` feat: PR 9-12 — bounded concurrency, typed envelope result, legacy adapters, dead contract removal) usa quattro coppie di struct dichiarate in due package distinti:

| Wire copy (`internal/infrastructure/qdrant/types_dr.go`) | Canonical mirror (`internal/application/qdrant/dr/types.go`) |
|---|---|
| `qdrant.SnapshotDescription` (line 45) | `dr.SnapshotDescription` (line 38) |
| assente — solo nel dr package | `dr.RetentionConfig` (line 50) |
| assente — solo nel dr package | `dr.RetentionResult` (line 60) |
| assente — solo il dr package | `dr.VerifyReport` (`dr/ports.go:62`) |

Più il connettore `dr_adapter.go` (che definisce `SnapshotStoreAdapter` / `AliasSwitcherAdapter` / `CollectionCreatorAdapter` / `VerifierAdapter` / `RetentionExecutorAdapter` / `PromDRMetricsAdapter`) traduce field-by-field tra le quattro coppie con `var _ dr.<Port> = (*<Adapter>)(nil)` come compile-time assertion.

### Root cause (perché esiste il pattern)

Il ciclo di import vietato da Go vieterebbe `internal/application/qdrant/dr/` di importare `internal/infrastructure/qdrant/` (perché `qdrant → dr_adapter → dr` chiude il cerchio). La scelta QDRANT-005C PR3 è stata di:
- Spostare i tipi canonici (SnapshotDescription, RetentionConfig, RetentionResult, VerifyReport) nel package `dr` come **mirror del wire side**.
- Lasciare la copia wire-side in `internal/infrastructure/qdrant/types_dr.go` per i decoder REST + i manager wrappers.
- Bridge in `dr_adapter.go` con campi ad-hoc + `var _ dr.<Port> = (*Adapter)(nil)` come invariant compile-time.

È il classico **EXPAND acknowledgement** di `docs/architecture/godlike/07_ZERO_LEGACY_POLICY.md` §"Cross-file redeclaration within one Go package" — accettabile oggi, ma con obbligo di enforcement via almeno UNA tecnica (a) lint same-package redeclaration (`scripts/ci-architectural-checks.sh::Check 5`) OPPURE (b) compile-time assertion gate. Il codice attuale usa (b).

### Trigger (cosa l'orchestrazione ha rivelato)

Il pattern wire-mirror è emerso perché l'architettura canonica (Clean Architecture: ports own types) richiede che `dr/ports.go` definisca le interfacce con i tipi canonici — ma i metodi `qdrant.Client.{Create,List,Restore}Snapshot` restituiscono i tipi wire. Senza il mirror, il compilatore Go non permetterebbe alcun flusso. Il pattern è dunque una soluzione operativa al divieto di import-cycle, non una scelta di idoneità.

## Audit: wire-mirror vs. single shared types package

### Alternativa "single shared types package"

Una via d'uscita canonica sarebbe un package `internal/application/qdrant/types/` (o `internal/application/qdrant/dr/types/` se si vuole restare dentro `dr/`) che:
- Contiene i 4 tipi canonici (SnapshotDescription, RetentionConfig, RetentionResult, VerifyReport) come pure-data structs.
- Non importa nulla (è un leaf types package).
- È importato sia da `internal/application/qdrant/dr/` (porte + servizi) sia da `internal/infrastructure/qdrant/` (decoder REST + manager wrappers).

Questo risolve il ciclo perché `qdrant → types` non viola `application → infrastructure` (types è pure-data leaf). Il mirror viene eliminato: il decoder REST popola direttamente `types.SnapshotDescription` invece di `qdrant.SnapshotDescription`, e il bridge in `dr_adapter.go` diventa trivialmente `passthrough` (no field copy).

### Verdetto

Il wire-mirror è un **EXPAND acknowledgement accettabile ma non definitivo**. I motivi:

1. **Costo di manutenzione doppia**: ogni campo aggiunto a una shape richiede una modifica simmetrica sull'altra + un bridge in `dr_adapter.go` + l'aggiornamento della compile-time assertion. Tre posti per una modifica logica. (Cfr. `docs/operations/05-qdrant-redeclaration-recovery.md` per il post-mortem storico.)
2. **Drift detector oggi funzionante ma fragile**: la compile-time assertion `var _ dr.<Port> = (*Adapter)(nil)` fallisce se l'adapter non implementa il port, ma NON fallisce se il field copy in `VerifierAdapter.VerifyReindex` dimentica un nuovo campo (è field-by-field manuale). Un futuro indebolimento del campo copia = silenzioso zero a runtime.
3. **Il package types puro è structurally equivalente all'investimento EXPAND**: nessuna dipendenza ciclica, nessuna concessione di layering, nessun costo runtime. Collassare è un **CONTRACT puro** (rimozione del mirror + bridge, no nuovo design).
4. **JSON-tag leakage trade-off (vedi sotto)**: il verdetto è contingente al risultato dell'analisi JSON-tag — Option B è raccomandata ma richiede validazione empirica in fase EXPAND.

### JSON-tag leakage: il trade-off load-bearing

L'audit non può raccomandare un verdetto definitivo senza prima pesare questo trade-off. `internal/infrastructure/qdrant/types_dr.go::SnapshotDescription` porta i JSON tag (`json:"name|creation_time|size|checksum,omitempty"`) sulla copia wire. `dr.SnapshotDescription` deliberatamente li omette (vedi `dr/types.go` doc: *"JSON tags are deliberately omitted: the application layer does not marshal this shape to REST callers; the admin CLI does, via `json.MarshalIndent`"*).

Il collasso a un singolo struct pone due concessioni possibili:

- **Option A (single-struct + JSON tags)**: la copia canonical eredita i JSON tag wire-side; il package `dr/restore.go` finisce per dipendere dalla forma REST Qdrant. Questo viola direttamente `docs/architecture/godlike/07_ZERO_LEGACY_POLICY.md` §"No fake availability" — il application domain non deve silenziosamente dipendere dalla REST surface.
- **Option B (per-shape WireShape + CanonicalShape siblings in ONE types package)**: il nuovo `internal/application/qdrant/types/` ospita DUE struct per ogni concetto: `WireSnapshotDescription` (con JSON tags, owner: grpc/REST decoder) e `CanonicalSnapshotDescription` (senza JSON tags, owner: dr package + admin CLI). Sono sibling PURE-data, identici-by-construction (field-for-field). Non serve compile-time assertion perché l'helper `wireToCanonical` + `canonicalToWire` è generato o trivialmente scritto e testato una volta sola. La cycle-break resta: types è leaf, infra importa `WireSnapshotDescription`, dr importa `CanonicalSnapshotDescription`.
- **Option C (status quo wire-mirror indefinito)**: il trade-off JSON-tag resta vivo, il dual maintenance resta, ma il drift detector funziona e la separazione dei concern è netta. Nessuna rimozione pianificata.

**Raccomandazione provvisoria: Option B.** I motivi:

- Elimina il dual maintenance (un solo file per Wire + Canonical, monocromatico, due struct side-by-side).
- Elimina il drift detector fragile (l'helper di traduzione è triviale e testabile, non field-by-field manuale).
- Mantiene la separation of concerns: wire-shape con JSON tags è owned dal package types ma è destinato a infra (questo è annunciato via godoc-comment + ownership yaml entry); canonical è owned dal dr package.
- È il solo modo per **non** ereditare JSON tags nel dr domain.

**Azione richiesta prima del COMMIT**: smoke test rigoroso sulla admin CLI pre/post EXPAND, confermando che il marshal output di `CanonicalSnapshotDescription` (oggi CapitalCamel via default Go) non cambia dopo l'aggiunta dei JSON tag alla `WireSnapshotDescription`. Se lo smoke test fallisce (admin CLI dipende parzialmente da CapitalCamel), si degenera a Option C.

Sulla base del risultato dello smoke test EXPAND:

- **Se Option B passa lo smoke test**: la fase CONTRACT target rimane **Q4 2026**.
- **Se Option B fallisce lo smoke test**: la target diventa **Q1 2027** con Option C come fallback raccomandato (data trade-off, non data crollo).

Il pattern merita pertanto una rimozione pianificata via **EXPAND/BACKFILL/CUTOVER/CONTRACT** con data target **Q4 2026 (contingent) / Q1 2027 (fallback)**.

## Conseguenze (enumerated)

### 1. Dual maintenance burden (oggi)

Per ogni nuovo campo lógico su una qualsiasi delle 4 shape mirror:
- `internal/infrastructure/qdrant/types_dr.go` deve essere aggiornato (wire copy + JSON tags).
- `internal/application/qdrant/dr/types.go` deve essere aggiornato (canonical + omitempty JSON).
- `dr_adapter.go` deve aggiornare il field-by-field copy in `translateSnapshot` / `VerifierAdapter.VerifyReindex` / `RetentionExecutorAdapter.CleanupWithConfig` / i port signatures equivalenti.
- La compile-time assertion (`var _ dr.<Port> = (*Adapter)(nil)`) deve continuare a passare. (Non serve cambiare l'asserzione; serve che l'utente non dimentichi un campo nel bridge).

Questo è un moltiplicatore 3-4× rispetto a un modello "single shared types package". Carico accettabile per 4 tipi ma che diventa ingestibile se PR future aggiungono shapes (es. alias-switch telemetry, dr-side DR metrics).

### 2. Compile-time assertion gates as drift detector (oggi)

Le compile-time assertion sono **oggi la rete di sicurezza** del pattern:

```go
// dr_adapter.go:46
var _ dr.SnapshotStore = (*SnapshotStoreAdapter)(nil)

// dr_adapter.go:84
var _ dr.AliasSwitcher = (*AliasSwitcherAdapter)(nil)

// dr_adapter.go:104
var _ dr.CollectionCreator = (*CollectionCreatorAdapter)(nil)

// dr_adapter.go:152  ← nota: il field copy è manuale e non è protetto
var _ dr.Verifier = (*VerifierAdapter)(nil)

// dr_adapter.go:194
var _ dr.RetentionExecutor = (*RetentionExecutorAdapter)(nil)

// dr_adapter.go:222
var _ dr.DRMetrics = (*PromDRMetricsAdapter)(nil)
```

Limite: queste assertion rilevano SOLO errore di signature a livello di metodo (adapter non implementa un metodo del port). NON rilevano drift nei field copy manuali (es. `VerifierAdapter.VerifyReindex` che dimentica di copiare `DeadLetterOpen` in futuro → silenzioso zero a runtime). `docs/architecture/godlike/07_ZERO_LEGACY_POLICY.md` riconosce esplicitamente la fragilità: il pattern è un EXPAND acknowledgement ma non sostituisce un test di conformità.

### 3. Near-term removal via ExperimentalDeprecation (zero-legacy, Giugno 2026)

`docs/architecture/godlike/07_ZERO_LEGACY_POLICY.md` §"Migration sequence" prescrive **EXPAND → BACKFILL → CUTOVER → CONTRACT** per ogni migrazione. La sequenza di rimozione del wire-mirror è:

- **EXPAND** (questo ticket + deprecation entry): audit + decisione di collassare + registrazione PR-QDRANT-WIRE-MIRROR.
- **BACKFILL** (Q3 2026): migrazione incrementale delle firme dei port da `dr.SnapshotStore` a `types.SnapshotStore`. Aggiornare uno per uno i 4 tipi.
- **CUTOVER** (Q3/Q4 2026): tutti i decoder REST scrivono direttamente `types.SnapshotDescription` invece di `qdrant.SnapshotDescription`. `dr_adapter.go` diventa trivialmente passthrough.
- **CONTRACT** (Q4 2026): rimozione del mirror (`types_dr.go` slim-down) + rimozione dei field copy in `dr_adapter.go` + rimozione delle compile-time assertion ormai ridondanti (la protezione torna nativa nel compilatore Go perché c'è un solo tipo).

### 4. Data target di rimozione

**Q4 2026 (entro Dicembre 2026).** Motivazione:
- Allinea con la cadenza dei wave tracciati in `architecture/current.yaml` (recent wave-rollouts: Wave 20 QDRANT-005D hygiene).
- 2 quarter di respiro tra audit (Q2) e CONTRACT (Q4) per le fasi BACKFILL + CUTOVER senza pressione sulle landing.
- Coincide con la milestone di "fine cleanup wave" del progetto (analogous to the June 2026 milestone che ha prodotto questo pattern).

## Criteri di accettazione per la chiusura del ticket (CONTRACT phase)

- [ ] `grep -rn 'type SnapshotDescription' internal/` ritorna esattamente 1 hit (il canonical in `internal/application/qdrant/types/`). Idem `RetentionConfig`, `RetentionResult`, `VerifyReport`.
- [ ] `internal/infrastructure/qdrant/types_dr.go` non contiene più le struct mirror (può contenere solo typing helpers / codec helpers se utili).
- [ ] `dr_adapter.go` non contiene più field copy manuali verso le 4 shape collassate — i bridge sono passthrough o rimossi.
- [ ] Le compile-time assertion `var _ dr.<Port> = (*Adapter)(nil)` sono rimosse (la protezione ora è nativa perché c'è un solo tipo omonimo).
- [ ] La deprecation entry `PR-QDRANT-WIRE-MIRROR` in `architecture/deprecations.yaml` è flaggata `status: removed` + `migration_phase: CONTRACT` concluso.
- [ ] Compatibilità test: `go build ./internal/infrastructure/qdrant/...` + `go build ./internal/application/qdrant/dr/...` exit 0.
- [ ] Tutti i consumer esistenti (admin paths in `cmd/admin/dr_qdrant.go`, anche Wrapper/CLI) continuano a funzionare senza modifiche alla loro firma (è il bridge a gestire la transizione).

## Test obbligatori (per ogni fase della migration sequence)

- [ ] **Audit phase (questo PR)**: `bash scripts/ci-architectural-checks.sh` exit 0 con la deprecation-entry PR-QDRANT-WIRE-MIRROR registrata.
- [ ] **EXPAND phase**: nuovo file `internal/application/qdrant/types/snapshot.go` (e simili per gli altri 3 tipi). Compile-time: import dal dr package e dal qdrant package deve restare cycle-free.
- [ ] **BACKFILL phase**: smoke test — un consumer `dr.SnapshotService.ListSnapshots` riceve `[]types.SnapshotDescription` invece di `[]dr.SnapshotDescription`; nessun cambio al call site perché il bridge assorbe.
- [ ] **CUTOVER phase**: smoke test — un decoder REST scrive direttamente `types.SnapshotDescription`; nessun bridge più necessario per quel consumer.
- [ ] **CONTRACT phase**: zero method declarations `func translateSnapshot(` rimaste in `dr_adapter.go`. Zero type declarations plurali per `SnapshotDescription` / `RetentionConfig` / `RetentionResult` / `VerifyReport`.

## Evidenze richieste per la chiusura

- `git show --stat` dei 4 commit (uno per fase BACKFILL/CUTOVER/CONTRACT; EXPAND è questo PR).
- Output di `grep -rn 'type <X>' internal/` per ciascuno dei 4 tipi — 1 hit solo.
- Output di `grep -rn 'func translate' internal/infrastructure/qdrant/dr_adapter.go` — 0 hits dopo CONTRACT.
- Ci gate: `scripts/ci-architectural-checks.sh::Check 5` esce 0; `architecture/deprecations.yaml::PR-QDRANT-WIRE-MIRROR.status: removed`.

## Stato

`OPEN`. Questo PR è la registrazione EXPAND. Le fasi BACKFILL/CUTOVER/CONTRACT avvengono in PR successive entro Q4 2026, ognuna con la sua migration_phase aggiornata in `architecture/deprecations.yaml`.

---

## 3. Definizione di Done globale

Il runbook 06 chiude quando:
- Tutti i criteri di accettazione sono ✓.
- La deprecation entry `PR-QDRANT-WIRE-MIRROR` flagga `status: removed`.
- La migration sequence ha completato le 4 fasi (audit → EXPAND → BACKFILL → CUTOVER → CONTRACT).
- `architecture/current.yaml` registra la wave finale (suggerita: id 23 "QDRANT-WIRE-MIRROR-COLLAPSE" Q4 2026).
- `docs/architecture/godlike/07_ZERO_LEGACY_POLICY.md` §"Cross-file redeclaration within one Go package" riceve un'append/appendice che annota questo caso come collapse completato (analogous to come Wave 20 ha documentato il post-QDRANT-005D riferimento).

---

## 4. Note operative

- **Follow-up engineering PR (out of scope oggi, ma scoperto durante l'audit)**: aggiungere uno scenario di regressione in `internal/infrastructure/qdrant/dr_adapter_test.go` che alimenta `VerifierAdapter.VerifyReindex` con una wire shape deliberatamente aumentata (campo extra noto) e asserisce che `VerifyReport` faccia round-trip del campo (o errori tipizzati). Questo chiude il gap di field-copy-drift che le compile-time assertion `var _ dr.<Port> = (*Adapter)(nil)` non rilevano oggi (catturano il signature drift ma non il field-copy drift manuale). È engineering, non audit — non blocca la registrazione EXPAND.

- Replacement per l'attuale pattern: un singolo package `internal/application/qdrant/types/` (oppure internalizzato sotto `internal/application/qdrant/dr/types/` se si preferisce tenerlo dentro il package ports). Layering rule: pure-types package non importa nulla, quindi sia infra sia dr possono importarlo senza ciclo di import.
- `architecture/ownership.yaml` dovrà registrare `application_qdrant_types:` come sub-ownership con rule "MUST NOT import anything from internal/infrastructure or internal/api" — coerente con gli altri ownership entry.
- La wave finale (`QDRANT-WIRE-MIRROR-COLLAPSE`) è il candidato naturale per promuovere lo stato attuale da Phase 0 (report-only) a Phase 1 (gate-promoted) per il Check 5 expanded (same-package redeclaration across `internal/`), per analogia con Wave 20.
- Owner definitivo della wave finale: stessa code-ownership di `wave_qdrant_005d_hygiene` per continuità di governance. La registrazione di questa ownership avviene al primo commit BACKFILL.
- Tracking issue (GitHub-side): `QDRANT-REVIEW-001 → autoref su PR back-fill etichettati "QDRANT-WIRE-MIRROR-COLLAPSE"`.
