# Cleanup wave — 2026-11-24

**Data:** 2026-11-24
**Base misurata:** `refactored` @ `2a3ea0b2c` (working tree pulito, esclusi i ticket di questa wave)
**Origine:** radiografia 2026-11-24, Ondata 1.
**Perimetro di questa wave:** **Ondata 1** — dead code, helper test-only,
cinque cloni, `defer` nel loop — chiusa e verificata. I ticket esecutivi
dell'**Ondata 2** sono aperti (CLEANUP-05/06/07); la migrazione è **OPEN** e non
si dichiara chiusa a metà. Le Ondate 3–4 restano misurate in §5 e non avviate.

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
| Inventario platform → scripts | `platformCapabilityScriptsInventory` | **24** entry, di cui **13** `rustexec` |
| Inventario SQL nelle capability | `capabilitySQLInventory` | **21** entry |
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
| [CLEANUP-01](01-DEAD-CODE-ORPHANS.md) | 10 funzioni morte e import orfani | P1 | DONE (perimetro) |
| [CLEANUP-02](02-TEST-ONLY-PRODUCTION-HELPERS.md) | 12 helper test-only fuori dai file di produzione | P1 | DONE (perimetro) |
| [CLEANUP-03](03-DUPLICATE-HELPERS.md) | 5 cloni esatti verso i leaf package | P1 | DONE |
| [CLEANUP-04](04-DEFER-IN-LOOP-REACHABILITY.md) | `defer` ridondante nel loop orphan | P2 | DONE (modifica) |
| [CLEANUP-05](05-ONDATA-2-RUSTEXEC-PORTS.md) | 13 adapter `rustexec` → contratti nel kernel | P1 | OPEN |
| [CLEANUP-06](06-ONDATA-2-SQL-PORTS.md) | 21 file capability con `database/sql` → porte tipizzate | P1 | OPEN |
| [CLEANUP-07](07-ONDATA-2-PLATFORM-ROWS.md) | 2 `ollama` + 9 `sqlite` → row types nel kernel | P1 | OPEN |

## 5. Fuori perimetro (misurato, non avviato)

- **Ondata 2 — svuotare gli inventari di archcheck.** Ticket aperti:
  CLEANUP-05 (13 `rustexec`), CLEANUP-07 (2 `ollama` + 9 `sqlite`) e
  CLEANUP-06 (21 file SQL). La migrazione è **OPEN**: ogni slice sposta i
  contratti nel kernel e cancella l'entry di inventario nello stesso cambio;
  nessuna entry va rimossa senza codice migrato.
- **Ondata 3 — RA-12.** `refactored/docs/tickets/refactor-audit-2026-11-21/12-SCRIPTS-SCC-SPLIT.md`
  resta OPEN: la closure SCC era 118 e la migrazione dei lettori di
  `GenerateRequest/GenerateResult` non è iniziata. Il root è a 87 file, non a
  111: il numero del report va riallineato prima di fissare il target ≤65.
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
aumento dei file prod, del deadcode o degli inventari. I ticket 01–04 si
chiudono in questa sessione; le Ondate 2–4 restano aperte come programmi
separati.

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
| Commit | non eseguito: nessun commit richiesto; il tree resta condiviso |

Riverifica finale: `go build ./...` rc 0; `go test` su root `scripts` e sui
package cleanup PASS; `go run ./cmd/archcheck --strict` rc 0;
`python3 scripts/regen_hotspots.py --check` OK (root 63, target ≤65).
CLEANUP-01..04 chiusi. Nessun commit.

## 9. Aggiornamento 2026-11-24 (move overlayplan e gate correnti)

- HEAD `3e0a9e3ae`: il move `scripts → overlayplan` è committato dalla
  sessione concorrente; la bridge test-only `overlayplan_test_aliases_test.go`
  è stata ripristinata e il root compila.
- `go build ./...` rc 0; `go test ./internal/capabilities/scripts/... -count=1`
  PASS (root 24,185 s + tutti i sottopackage);
  `go vet ./internal/capabilities/scripts/...` rc 0;
  `archcheck --strict` rc 0, `has_hard_gate_hits: false`.
- `go test ./... -count=1`: unico FAIL
  `TestA1_ScaleBacklogInteractiveFirstClaimUnderBudget` in
  `internal/app/wiring` (174,4 ms vs budget 100 ms sotto carico parallelo).
  Il package `wiring` da solo PASS (19,521 s); il test A1 passa 1× e 3× in
  isolamento (38,1 ms). È un test di latenza flaky, non una regressione
  funzionale.
- Commit: i ticket sono committati separatamente; il codice della wave resta
  **non committato** perché il working tree contiene modifiche concorrenti
  (`overlayplan_facade.go`, `overlayplan_test_aliases_test.go`,
  `jobs/extracted_compat.go`). Non si fa un commit selettivo che includa
  lavoro altrui.
- Ondata 2: CLEANUP-05/06/07 aperti; nessuna migrazione dichiarata chiusa.
