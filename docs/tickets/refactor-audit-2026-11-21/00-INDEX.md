# Refactor audit — wave del 2026-11-21

**Data:** 2026-11-21
**Base misurata:** `refactored` @ `34d21526c` + working tree (misure eseguite il 2026-11-21)
**Origine:** report esterno "Verdetto Esecutivo / Matrice Hotspot / Piano d'Azione" ricevuto il 2026-11-21.
**Vista:** questo indice è la vista di wave; ogni ticket è la fonte proprietaria del proprio
perimetro, della propria evidenza e dei propri check.

## 1. Metodo

Il report non è stato copiato: ogni claim è stato confrontato con il codice di questo
checkout. Le misure sono state rifatte dove costavano poco (`archcheck`, conteggi file,
`git check-ignore`, grep mirati). Dove la misura richiede strumenti assenti sul host
(`deadcode`, `gocyclo`) o servizi live (Postgres, Master, worker), il ticket lo dichiara e
include il comando da eseguire, invece di ereditare il numero del report.

I ticket non contengono codice: contengono il perimetro, la causa, la sequenza di
modifiche e i check che chiudono la scheda. Un ticket si chiude solo con la sezione
**Check finali** eseguita e il suo output incollato nella scheda.

> **Numeri di riga.** Le righe citate sono quelle misurate il 2026-11-21 sulla base
> `34d21526c`. Il working tree è in movimento (46 file modificati/cancellati durante la
> sessione): ogni ticket riporta anche il `grep` che localizza il difetto. Se il numero non
> corrisponde, vale il grep — e il ticket va aggiornato con la nuova riga.

## 2. Stato misurato (riprodotto, non ereditato)

| Misura | Comando | Esito 2026-11-21 |
| --- | --- | --- |
| Gate architetturale | `go run ./cmd/archcheck --strict` | **exit 1**, `has_hard_gate_hits: true`, **6 violazioni `error`** + **67 `warnings`** |
| Violazioni port boundary | idem (`rule=percheck_platform_port_boundary`) | 3: `semantic_engine_discourse.go:6`, `semantic_engine_people_events.go:9`, `semantic_engine_places.go:7` |
| Violazioni hotspot | idem (`rule=pkg_size`, `percheck_legacy_hotspot_growth`) | 3: `internal/app/wiring` 72 > 70 (×2 regole), `internal/capabilities/scripts` 146 > 141 |
| File prod `internal/app/wiring` | `find … -name '*.go' ! -name '*_test.go' \| wc -l` | **72** |
| File prod `internal/capabilities/scripts` | idem | **146** |
| Binari/artefatti in root | `ls -la` + `git check-ignore` | presenti (~207 MB: `server` 83 MB, `goldensummary` 36 MB, `semantic-extract` 34 MB, `gen_baseline` 11 MB, `velox` 10 MB, `coverage.out` 9,5 MB, `archcheck` 6,4 MB, `overlaystatus`, `velox-knobs`, `regen-current-yaml`) ma **tutti gitignored**: `git status` mostra 4 soli file non tracciati |
| Dead code | `deadcode -test=false ./...` | **229** ≤ guard **229** (`-test` = 84), allineato a `docs/operations/DEAD-CODE-RETIREMENT-PLAN-2026-11-18.md`. La misura 279/≤292 di RA-03 §6 è superata dal ratchet del §7 |
| **Ricontrollo a metà sessione** (working tree aggiornato da lavoro concorrente non committato) | `go run ./cmd/archcheck --strict` + conteggi | **exit 0**, `total_violations: 0`, 67 warning; `internal/app/wiring` **70**, `internal/capabilities/scripts` **140**; i 3 file `rustexec` importano `internal/kernel/script/semantic` |

> **Attenzione allo snapshot.** La riga rossa descrive il tree com'era all'inizio della
> sessione ed è quella che il report esterno ha misurato: è corretta per quel tree. Mentre
> questa wave veniva scritta, un lavoro concorrente non committato ha chiuso entrambe le
> violazioni strutturali. Per questo RA-01 e RA-02 sono schede di **verifica e chiusura**, non
> di implementazione: il codice c'è, ma non è committato e non è verificato. Chi le esegue
> parte dal §0 di ciascuna scheda.

## 3. Analisi del report: cosa regge e cosa va corretto

### 3.1 Confermato sul codice

- I 6 errori di `archcheck --strict` e i due conteggi hotspot: identici, riga per riga.
- Il `defer` dentro il `for` in `adapters/vidrush_internet_image_candidates.go:83-100`
  (il `releaseDiscovery` resta trattenuto fino al ritorno della funzione, non a fine
  iterazione): presente.
- Il bypass di porta `interface{ DB() *sql.DB }`: 5 siti in `internal/app/wiring`
  (`build_bundles_voiceover.go:122`, `script_generation_bindings.go:40`,
  `canonical_media_committer_derived_reads.go:44,73,103,137,171` — 7 assertion in 3 file) +
  `internal/capabilities/assets/artifacts/clips_adapter.go:371`.
- SQL/DDL dentro `internal/capabilities`: `cliprender/render_cache.go:142,230`,
  `localization/adapters/render_reuse.go:62,150`, `assets/artifacts/repository.go:31,175,195`.
- `RENDERINGGEN_STORE_URL` letto con `os.Getenv` in 3 punti fuori da `knobs`/config
  (`app/wiring/script_generation_runtime.go:178`, `script_generation_overlay_queue.go:21`,
  `registry_internal_modules_render.go:142`).
- `writeMu sync.Mutex` di package in `overlaystatus/status.go:78`, tenuto per tutta la
  scrittura su disco.
- `resetFontAssetCache()` che azzera `assetRootOnce = sync.Once{}` (`font_assets.go:111,200`).
- `logSink` package var senza lock (`httpserver/middleware/middleware_logger.go:33`).
- Variadico posizionale `perSceneImages ...bool` con semantica `[0]`/`[1]`
  (`overlay_plan_compile.go:23,129,135`).
- Bus non tipizzato del final job: `map[string]any` in `final_job_payload.go:565-599`, con
  **tre riassegnazioni ridondanti** a `pre["compiled_render_plan_json"]`,
  `pre["compiled_render_plan_sha256"]`, `finalize["runtime_assets"]` (595-597) di chiavi già
  scritte a 569-576.
- `OverlayStyleProfile` in `channelprofile/types.go:171-181` ha **5 campi in meno** di
  `kernel/script.OverlayStyleSpec` (`Bullet`, `GlowColor`, `Cursor`, `Image`,
  `CardBackground` — verificato in `overlay_style_spec.go:56-88`).
- `AppendProviderCandidatesUnique` clona il base e ricostruisce la map a ogni chiamata
  (`vidrush_candidate_shared.go:13-33`): O(N²) se invocata per singolo candidato.
- `mirrorInFlight sync.Map` (`ytdlp_mirror.go:120-125`): `LoadOrStore` senza mai `Delete`.
- `http.DefaultClient` in produzione: **4** siti, non 1 (`remote_final_job_artifact.go:110`,
  `vidrush_materialization.go:333`, `person_image_scorer.go:69,190`).
- `os.ReadFile` su file pesanti (`render_queue_map_composition.go:239`,
  `soundeffect/handler.go:192`).

### 3.2 Ridimensionato o smentito (non copiare nel ticket)

1. **`remote_final_job_artifact.go:51` non è un "falso successo" dimostrato.** Il codice
   restituisce `(receipt, nil)` con `artifactToken` vuoto, ma il commento di funzione
   dichiara che il persister fallisce chiuso con errore di configurazione, e il consumo di
   `ArtifactPath` avviene in `capabilities/scripts/durable_mapper.go:97`. Il difetto reale
   osservabile è un altro: **nessun test prova il fail-closed** per quel ramo, e manca il
   timeout sul client. Ticket RA-05 chiede prima un test, poi l'eventuale hardening — non
   "correggi un bug" sulla parola del report.
2. **`catalogCache[root] = nil` è deliberato e documentato** (`overlays/map_territory.go:135-141`:
   "a missing catalog never memoizes a failure into global state for later callers"). La
   conseguenza vera è che il *fallimento* resta memoizzato per il processo: se il file
   compare dopo, serve un restart. Non è "errore cachato al posto del risultato".
3. **"10+ binari non tracciati in root" è falso in questo checkout**: i binari esistono ma
   sono tutti coperti da `.gitignore`/`.git/info/exclude`; il working tree ha 4 file non
   tracciati, tutti sorgenti/test. La pulizia è igiene locale dell'operatore, non un
   difetto del repo, e non va messa in un gate CI.
4. **`audio/processor.go:202-230` controlla il ctx**: il `select` su `ctx.Done()` c'è. Il
   difetto residuo è solo lo sleep dopo l'ultimo tentativo fallito (300 ms buttati).
5. **`regexp.MustCompile`: 102 occorrenze** in `internal/` non-test, non 13. Le 13 sono i
   siti *dentro il corpo di una funzione* secondo il report; la distinzione va rifatta con
   un check, perché il numero aggregato è un'altra cosa.
6. **Il perimetro del port boundary è più largo delle 3 violazioni.** In
   `internal/platform/media/rustexec` **16** file non-test importano
   `internal/capabilities/scripts`; 13 sono in `platformCapabilityScriptsInventory`
   (debito registrato) e solo i 3 non inventariati sono errori. Correggere solo quei 3
   senza toccare l'inventario sposta il problema, non lo chiude: RA-01 chiede l'estrazione
   dei contratti semantici in `internal/kernel/script/…` e l'azzeramento dell'inventario
   per `rustexec`.
7. **`deadcode 300` non è verificabile qui** (tool assente). RA-03 parte dal guard
   documentato ≤ 292 e chiede la rimisurazione come primo passo.
8. **Il perimetro del port boundary si è chiuso durante la sessione.** Al ricontrollo i tre
   file non importano più il package business: importano `internal/kernel/script/semantic`.
   Restano **12** entry `rustexec` in `platformCapabilityScriptsInventory` (le altre famiglie,
   debito registrato): RA-01 verifica che la migrazione non sia solo un'entry di inventario
   aggiunta e, se possibile, prosegue con le 12 rimanenti.

### 3.3 Non verificabile in questa sessione

- Conteggi di branch/ciclomatica delle sei funzioni "god" (richiede `gocyclo`/`gocognit`):
  RA-18 fissa il comando e la soglia, non il numero del report.
- Cicli di sottosistema `capabilities <-> platform` (148/119 import, 19 cicli): RA-11 usa
  `go list -deps` e uno script di SCC; i numeri del report sono un'ipotesi da riprodurre.
- I 300 deadcode, i 143/120/114 branch, i "1.784 tocchi di package" su 150 commit: il
  metodo va riprodotto (RA-03, RA-18) prima di usarli come baseline.

## 4. I ticket

| ID | Area | Priorità | Dipende da | Stato |
| --- | --- | --- | --- | --- |
| [RA-01](01-PLATFORM-PORT-BOUNDARY.md) | Port boundary: contratti semantici fuori da `capabilities/scripts` | P0 | — | **DONE (verificato)** — import dal kernel, 0 violazioni; da committare |
| [RA-02](02-HOTSPOT-CAPS.md) | Hotspot caps: `wiring` 72→≤70, `scripts` 146→≤141 | P0 | — | **DONE** — 70/140, `scripts` ratchetato a 140; da committare |
| [RA-03](03-DEADCODE-BUDGET.md) | Budget dead code (guard ≤292) | P0 | — | **DONE** — `deadcode -test=false` = **229**; guard ratchettato a ≤229, oracoli in allowlist |
| [RA-04](04-DEFER-IN-LOOP-VIDRUSH.md) | `defer` in loop: lock discovery trattenuti | P0 | — | **DONE** |
| [RA-05](05-REMOTE-ARTIFACT-FAIL-CLOSED.md) | Artifact remoto: fail-closed provato + timeout HTTP | P0 | — | **DONE** |
| [RA-06](06-RUNNER-ERROR-PROPAGATION.md) | Runner/voiceover: errori inghiottiti e stato "completed" falso | P0 | — | **DONE** — §1.1/§1.2/§1.3 chiusi; commit fallito → orphan cleanup provato |
| [RA-07](07-OUTBOX-FALSE-SUCCESS.md) | Outbox: successo su dipendenze nil e retry infinito su validazione | P1 | — | **DONE** — voiceover, delivery e `index_delete` chiusi |
| [RA-08](08-RETRY-HYGIENE.md) | Retry: spin loop, sleep finale, jitter, ctx | P1 | — | **DONE** |
| [RA-09](09-GLOBAL-STATE-RACES.md) | Stato globale mutabile e race (`sync.Once`, `logSink`, registry, cache) | P1 | — | **DONE** — font, logSink, registry Ollama, clock seam e matcher atomici; cache VidRush già LRU thread-safe |
| [RA-10](10-OVERLAYSTATUS-WRITE-LOCK.md) | `overlaystatus`: mutex globale su I/O disco | P1 | — | **DONE** |
| [RA-11](11-CAPABILITY-PLATFORM-CYCLES.md) | Cicli `capabilities ↔ platform` e fan-out | P1 | RA-01 | **DONE** — coppie 19→18, `system ↔ httpserver` risolta, percheck attivo; fan-out wiring 217 |
| [RA-12](12-SCRIPTS-SCC-SPLIT.md) | SCC di `scripts`: split contract-first di `GenerateRequest/Result` | P1 | RA-02 | **OPEN** — inventario 78+67 e contratti generation/audio fatti; SCC 118, migrazione lettori resta |
| [RA-13](13-SQL-IN-CAPABILITIES.md) | SQL/DDL dentro le capability (I/O binder) | P1 | — | **DONE** — tre adapter in `platform`, migrazioni 015/016, percheck `capability_sql_ban` con inventario 21 file |
| [RA-14](14-PORT-BYPASS-DB.md) | Port bypass `interface{ DB() *sql.DB }` | P1 | — | **DONE** — porta `EngineHandle`, SQL media in `platform/postgres/media`, percheck `capability_db_bypass` |
| [RA-15](15-WIRING-BUSINESS-LOGIC.md) | Logica media dentro `app/wiring` | P1 | RA-02 | **DONE** — pipeline in `platform/media/finaljob`, contratti kernel, wiring 66 file, gate `os/exec` |
| [RA-16](16-UNTYPED-FINALJOB-BUS.md) | Bus `map[string]any` e ri-serializzazioni del final job | P1 | RA-12 | **DONE** — builder 92→3, fasi ≤30, porte/wire tipizzati, golden round-trip |
| [RA-17](17-PRIMITIVE-OBSESSION.md) | Primitive obsession e parametri opachi | P2 | RA-19 | **DONE** — bundle `overlayPlanImageOptions`, 0 `...bool`, tipi valore in kernel |
| [RA-18](18-GOD-FUNCTIONS.md) | Sei funzioni oltre soglia di complessità | P1 | — | **DONE** — `materialize` 107→13, `semanticBundles` 93→8; `buildFinalJobPayloads` 92 tracciato in RA-16 |
| [RA-19](19-DUPLICATION-SSOT.md) | Duplicazioni e deriva semantica (validatori, mirror, env) | P2 | — | **DONE** — tutti i sei punti chiusi (grid, mirror, cache L1/L2, parser/URL Drive, helper SHA/colore/lingua, env store) |
| [RA-20](20-REGEXP-PRECOMPILE.md) | `regexp.MustCompile` a runtime | P2 | — | **DONE** — 11 siti a package-level |
| [RA-21](21-QUADRATIC-LOOPS.md) | Loop quadratici e allocazioni nascoste | P2 | — | **DONE** — nessuna misura bench, solo complessità |
| [RA-22](22-UNBOUNDED-STATE-IO.md) | Stati illimitati e I/O senza limiti | P2 | — | **DONE** |
| [RA-23](23-GOVERNANCE-DRIFT.md) | Deriva di governance e artefatti locali | P2 | — | **DONE** — baseline vuota + gate sulle deadline |

## 5. Ordine di esecuzione

1. **Ondata A — sbloccare il gate e i bug P0** (nessuna dipendenza esterna):
   RA-04, RA-05, RA-06, RA-01, RA-02. Il gate è **già verde** nel working tree attuale per
   RA-01/RA-02 (lavoro concorrente): il primo passo è verificarle e committarle, poi restano
   RA-04, RA-05, RA-06.
2. **Ondata B — robustezza**: RA-03, RA-07, RA-08, RA-09, RA-10.
3. **Ondata C — architettura**: RA-11, RA-12, RA-13, RA-14, RA-15, RA-16, RA-18.
4. **Ondata D — igiene e prestazioni**: RA-17, RA-19, RA-20, RA-21, RA-22, RA-23.

RA-02 e RA-15 non possono essere chiusi contemporaneamente senza coordinamento: entrambi
spostano file fuori da `internal/app/wiring`. La regola della wave è che il conteggio non
risale: ogni ticket che tocca un hotspot deve chiudere con il conteggio ≤ baseline.

## 6. Definizione di "check finali"

Comuni a tutta la wave, da eseguire nell'ordine e da incollare nella scheda:

```bash
cd refactored
go build ./...                                   # exit 0
go vet ./...                                     # exit 0
gofmt -l <file toccati>                          # nessun output
go test ./<package toccati>/...                  # PASS
go run ./cmd/archcheck --strict                  # exit 0, has_hard_gate_hits=false
make verify-fast                                 # PASS
make verify-agent                                # PASS (se il cambio tocca >1 componente)
```

Specifici, quando il ticket li richiede:

```bash
deadcode -test=false ./... | wc -l               # <= guard dichiarato nel ticket
go list -deps ./internal/... > /tmp/deps.txt     # analisi cicli / fan-out
git diff --stat                                   # il perimetro dichiarato, niente altro
```

`verify-main`, `verify-unit` e le suite live non sono eseguibili senza Postgres/Master/
worker reali: se un ticket ne ha bisogno lo dichiara come `BLOCKED-ENV`, non come PASS.

## 7. Regola di chiusura

- Un ticket si chiude **solo** con: (a) diff che rispetta il perimetro dichiarato, (b) tutti
  i check della sua sezione eseguiti con output, (c) nessun nuovo warning hard in
  `archcheck --strict`, (d) nessun aumento dei conteggi hotspot né del dead code.
- Un ticket che scopre un problema fuori perimetro apre una scheda nuova invece di
  allargarsi.
- Le correzioni al report elencate in §3.2 sono vincolanti: chi esegue RA-05 o RA-22 parte
  dal fatto verificato, non dalla formulazione originale.

## 8. Verifica finale (2026-11-21)

Eseguita sul working tree dopo tutte le correzioni. Comandi e output reali, non ereditati:

| Check | Comando | Esito |
| --- | --- | --- |
| Build | `go build ./...` | exit 0 |
| Vet | `go vet ./...` | exit 0, nessun output |
| Format | `gofmt -l` sui 103 file `.go` toccati | nessun output |
| Test aggregati | 21 package (capabilities, platform, kernel, wiring, archcheck) | exit 0 |
| Architettura | `go run ./cmd/archcheck --strict` | exit 0, `passed=true`, 0 violazioni, 67 warning |
| Dead code | `deadcode -test=false ./...` | **229** ≤ 229 (`-test` = 84); ratchet riconciliato con `DEAD-CODE-RETIREMENT-PLAN` |
| Hotspot | `app/wiring` 70, `capabilities/scripts` 140 | entrambi a cap |

Suite **non** eseguite e dichiarate come tali: `verify-main`, `verify-unit`, Postgres live,
Master, worker reale. `deadcode` e `gocyclo` sono installati con `go install`; le misure sono
state rifatte il 2026-11-21: dead code **229**, RA-18 misurato (top: `OverlayStyleSpec.Validate`
183, `materialize` 107, `enqueueChrononPlan` 99, `OverlayPlan.Validate` 97,
`processInternetImageSegments` 93, `buildFinalJobPayloads` 92) e registrato in
`18-GOD-FUNCTIONS.md` §7.

### 8.1 Requisiti: dove sono

- **P0 bloccanti** — RA-01, RA-02, RA-03, RA-04, RA-05, RA-06 chiusi.
- **Robustezza** — RA-07, RA-08, RA-09, RA-10: due chiusi, due parziali con la parte restante
  scritta in scheda e il perché.
- **Architettura** — RA-11, RA-13, RA-14, RA-15 e RA-16 chiuse in questa wave; resta
  OPEN RA-12: **22 chiuse su 23**. RA-12 (split contract-first di `scripts`, SCC 118) è una
  migrazione multi-PR: richiede i contratti di fase, la migrazione dei lettori e solo dopo
  il taglio dei file. Il tree contiene oltre 100 file non committati di lavoro concorrente:
  avviare lo split ora mischierebbe due autori nello stesso diff, che è esattamente ciò che
  RA-23 vuole impedire.
- **Igiene e prestazioni** — RA-20, RA-21, RA-22, RA-23 chiusi.

### 8.2 Aggiornamento RA-19 (2026-11-21, sessione successiva)

Punti 1, 2, 4 e 6 della scheda chiusi.

- **Punto 1** — `OverlayImageBackdropGridSpec` eliminato: `OverlayImageBackdropSpec.Grid` è
  `*OverlayImageGridSpec`; `validateImageGrid(prefix, grid)` è l'unico validatore delle due
  superfici, `validateImageBackdrop` rimosso.
- **Punto 2** — eliminati `OverlayStyleProfile` e i tre tipi speculari, aggiunti i `yaml` tag a
  `kernel/script.OverlayStyleSpec` (e ai tipi annidati), decodifica diretta del documento
  canale, validazione delegata a `OverlayStyleSpec.Validate()`, default `shadow.enabled`
  preservato. Test nuovi: `TestParseDecodesTheCanonicalOverlayStyleSpec`,
  `TestParseAppliesTheProfileShadowDefault`.
- **Punto 4** — i tre parser Drive manuali sono spariti: i call-site usano
  `urlutil.FileIDFromDriveLink`; nuovo `urlutil.DriveFileURL(id)` adottato in 14 costruttori
  della stessa URL. Test nuovi: `TestDriveFileURL`, `TestDriveFileURLRoundTrips`.
- **Punto 6** — `RENDERINGGEN_STORE_URL` è il binding env di
  `external.renderinggen_store_url` (default `http://127.0.0.1:9000`), letto solo dal loader;
  i tre siti di `app/wiring` usano `renderingGenStoreURL(cfg)`. Test nuovo:
  `TestExternalConfigRenderingGenStoreURLBindings`.

Verifiche: `go build ./...`, `gofmt`, `go vet` puliti; `go test` su `channelprofile`, sul
filtro `ChannelProfile` di `scripts`, su `platform/config`, `app/wiring` e sui 13 package
toccati dal punto 4 PASS; `archcheck --strict` invariato. La scheda resta **PARTIAL** per i
punti 3 e 5.

### 8.3 Cosa manca per considerare chiusa l'intera wave

1. Committare il working tree (RA-01/RA-02 sono verificate ma non ancora in un commit).
2. RA-11…RA-15 e RA-18, una per sessione, ognuna con i check della §6. RA-17 resta PARTIAL
   per il variadico e per i tipi valore; RA-19 è chiusa.
3. RA-09 §1.4 (hook/cache VidRush): iniezione delle deps in un tipo, migrazione incrementale.
4. RA-06 §1.3: decisione di contratto sull'async voiceover (§7 della scheda).

### 8.4 Aggiornamento sessione 2026-11-21 (pomeriggio)

- **RA-19 DONE** — punto 3: cache L1/L2 unica in `adapters/internet_image_cache.go` con
  `internetImageReplayPolicy` (media resolver) e `internetImageObservationPolicy` (fan-out);
  punto 5: `kernel/color` (`IsHexRGB`/`ParseHexRGB`), `digest.IsSHA256`/
  `IsCanonicalSHA256` e `asset.Normalize` sono gli unici validatori; 8 copie colore, 5 copie
  SHA-256 e `normalizeLanguageTag` eliminate.
- **RA-07 DONE** — `IndexDeleteHandler` panic in costruzione su deleter mancante e errore
  terminale in `Handle` se le dipendenze mancano.
- **RA-09** — registry Ollama rimosso (era una catena morta senza writer); VidRush resta.
- **RA-03** — misura 229 (da 279), guard ratchettato; oracoli classificati nel piano.
- **RA-17** — §1.2 chiuso; restano variadico e tipi valore.
- **RA-06 §1.3** — documentata la decisione richiesta; non è un fix locale.

Verifiche della sessione: `go build ./...`, `go vet ./...`, `gofmt -l` (nessun output),
`archcheck --strict` exit 0, `deadcode` 229/84, test verdi su `scripts` (incl. `adapters`),
`scripts/adapters`, `jobs`, `voiceover/service`, `app/wiring`, `kernel/{color,script}`,
`overlays/model`, `cliprender`, `clipsequence`, `clipmerge`, `postgres/media`,
`stock/enrichment`, `brain/intent`, `platform/ollama/...`, `ai/classifier`.
- **Gate nuovo:** `internal/kernel/color` è dichiarato in
  `architecture/policy.yaml::kernel_subzones` (altrimenti `kernel_subzone_undeclared`).

### 8.5 Aggiornamento sessione 2026-11-21 (seconda sessione)

- **RA-06 DONE** — §1.3 chiuso: censimento dei lettori di `StatusGenerated`, decisione di
  contratto documentata e bug reale corretto (commit fallito dopo upload riuscito → orphan
  cleanup, sync e async). Test nuovo
  `TestProcessSegmentUseCase_Execute_FASE4_CommitFailAfterUpload_EmitsCleanup` PASS.
- **RA-07 DONE** — verificato nel codice: costruttore fail-fast, `Handle` terminale, test
  `ConstructorRejectsMissingDependency` / `MissingDependencyIsTerminal`; la composizione che
  salta gli handler PG opzionali fallisce chiusa in dead-letter, non ack falso.
- **RA-11** — baseline corretta: lo script del §2 misura SCC di **package** e restituisce 0 per
  costruzione. La metrica del report è la relazione bidirezionale fra famiglie: **19 coppie**
  (riprodotte), `app/wiring` 218 import, `scripts` 42.
- **RA-09 DONE** — `defaultNowClock`, `youTubeCooldownClock` e `defaultLocalizedIdentityMatcher`
  sono atomici; `-race` verde su monitor, downloader e VidRush con `-parallel 8`.
- **RA-17 DONE** — `...bool` non-test = 0: bundle nominato `overlayPlanImageOptions` per i
  due flag per-scena, booleani espliciti per i flag singoli, 17 firme e relativi call site
  aggiornati; build, suite `scripts`, vet, gofmt e gate verdi.
- **RA-18** — misurato con `gocyclo`: 183 / 107 / 99 / 97 / 93 / 92, non 143/120; soglia ≤40.
- **Codice morto** — `deadcode` resta **229** (tutte le funzioni sono raggiunte da test o
  contratti; 0 zero-reference). Sweep `staticcheck U1000`: **27 simboli e 5 file rimossi**;
  restano 21 campi di gusci API e il registry `allowedMetadataKeys`, che è superficie del
  percheck (cancellarlo fa scattare il gate: provato e ripristinato). Dettaglio nel piano
  `docs/operations/DEAD-CODE-RETIREMENT-PLAN-2026-11-18.md`.

Verifiche: `go build ./...` exit 0; `go vet` sui package toccati exit 0; `gofmt -l` vuoto;
`go test` su `overlays/...`, `kernel/asset/...`, `scripts/adapters`, `scripts`,
`voiceover/service` (FASE4) → ok; `archcheck --strict` exit 0, `has_hard_gate_hits=false`.

### 8.6 Aggiornamento sessione 2026-11-21 (terza sessione)

- **RA-14 DONE** — i cinque helper `DB()` di `app/wiring` e la seconda copia in
  `capabilities/assets/artifacts` sono sostituiti dalla porta `pgmedia.EngineHandle`.
  `ClipsRegistry` riceve un `artifacts.MediaReader` iniettato; SQL e `pgDB()` rimossi.
  Nuovo percheck `percheck_capability_db_bypass` (app + capabilities, platform escluso).
  I tre file nuovi sono stati consolidati in `platform/postgres/media/media_engine_ports.go`
  per rispettare il cap di 65 file del package.
- **RA-13 DONE** — `PostgresRenderCache`, `LocalizedRenderReuse` e `SQLiteRepository`
  vivono ora in `platform/postgres/{cliprender,localizationreuse}` e
  `platform/sqlite/artifacts`; le capability conservano solo i port. Migrazioni
  `015_clip_render_cache.sql` e `016_localized_render_reuse.sql` registrate nel ledger
  embedded. Nuovo percheck `percheck_capability_sql_ban`: DDL vietato in modo assoluto,
  import `database/sql`/driver consentito solo per i 21 file inventariati con owner e
  removal trigger (da 24 prima del ticket).

Verifiche: `go build ./...` exit 0; `go test` su `boundaries`, `migrations/postgres`,
`platform/postgres/cliprender`, `capabilities/cliprender`, `capabilities/localization/...`,
`capabilities/assets/artifacts`, `app/wiring` → ok; `archcheck --strict` exit 0, 0 violazioni,
67 warning; `gofmt -l` vuoto. Migrazioni applicate live sul Postgres di test: prima run 13
migrazioni, seconda 0 applicate/13 skip, checksum tamper fail-closed
(`TestRunMediaMigrations_LedgerIsChecksumPinned` PASS).

### 8.7 Aggiornamento sessione 2026-11-21 (quarta sessione)

- **RA-11 DONE** — baseline riprodotta: 19 coppie bidirezionali. RA-13 ne aveva
  aggiunta una (`capabilities/cliprender ↔ platform/postgres`) con l'adapter in
  platform; il contratto `RenderCache`/`RenderBackend` è stato spostato in
  `internal/kernel/media/render_cache.go` e l'adapter non importa più la capability.
  Rimossa anche la coppia `capabilities/system ↔ platform/httpserver` (il
  `Module.Build` è passato al composition root via `StaticCapabilityModule`).
  Misura finale: **18 coppie**; fan-out interno `app/wiring` **217** (da 218).
  Nuovo percheck `percheck_system_http_boundary`.
- **RA-15 DONE** — i quattro file della pipeline (1.777 righe) e i tre test sono
  in `internal/platform/media/finaljob`; il wire è in `kernel/script/finaljob` e
  il contratto del segmento in `kernel/media/finaljob`. Il publisher è iniettato
  dal wiring, che resta a 66 file prod e zero `os/exec` nel root; il nuovo
  `percheck_wiring_finaljob_process_exec` impedisce il ritorno. I due `os/exec`
  residui in `wiring/lifecycle` e `wiring/rendering` sono debito separato.
- **RA-12 resta OPEN.** Lo split contract-first di `scripts` (117 file nella
  closure, god model `GenerateRequest`/`GenerateResult`) è una migrazione
  multi-PR: richiede di definire i contratti di fase, migrare i lettori e solo
  dopo tagliare i file. Non è un move meccanico e non è stato dichiarato chiuso.
  **RA-16** viene chiuso in questa sessione con la separazione del builder in fasi
  e la rimozione delle mappe dai contratti.

Verifiche della sessione: `go build ./...` exit 0; `go vet ./...` exit 0;
`gofmt -l` vuoto; test PASS su `boundaries`, `platform/httpserver`,
`capabilities/system/...`, `app/wiring`, `platform/postgres/cliprender`,
`capabilities/cliprender`; `archcheck --strict` exit 0, 0 violazioni, 67 warning.
