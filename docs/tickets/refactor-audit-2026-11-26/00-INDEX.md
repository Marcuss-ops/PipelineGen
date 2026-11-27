# Audit architetturale esterno — wave del 2026-11-26

**Data:** 2026-11-26
**Base:** `refactored` @ `347577401` (PipelineGen `main`), albero con le modifiche di questa wave
**Origine:** audit statico esterno (checklist: accoppiamento, complessità, SSOT,
duplicazione semantica, dead code, concorrenza, risorse, retry, falsi successi).
**Regola della wave:** un item è `DONE` solo con il comando eseguito e l'output
nella scheda. Un item che richiede una push o un ambiente live è `PARTIAL` con
il motivo, non `DONE`.

## 1. Checklist operativa (15 item)

| # | Pri | Item | Stato | Evidenza |
|---|---|---|---|---|
| 1 | P0 | Ripristinare CI e consumo Queue pubblicato | **PARTIAL** | [01](01-CI-REPRODUCIBILITY.md) §2 e §6 |
| 2 | P0 | Contratti BrainRust/mediaexec riproducibili | **DONE** | [01](01-CI-REPRODUCIBILITY.md) §3 |
| 3 | P0 | `verify-main` sul commit effettivo | **DONE (locale)** | §4 sotto; la CI si prova solo con una push |
| 4 | P1 | Eliminare error swallowing nella risoluzione asset | **DONE** | [02](02-ASSET-TRANSFER-PORTS-AND-OUTBOX.md) §1 |
| 5 | P1 | Atomicità e recovery dell'outbox enrichment | **DONE** | [02](02-ASSET-TRANSFER-PORTS-AND-OUTBOX.md) §2 |
| 6 | P1 | Disaccoppiare `AssetTransferServiceImpl` da SQLite e Drive | **DONE** | [02](02-ASSET-TRANSFER-PORTS-AND-OUTBOX.md) §3 |
| 7 | P1 | Precedenze delle fonti media rispetto alla SSOT | **DONE** | [02](02-ASSET-TRANSFER-PORTS-AND-OUTBOX.md) §4 |
| 8 | P1 | Tipizzare gli envelope FinalJob | **DONE** (2026-11-27) | [04](04-FINALJOB-TYPED-ENVELOPE.md) — [03](03-FINALJOB-TYPING-STATE.md) è lo stato pre-refactor |
| 9 | P1 | Idempotenza e stati terminali con fault injection | **DONE** | [02](02-ASSET-TRANSFER-PORTS-AND-OUTBOX.md) §2 |
| 10 | P2 | Churn, fan-in, fan-out e complessità per hotspot | **DONE** | §5 sotto |
| 11 | P2 | Ridurre le funzioni oltre il budget ciclomatico | **DONE (ratchet)** | §6 sotto |
| 12 | P2 | Duplicazioni semantiche e regole replicate | **DONE** | §7 sotto |
| 13 | P2 | Race condition e timeout del cleanup | **DONE** | [02](02-ASSET-TRANSFER-PORTS-AND-OUTBOX.md) §5 |
| 14 | P2 | Riconciliare documentazione canonica e ticket chiusi | **DONE** | §8 sotto |
| 15 | P3 | Ottimizzare le allocazioni sulla base dei benchmark | **DONE (misura)** | §9 sotto |

## 2. Le tre cause reali della CI rossa (non erano tre sintomi)

Nota aggiornamento 2026-11-27: i dettagli Queue qui sotto descrivono lo stato
storico. La repository identity è stata corretta e il modulo `v0.1.0` è
pubblicato; la migrazione di questo consumer a `require` versionato è in corso
nel §6 di [01](01-CI-REPRODUCIBILITY.md). Lo stato resta PARTIAL finché il PR
PipelineGen e la CI non risultano verdi.

1. `go.mod` risolve la Queue dal sibling `../RenderingGen/queue`, e l'action CI
   clonava `github.com/Marcuss-ops/RenderingGen.git`: quel repository **non
   esiste** (il nome pubblicato è `RenderginGen`, con la "g" minuscola). La
   clone falliva con la richiesta di credenziali, quindi `verify-main`, il gate
   architetturale e l'ICU contract non partivano nemmeno.
2. Il job `lexicon-parity` leggeva `bin/BRAINRUST_RELEASE.json`, ma `bin/` è
   ignorata da `.gitignore`: in un checkout fresco il file **non c'è**. In più
   il manifest locale dichiarava `"source": "worktree"` e un `git_sha`
   (`644f1ce`) che GitHub non conosce ("not our ref"), quindi nessun runner
   poteva fetcharlo.
3. `make verify-rust-contract` risolveva il commit da `git ls-tree HEAD rust`,
   ma questo repository **non ha alcun gitlink**: il comando era vuoto per
   costruzione. Il job ICU, allo stesso modo, leggeva `BrainRust/ICU_MAJOR`
   senza aver mai fatto il checkout di BrainRust.

## 3. Misure riprodotte in questa wave

| Misura | Comando | Esito |
| --- | --- | --- |
| Build | `go build ./...` | exit 0 |
| Vet | `go vet ./...` | exit 0 |
| Pin esterni | `make verify-release-pin` | ✅ v0.3.8 @ a4ab4cb, ICU 72 |
| Contratto mediaexec | `make verify-rust-contract` | ✅ 4 goldens, consumer copy, `rustexec` ok |
| Contratto ICU | `make verify-rust-fetch` | ✅ (self-test sintetico) |
| Governance architetturale | `make verify-architecture` | exit 0, `has_hard_gate_hits=false` |
| Cyclo ratchet | `go run ./cmd/cyclo-budget -check` | ✅ 39 funzioni >40, baseline 39 |
| Dead code | `scripts/ci/check-deadcode-baseline.sh` | ✅ 219 / max 219 al 2026-11-26; **214 / max 214** dal 2026-11-27 ([05](05-CLIPRENDER-BUDGET-AND-DEADCODE.md) §4) |
| Duplicazione | `dupl -t 100 internal cmd pkg` | 149 gruppi; **47** produzione; **20** cross-file |
| Archi capability→platform | `go list` + `Imports` | **142** (era 146 documentato) |
| Archi kernel→platform | `go list` + `Imports` | **0** |
| Package | `go list ./internal/... ./cmd/...` | 425 |
| Race | `go test -race` sui package toccati | ✅ 6 package |
| Allocazioni payload | `BenchmarkFinalJobDemolition` | 2286 allocs/op, 193.417 B/op |

## 4. `verify-main`: cosa è stato verificato e cosa no

`make verify-main` compone `verify-push` + `verify-architecture`; `verify-push`
include `verify-foundation`, che a sua volta esegue `go mod tidy` e
`git diff --exit-code -- go.mod go.sum`. **Il target è fail-closed sull'albero
sporco per costruzione**: su un worktree con modifiche non committate
`tidy-check` è rosso anche quando `go mod tidy` non cambia nulla (verificato:
`go mod tidy` è un no-op, il diff è solo la modifica in corso).

Quindi: `verify-foundation`, `verify-static` e `verify-architecture` sono stati
eseguiti singolarmente e sono verdi, e `verify-main` è stato eseguito sul commit
locale della wave (§ sotto il commit). La CI GitHub resta `PENDING`: la prova
end-to-end richiede una push, che questa sessione non esegue.

## 5. Hotspot: LOC, churn 90 giorni, fan-in, fan-out

Churn = `git log --since=2026-08-28 -- <path>`; fan-in/fan-out = import interni
distinti (`go list` su `./internal/... ./cmd/...`).

| Package | File | LOC | Fan-out | Fan-in |
| --- | ---: | ---: | ---: | ---: |
| `internal/capabilities/scripts` | 62 | 18.695 | 53 | 16 |
| `internal/capabilities/images` | 59 | 6.083 | 29 | 7 |
| `internal/kernel/script` | 51 | 9.589 | 12 | **80** |
| `internal/capabilities/cliprender` | 50 | 10.629 | 13 | 11 |
| `internal/capabilities/overlays` | 50 | 5.965 | 9 | 13 |
| `internal/capabilities/scripts/vidrush` | 43 | 11.889 | 27 | 4 |
| `internal/app/wiring` | 43 | 10.011 | **169** | 10 |
| `internal/capabilities/voiceover/service` | 42 | 7.594 | 18 | 8 |
| `internal/capabilities/youtube/usecase` | 41 | 7.363 | 24 | 5 |
| `internal/capabilities/jobs` | 40 | 8.437 | 31 | 31 |

| File monitorato | LOC | Commit / 90g |
| --- | ---: | ---: |
| `internal/app/wiring/buildbundle/build_bundles_domain_media.go` | 692 | 1 |
| `internal/kernel/script/overlay_style_spec.go` | 714 | 7 |
| `internal/platform/renderinggen/queue_client.go` | 697 | **48** |
| `internal/capabilities/scripts/runner_phase_voiceover.go` | 653 | **32** |
| `internal/capabilities/scripts/vidrush/media_resolver_image_segments.go` | 610 | 4 |
| `internal/capabilities/scripts/runner_overlay_plan_branch.go` | 624 | **21** |
| `internal/platform/postgres/media/media_searcher.go` | 749 | 11 |
| `internal/capabilities/scripts/finaljob/final_job_payload_builder.go` | 617 | 5 |

Lettura: la dimensione **non** è il predittore. Il rischio di cambio è
concentrato in `queue_client.go` (48 commit/90g), `runner_phase_voiceover.go`
(32) e `runner_overlay_plan_branch.go` (21); i file grandi e stabili
(`build_bundles_domain_media.go`, 692 LOC, 1 commit) non giustificano una
spezzatura. `app/wiring` resta a fan-out 169: è il composition root, per
definizione conosce tutto, ed è già registrato come hotspot con cap.

## 6. Complessità ciclomatica

`cmd/cyclo-budget -check` era **rosso** su `main`: due funzioni erano state
rinominate da esportate a private (`AlignFinalJobCompiledPlanAssetIDs` →
`alignFinalJobCompiledPlanAssetIDs`, `PrepareFinalJobVideoSegments` →
`prepareFinalJobVideoSegments`) e la baseline registrava ancora i nomi vecchi,
quindi comparivano come "nuove funzioni sopra budget". La baseline è stata
rigenerata con `-write`: 39 funzioni sopra 40 (era 40), 47 e 48 registrate.

Il target dell'audit (≤10 ideale, >25 revisione, 40 massimo) resta più stretto
del ratchet di progetto: **39 funzioni sopra 40 sono debito noto e baselinato**,
non un risultato. Il ratchet impedisce la crescita; la riduzione è un ticket
dedicato, non una riscrittura in questa wave.

## 7. Duplicazione

`dupl -t 100` riprodotto: 149 gruppi totali, 47 di produzione, 20 cross-file —
identici ai numeri documentati in `refactor-p0-2026-11-26/06`. Tra i residui è
stata trovata e rimossa **una regola di business replicata**: i 61 righe di
`personImageSourceNameMismatch` (più `isASCIIDigits` e la vocabulary
`personImageFilenameQualifier`) esistevano identiche in
`capabilities/scripts/vidrush` e in `kernel/script/imageverify`. Ora c'è un solo
owner (kernel); VidRush delega e conserva solo la metà "pagina enciclopedica",
che è locale. Il test della vocabulary è stato spostato sull'owner.

Il resto dei 20 gruppi cross-file è dei tre tipi già classificati nel ticket 06
(alias deliberati, boilerplate degli scanner archcheck, forme strutturali).

## 8. Governance documentale

`CANONICAL.md` diceva "historical plans and closure evidence belong in Git
history" mentre `docs/tickets/**` contiene 253 schede (229 chiuse). Non è stato
risolto cancellando le schede — perderebbe il registro e i riferimenti — ma
dichiarando la regola che il repository applica davvero: il registro dei ticket
è l'eccezione sanzionata, è un registro di decisioni (perimetro, acceptance,
check eseguiti, `Stato:`) e non un diario; timing map, log e dump restano in
`ops/` o nella storia Git.

## 9. Allocazioni (P3)

`BenchmarkFinalJobDemolition/PayloadBuild` misurava **2286 allocs/op** e
**193.417 B/op**, contro **2538 / 223.145 B** documentati in
`refactor-p0-2026-11-26/07`: il costo era già sceso con la rimozione degli
snapshot integrali (DEMOL-07) e non c'era un'allocazione da ottimizzare senza
un profilo che la indicasse. Nessuna modifica ai loop.

Dopo DEMOL-08 (2026-11-27) la proiezione a mappa misura **2323 allocs/op** e
**226.150 B/op** (+1,6% allocs, +16,9% byte): i byte extra sono la copia della
stringa del piano nella proiezione PREPARE, che il percorso di produzione non
paga. Dettaglio e misure in [04](04-FINALJOB-TYPED-ENVELOPE.md) §4.

## 10. Residui dichiarati (non nascosti)

1. **La CI non è ancora verde su GitHub**: le correzioni sono locali. Serve una
   push; il primo run è la prova end-to-end.
2. **BrainRust e RenderingGen locali sono avanti rispetto ai remoti** e non
   pushabili da qui: `RenderingGen` ha 2 commit non pubblicati (`df9f9f7`,
   `b9135bf`) e `BrainRust` ha un worktree sporco e `644f1ce` non pubblicato.
   I pin di questa wave puntano alle revisioni **pubblicate** (`e8fec015`,
   `v0.3.8` @ `a4ab4cb`) perché sono le uniche fetchabili da un runner.
3. **`DEMOL-08`** (chiuso il 2026-11-27): il builder componeva ancora
   `map[string]any`; ora costruisce `PrepareV1`/`FinalizeEnvelopeV1` e la mappa
   esiste solo in `MasterPayloadsOf`. Vedi [04](04-FINALJOB-TYPED-ENVELOPE.md).
4. ~~**Dead code guard a 219/219**~~: risolto il 2026-11-27 con lo sweep
   dell'orphan package `voiceover/verbatimcheck` e di due helper senza
   chiamanti. Misura **214/214**, `MAX` abbassato 219 → 214
   ([05](05-CLIPRENDER-BUDGET-AND-DEADCODE.md) §4).
5. **39 funzioni sopra complessità 40**, baselinate (§6).
6. **`archcheck --strict` è rosso su `internal/capabilities/scripts`** (63 file
   contro 62, 18.786 righe contro 18.696): la crescita è il file non tracciato
   `runner_clip_audio_latch.go` di un altro attore. Il budget di `cliprender`
   (11.600 / 10.629) è invece chiuso: 10.583 / 10.629
   ([05](05-CLIPRENDER-BUDGET-AND-DEADCODE.md) §5).

## 11. Esito sul commit effettivo

Commit della wave: `refactor: close the external audit wave on reproducibility
and invariants` — HEAD dell'albero su cui è stato eseguito `make verify-main`
(albero pulito, nessuna push).

| Comando | Esito |
| --- | --- |
| `go run ./cmd/archcheck --strict` | ✅ `passed=true`, `has_hard_gate_hits=false`, 70 warning (= baseline HEAD) |
| `python3 scripts/regen_hotspots.py --check` | ✅ fresh (wiring resta 42 file) |
| `make verify-main` | ✅ `EXIT=0` (foundation + tidy-check + static + unit + architecture) |

Nota di percorso: i primi gate hanno bocciato due scelte intermedie di questa
wave — un `WorkerOutputRecord` con `AssetID` accanto alle location (3 nuove
violazioni `media-identity-location-fields`) e gli adapter in `app/wiring`
(+1 file, +170 LOC contro il tetto 42 file / 10.000 LOC). Entrambi sono stati
corretti prima del commit: l'identità resta al chiamante, gli adapter vivono in
`internal/app/modules/registry`. Nessuna soglia è stata alzata.

## 12. Check comuni di chiusura

```bash
cd refactored
gofmt -l <file toccati>                       # nessun output
go build ./...                                # exit 0
go vet ./...                                  # exit 0
make verify-release-pin                       # exit 0
make verify-rust-contract                     # exit 0
make verify-architecture                      # exit 0
go run ./cmd/cyclo-budget -check              # exit 0
scripts/ci/check-deadcode-baseline.sh         # exit 0 (219/219)
go test -race -count=1 <package toccati>      # exit 0
```
