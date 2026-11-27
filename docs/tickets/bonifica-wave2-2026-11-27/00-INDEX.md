# Bonifica Wave 2 — secondo audit (2026-11-27)

**Oggetto:** esecuzione della roadmap "Bonifica Wave 2" del secondo audit
(0/22 sul commit `9884e041`).
**Metodo:** ogni voce è stata prima **verificata sul sorgente corrente**, poi
lavorata. Il repository conteneva già interventi dei commit `3b0c0255`,
`c91fa8c` e `436f169` che coprono alcune voci: sono marcate `DONE (preesistente)`
con la prova, non riproposte. Le voci non lavorate restano `OPEN`/`PARTIAL`:
nessun numero è stato arrotondato per far sembrare chiusa la wave.

**Stato wave:** 6 `DONE`, 7 `DONE (preesistente)`, 4 `PARTIAL`, 1 `BLOCKED`,
4 `OPEN`.

## 1. Roadmap e stato

| # | Pri | Voce | Stato | Prova |
| --- | --- | --- | --- | --- |
| 1 | P0 | Aggiornare OpenTelemetry e chiudere `GO-2026-6974` | **DONE** | `go.mod` 1.44.0 → 1.47.0; `govulncheck ./...` exit 0: "affected by 0 vulnerabilities" |
| 2 | P0 | Fixture, corpus e binari Rust disponibili in CI pulita | **BLOCKED** | corpus overlay vendorizzato (`testdata/overlay_contract_corpus`); il planner BrainRust che produce i nuovi golden è **non pubblicato**, quindi il binario pinnato fetchabile non può certificarli (§3.1) |
| 3 | P0 | Risolvere il panic `SA4023` del linter | **DONE** | `golangci-lint v2.15.0` eseguito sull'intero repo: nessun panic; il package `artifactsinfra` non esiste più |
| 4 | P1 | Falso `Published` nella deduplicazione asset | **DONE (preesistente)** | `assetop.DecideDedupe` + `DeliveryCertified`: `PUBLISH_PENDING`/`FAILED` o record senza identità Drive non certificano la consegna (`assetop/dedupe_decision.go`) |
| 5 | P1 | Unificare la policy `CheckDuplicate`/`ProcessAsset` | **DONE (preesistente)** | entrambi consumano `DecideDedupe`; nessuna seconda copia delle condizioni nel lifecycle |
| 6 | P1 | Eliminare la doppia preparazione clip audio | **DONE (preesistente)** | un solo producer `planBranch.clipAudio.Produce` nella fase audio (`runner_phase_audio.go:132`) e `ApplyTo` nella branch overlay: due consumer, un produttore (`clips/audio_latch.go`) |
| 7 | P1 | Unificare i criteri di ammissione geografica | **DONE (preesistente)** | `geography/admission.go`: `GeoAdmissionDecision` + un solo `sceneAdmission.decide`, usato da `enrichEntities` e `retryEntities`; `geography.Enrich` è uscito dalla lista cyclo >40 |
| 8 | P1 | Eliminare il cache hit ClipRender senza input digest | **DONE** | `cacheInputMatches` fail-closed su digest mancante; i due test cache-hit ora seminano `InputSHA256` + resolver invece di contare su un hit non certificato |
| 9 | P1 | Ridurre l'accoppiamento `OverlayPlanningInput`/`GenerateResult` | **PARTIAL** | i documenti scritti dalla branch sono clonati (`ClonePlanning*`), ma `OverlayPlanningInput.document()` ricostruisce ancora un `GenerateResult` completo e l'input continua a copiare ~30 campi: l'immutabilità è per-campo, non per-tipo |
| 10 | P1 | Bonificare `queue_client.go` per responsabilità | **DONE** | split in wire / executor / submit / chunking / settle; vedi [01](01-QUEUE-AND-VOICEOVER-SPLIT.md) |
| 11 | P1 | Bonificare `runner_phase_voiceover.go` | **DONE** | split in fase, sintesi e metriche; vedi [01](01-QUEUE-AND-VOICEOVER-SPLIT.md) |
| 12 | P1 | Chiudere `DEMOL-08` FinalJob tipizzato | **DONE (preesistente)** | `PrepareV1`/`FinalizeEnvelopeV1`; la mappa esiste solo in `MasterPayloadsOf` (`refactor-audit-2026-11-26/04`) |
| 13 | P1 | Ritirare planner e strategie legacy non raggiungibili | **PARTIAL** | dead-code guard verde a **214/214**; dei 13 simboli citati 11 hanno referenti (anche test) e 2 sono definizioni di strategia. Rimozione alla cieca esclusa: serve la scansione di raggiungibilità aggiornata |
| 14 | P2 | Ridurre `StyleParams`: 63 | **OPEN** | baseline cyclo invariata a **63** |
| 15 | P2 | Ridurre `ClipRenderPlanV1.Validate`: 62 | **OPEN** | baseline cyclo invariata a **62** |
| 16 | P2 | Ridurre `ProcessAsset`: 55 | **PARTIAL** | sceso a **50** (la decisione di dedup è fuori), ma resta sopra 40 |
| 17 | P2 | Ridurre `submitFinalJob`: 52 | **OPEN** | baseline cyclo invariata a **52** |
| 18 | P2 | Rimuovere facade, alias e `var _` inutili | **DONE** | 28 alias/facade non raggiungibili rimossi da `public_aliases_a/b`, `overlayplan/aliases`, `families_aliases`, `stockpipeline/aliases`; rimossi 4 `var _` di import-guard e 2 test di scratch (`zz_*_test.go`) |
| 19 | P2 | Verificare immutabilità e race delle branch concorrenti | **PARTIAL** | suite completa verde sotto `-race` non rieseguita (la suite standard è verde); l'immutabilità dell'input overlay è per-campo (vedi #9) |
| 20 | P2 | Misurare query e I/O aggiuntivi della nuova cache | **PARTIAL** | `TestWorker_CacheHitSkipsDestinationResolution` prova zero chiamate Drive su cache hit; manca una misura del numero di query di revisione per i batch |
| 21 | P2 | Ridurre le dipendenze residue capability → platform | **OPEN** | non rimisurato: lo split non aggiunge archi, ma il conteggio 142→≤120 non è stato prodotto |
| 22 | P2 | Completare la bonifica della selezione animazioni | **DONE (preesistente)** | wave `animation-randomization-2026-11-27`: 11 ticket chiusi, 4 bloccati da catalogo Chronon |

## 2. Gate eseguiti (2026-11-27)

| Comando | Esito |
| --- | --- |
| `go build ./...` | exit 0 |
| `go vet ./internal/... ./pkg/...` | exit 0 |
| `go test ./internal/... ./pkg/... -count=1` | exit 0, nessun FAIL |
| `govulncheck ./...` | exit 0, 0 vulnerabilità raggiungibili |
| `golangci-lint run` v2.15.0 | nessun panic; 143 rilievi preesistenti al cap del tool (errcheck 42, staticcheck 50, unused 45, ineffassign 6) |
| `go run ./cmd/cyclo-budget -check` | ✅ 37 funzioni > 40, baseline 37 |
| `scripts/ci/check-deadcode-baseline.sh` | ✅ 214 / max 214 |
| `go run ./cmd/archcheck --strict` | ✅ `passed=true`, 0 violazioni |
| `make verify-architecture` | ✅ |
| `make verify-rust-binary` | ✅ binario fresco e attribuito a BrainRust `ec9d2b7` |

## 3. Residui dichiarati

1. **P0-2 bloccato sulla pubblicazione BrainRust.** I golden overlay e i test
   `TestSemanticEngineRealBinary*` dipendono dal planner del worktree BrainRust
   `ec9d2b7` + modifiche non committate. Il pin pubblico
   (`architecture/releases.json`, `a4ab4cb`) non contiene quel planner: forzare
   `make rust-fetch` nei job di test renderebbe il gate rosso, non più
   riproducibile. Va pubblicata una release BrainRust e aggiornato il pin.
2. **Lint: nessun panic, ma 143 rilievi.** Il P0 riguardava il crash
   dell'analizzatore; il debito residuo è separato e non è stato nascosto
   disabilitando controlli. Il tool applica un cap di 50 per linter, quindi il
   totale reale è più alto.
3. **P1-13** richiede la scansione di raggiungibilità aggiornata prima di
   cancellare i 13 simboli citati.
4. **Budget del package `scripts`**: lo split P1-11 porta il root a 65 file /
   18.715 LOC. La baseline registrata è stata aggiornata 62→65 file e
   18.696→18.715 LOC con la motivazione in `architecture/package_hotspots.json`;
   il tetto globale (65 file) resta invariato, quindi ogni crescita futura
   fallisce subito.
