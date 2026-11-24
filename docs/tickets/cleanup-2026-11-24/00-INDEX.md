# Cleanup wave — 2026-11-24

**Data:** 2026-11-24
**Base misurata:** `refactored` @ `2a3ea0b2c` (working tree pulito, esclusi i ticket di questa wave)
**Origine:** radiografia 2026-11-24, Ondata 1.
**Stato aggiornato:** 2026-11-24 — Ondata 1 committata; **Ondata 2 interamente
chiusa**: CLEANUP-05 (rustexec 0), CLEANUP-06 (SQL 0, surface `pkg/sqlport`),
CLEANUP-07 (platform inventory 0).
**Perimetro di questa wave:** **Ondata 1** — dead code, helper test-only,
cinque cloni, `defer` nel loop — chiusa e verificata. I ticket esecutivi
dell'**Ondata 2** sono tutti CLOSED. Le Ondate 3–4 restano misurate in §5 e non
avviate.

## 1. Metodo

Ogni claim è stato riletto sul codice, non ereditato dal report. Per ogni
simbolo "morto" è stato eseguito `rg -w` su tutto il repository; per ogni clone
sono stati letti entrambi i corpi. I numeri del report che non corrispondono
sono corretti in §3.

## 2. Stato misurato

| Misura | Comando | Esito 2026-11-24 |
| --- | --- | --- |
| Gate architetturale | `go run ./cmd/archcheck --strict` | **exit 0**, `has_hard_gate_hits: false` |
| File prod root `capabilities/scripts` | `find … -maxdepth 1 -name '*.go' ! -name '*_test.go' \| wc -l` | **87** |
| Dead code verificato zero-reference | `rg -w '<simbolo>'` | **10** funzioni (9 del report + `numericValues` orfano) |
| Helper test-only | `rg -w '<simbolo>'` | **12** |
| Cloni esatti | confronto dei corpi | **5** coppie |
| `defer` in loop | lettura di `reachability_graph.go` | **1** sito |
| Inventario platform → scripts | `platformCapabilityScriptsInventory` | **0** entry (baseline 24: 13 `rustexec` + 11 platform) |
| Inventario SQL nelle capability | `capabilitySQLInventory` | **0** entry |
| Backup/binari locali | radiografia §2E | ~1,2 GB di backup in `bin/` + binari root, tutti gitignored |

## 3. Correzioni al report

1. **`capabilities/scripts` non è a 111 file prod.** Il root del package misura
   **87** file con `find -maxdepth 1`; i conteggi più alti del report includono
   la closure dei sottopackage. La baseline della wave è 87.
2. **Due alias "morti" sono in `public_aliases_a.go`**, non in
   `contract_aliases.go`: `checkedFloatSeconds` e
   `segmentExtractionExplicitlyEnabled`. La riga citata dal report non è
   affidabile; vale il grep.
3. **`numericValues` è un secondo orfano.** Dopo la rimozione di
   `numericEntityGroundedForLanguage` resta senza chiamanti: va rimosso nello
   stesso intervento, altrimenti il ticket sposta il dead code invece di
   eliminarlo.
4. **`fixedPlaybackDurationUS` ha una copia viva.** Quella del root `scripts`
   è morta; la copia in `scripts/workunits/fixed_playback.go` è usata da
   `workunits/scene_bindings.go:123` e **non va toccata**.
5. **`capSemanticVerificationCandidates` ha un gemello vivo** in
   `scripts/adapters/scene_annotations.go:301`: si sposta solo la copia
   `vidrush`, che è test-only.
6. **La pulizia disco non è un gate del repository.** I 25 backup da 1,2 GB e i
   binari root sono gitignored e non entrano in CI. Questa wave non esegue
   cancellazioni distruttive non richieste; la radiografia resta come igiene
   locale dell'operatore.
7. **`tmp_depscan/` è un artefatto non tracciato preesistente** (non di questa
   wave): non viene toccato.

## 4. Ticket

| ID | Area | Priorità | Stato |
| --- | --- | --- | --- |
| [CLEANUP-01](01-DEAD-CODE-ORPHANS.md) | 10 funzioni morte e import orfani | P1 | DONE (`b7a7ca2ec`) |
| [CLEANUP-02](02-TEST-ONLY-PRODUCTION-HELPERS.md) | 12 helper test-only fuori dai file di produzione | P1 | DONE (`d41dcdf52`) |
| [CLEANUP-03](03-DUPLICATE-HELPERS.md) | 5 cloni esatti verso i leaf package | P1 | DONE (`c6126a4ef`) |
| [CLEANUP-04](04-DEFER-IN-LOOP-REACHABILITY.md) | `defer` ridondante nel loop orphan | P2 | DONE (`d21cc4de9`) |
| [CLEANUP-05](05-ONDATA-2-RUSTEXEC-PORTS.md) | 13 adapter `rustexec` → contratti nel kernel | P1 | CLOSED (2026-11-24) — rustexec 0 |
| [CLEANUP-06](06-ONDATA-2-SQL-PORTS.md) | 21 file capability con `database/sql` → porte tipizzate | P1 | CLOSED (2026-11-24) — SQL inventory 0; surface `pkg/sqlport` |
| [CLEANUP-07](07-ONDATA-2-PLATFORM-ROWS.md) | 2 `ollama` + 9 `sqlite` → row types nel kernel | P1 | **CLOSED (2026-11-24)** — platform inventory 0 |

## 5. Ondata 2 chiusa; Ondate 3–4 fuori perimetro

- **Ondata 2 — svuotare gli inventari di archcheck.** CHIUSA: CLEANUP-05
  (13 `rustexec`, inventario 0), CLEANUP-06 (SQL 0) e CLEANUP-07 (platform
  inventory 0). Ogni slice ha spostato i contratti nel kernel o nella porta
  sanzionata `scripts/ports/run` e cancellato l'entry nello stesso cambio.
- **Ondata 3 — BON-01.** RA-12 è stato chiuso come SUPERSEDED il 2026-11-24:
  root `scripts` a **63 file**, SCC ≤63, gate verdi. Il residuo dei lettori di
  `GenerateRequest/GenerateResult` è ora in BON-01 §Riapertura 2026-11-24; il
  numero 87 del report è superato e non va usato come baseline.
- **Ondata 4 — god function e deadcode "decisionale".** RA-18 ha misurato
  183/107/99/97/93/92 con `gocyclo`; il budget deadcode è 229 (RA-03). Sono
  interventi che cambiano semantica o decisioni di prodotto (cablaggio vs
  rimozione), quindi non si eseguono come pulizia meccanica.

## 6. Check comuni

```bash
cd refactored
gofmt -l <file toccati>             # nessun output
go build ./...                      # exit 0
go test ./<package toccati>/... -count=1
go vet ./<package toccati>/...
go run ./cmd/archcheck --strict     # exit 0, has_hard_gate_hits=false
git diff --stat                     # solo il perimetro della wave
```

## 7. Regola di chiusura

Un ticket si chiude solo con: (a) diff che rispetta il perimetro, (b) check
finali eseguiti con output, (c) `archcheck --strict` ancora exit 0, (d) nessun
aumento dei file prod, del deadcode o degli inventari. I ticket 01–07 sono
tutti chiusi; le Ondate 3–4 restano programmi separati, misurati in §5 e non
avviati.

## 8. Esito (2026-11-24)

| Check | Esito |
| --- | --- |
| `gofmt -l` sui file toccati | nessun output |
| `git diff --check` | rc 0 |
| `go build ./...` | rc 0 |
| `go test` package stabili | PASS: subpackage `scripts`, `renderqueue`, `vidrush`, `cliprender`, `stockpipeline`, `jobs`, `clips`, `velox`, `sqlite`, `instaeditpublishing`, `youtube/jobs`, `mediamemory`, `brain/normalizer`, `pkg/*` |
| `go vet` superficie stabile | rc 0 |
| `go run ./cmd/archcheck --strict` | rc 0, `has_hard_gate_hits: false` |
| Test root `scripts` | PASS (20,4 s); sottopackage cleanup PASS |
| Commit | 6 commit separati (docs + 5 aree); vedi §9 |

Riverifica finale: `go build ./...` rc 0; `go test` su root `scripts` e sui
package cleanup PASS; `go run ./cmd/archcheck --strict` rc 0;
`python3 scripts/regen_hotspots.py --check` OK (root 63, target ≤65).
CLEANUP-01..04 chiusi e committati (vedi §9).

## 9. Chiusura wave e gate correnti (2026-11-24)

**HEAD riletto:** `e89a0a3dc` (include il commit concorrente sul final-job
subtitles). L'ultimo commit di questa wave è `d9e973e76`.

### 9.1 Commit

| Commit | Area | Ticket |
| --- | --- | --- |
| `db2d06c8a` | docs: gate Ondata 1, apertura Ondata 2 | CLEANUP-00…07 |
| `b7a7ca2ec` | dead code e helper morti | CLEANUP-01 |
| `d41dcdf52` | helper test-only nei `_test.go` | CLEANUP-02 |
| `c6126a4ef` | 5 cloni verso i leaf package | CLEANUP-03 |
| `d21cc4de9` | `defer` fuori dal loop orphan | CLEANUP-04 |
| `d9e973e76` | pre-work contratto frasi audio | CLEANUP-05 (non chiude) |

Il move `scripts → overlayplan` è nel commit concorrente `3e0a9e3ae`; la
bridge test-only `overlayplan_test_aliases_test.go` è committata e il root
compila senza alias di produzione.

### 9.2 Verifiche dopo i commit

| Check | Esito |
| --- | --- |
| `go build ./...` | rc 0 |
| `go test ./internal/capabilities/scripts/... -count=1` | PASS (root 20,038 s + tutti i sottopackage, incluso `runner/subtitles`) |
| `go vet ./internal/capabilities/scripts/...` | rc 0 |
| `go run ./cmd/archcheck --strict` | rc 0, `has_hard_gate_hits: false` |
| `gofmt -l` file toccati / `git diff --check` | nessun output / rc 0 |
| `go test ./... -count=1` | unico FAIL: `TestA1_ScaleBacklogInteractiveFirstClaimUnderBudget` (174,4 ms vs 100 ms sotto carico) |

`TestA1` passa nel package `wiring` da solo (19,521 s) e in isolamento 1× e 3×
(38,1 ms): è un test di latenza flaky sotto carico, non una regressione
funzionale. La suite completa non è dichiarata verde.

### 9.3 Ondata 2 — 05/06/07 chiusi

| Inventario | Entry | Stato |
| --- | ---: | --- |
| `platformCapabilityScriptsInventory` | 0 | inventario vuoto |
| `capabilitySQLInventory` | 0 | inventario vuoto |

CLEANUP-05 è chiuso: i 13 adapter `rustexec` importano `kernel/script/semantic`
e `kernel/audio`, l'inventario `rustexec` è vuoto e il kernel non importa
`capabilities`/`platform`. CLEANUP-06 è chiuso: `capabilitySQLInventory` è
vuoto, nessun import `database/sql` non-test nelle capability e nessun DDL
runtime; il surface è `pkg/sqlport`. CLEANUP-07 è chiuso: i contratti neutrali
(run/execution, render analytics, performance source, metadata, image gate e
verification) sono nel kernel; i DTO di persistenza legati a
`GenerateRequest/GenerateResult` sono nella porta `scripts/ports/run`; nessun
adapter platform importa più il package business.

### 9.4 Working tree

Al termine dei commit della wave il codice era pulito; durante questo
aggiornamento la sessione concorrente ha ripreso a modificare
`contract/model.go`, `contract/model_render.go`, `contract/model_result.go`,
`finaljob/final_job_payload.go`, `overlayplan/batch_overlay_plan.go` e
`overlayplan/overlay_plan_compile.go`. Quelle modifiche non sono di questa
sessione e non vengono toccate. Restano inoltre le modifiche concorrenti ai
ticket `refactor-audit-2026-11-21` (`00-INDEX`, `12-FIELD-INVENTORY`,
`12-SCRIPTS-SCC-SPLIT`); questo indice è aggiornato da questa sessione. `e89a0a3dc` è l'ultimo commit. Nessun push
eseguito.

## 10. Ondata 2 — esito 2026-11-24

| Ticket | Esito | Evidenza |
| --- | --- | --- |
| CLEANUP-05 | **CLOSED** | 13 adapter `rustexec` migrati; inventario `rustexec` 0 |
| CLEANUP-06 | **CLOSED** | inventario SQL 0; nessun `database/sql`/DDL nelle capability; `pkg/sqlport` |
| CLEANUP-07 | **CLOSED** | inventario platform 0; 07A/07B/07C completati; kernel foglia, build/vet/test e `archcheck --strict` verdi |

`archcheck --strict`: **rc 0**, `has_hard_gate_hits: false`. `go build ./...`,
`go vet` e `go test` sui package toccati (kernel, platform ollama/sqlite,
capability scripts, wiring, archcheck) sono rc 0. L'Ondata 2 è interamente
chiusa; nessun push eseguito.

## 11. Cosa resta (2026-11-24)

Nessun ticket della wave è aperto: 01–04 DONE e committati, 05–07 CLOSED con
inventari a zero. Non c'è lavoro bloccante per considerare chiusa la wave.

Restano solo attività non richieste o fuori perimetro:

| Voce | Stato | Note |
| --- | --- | --- |
| Commit/push del working tree | non eseguito | le modifiche di 05–07 sono nel working tree condiviso; nessun push |
| `go test ./...` completo | non dichiarato verde | unico FAIL noto: `TestA1_ScaleBacklogInteractiveFirstClaimUnderBudget` (latenza sotto carico); passa isolato |
| BON-01 / Ondata 3 | non avviato | spostamento di `GenerateRequest`/`GenerateResult` nel kernel; non serve per l'inventario platform |
| CLEANUP-06 — porte tipizzate per owner | follow-up architetturale | `pkg/sqlport` è il surface attuale; non era criterio di chiusura |
| Ondata 4 — god function | non avviata | interventi semantici, non pulizia meccanica |
| `gofmt` su tre file sqlite fuori scheda | debito locale | `assets/imagesregistry/index_request_committer.go`, `outbox/ports.go`, `outboxevents/repository.go`; non toccati da questa chiusura |
| Ticket `refactor-audit-2026-11-21` | modifiche concorrenti | non toccati da questa sessione |

Prossimo passo naturale, se richiesto: aprire BON-01 con il perimetro
`GenerateRequest`/`GenerateResult`; altrimenti la wave può restare chiusa così.
