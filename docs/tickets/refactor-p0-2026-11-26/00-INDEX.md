# Refactor P0 — wave del 2026-11-26

**Data:** 2026-11-26
**Base audit:** `refactored` @ `81dc06bca` (working tree pulito)
**Verifica finale:** `refactored` @ `cd43abdb7` (working tree P0 + modifiche esterne non tracciate)
**Origine:** audit del 2026-11-26 sulla checklist di refactoring (accoppiamento,
complessità, risorse, dead code, duplicazione, retry/falso successo).
**Vista:** questo indice è la vista di wave; ogni ticket è la fonte proprietaria
del proprio perimetro, della propria evidenza e dei propri check.

## 1. Perimetro di questa wave

Tre difetti P0 sono chiusi qui con codice e test:

1. il wiring dei render localizzati deve fallire **al dispatch**, non a fine run;
2. `jobs/queue.(*Service).Enqueue` (cyclo **73**) va ridotto sotto la soglia di
   progetto **40** senza cambiare comportamento;
3. `overlays/model.(*MapOverlay).validateCameraMove` (cyclo **77**) e
   `Validate` (cyclo **58**) vanno spezzati in validator nominati.

Un quarto ticket rende il budget di complessità **eseguibile** (ratchet), invece
di lasciarlo una frase nella documentazione.

I residui strutturali (accoppiamento, duplicazione, dead code, allocazioni) sono
ticket `OPEN` con acceptance misurabile: non sono nascondibili in questa
sessione e non vengono dichiarati chiusi.

## 2. Misure riprodotte (2026-11-26)

| Misura | Comando | Esito |
| --- | --- | --- |
| LOC Go di produzione | `find internal cmd pkg -name '*.go' ! -name '*_test.go' \| xargs cat \| wc -l` | **535.634** |
| File di produzione >1.000 righe | `wc -l` su tutti i non-test | **0** (max **795**) |
| Package | `go list ./... \| wc -l` | **459** |
| Complessità media produzione | `gocyclo internal cmd pkg` (no test) | **5,23** su 14.095 funzioni |
| Funzioni produzione >40 | `gocyclo -over 40` (no test) | **40** |
| Funzioni produzione >25 | `gocyclo -over 25` (no test) | **229** |
| Dead code dai 3 main | `deadcode -test=false ./cmd/{server,worker,admin}` | **267** (266 dopo lo sweep; +1 esterna) |
| Dead code guard (produzione) | `scripts/ci/check-deadcode-baseline.sh` | **216** correnti / max **219** → **GREEN** (4 untracked escluse) |
| Cloni `dupl -t 100` | `dupl -t 100 internal cmd pkg` | 149 totali; **47** produzione, **20** cross-file |
| Archi `capabilities → platform` | `go list` + `Imports` | **157** da **81** package (**146** dopo P1-05) |
| `var` mutabili tipizzati | `rg '^var .*(map\\[|\\[\\]|\\*|sync\\.|atomic\\.)'` | **14** |
| Hard gate architetturale | `go run ./cmd/archcheck --strict` | **0** dopo P1-09 (2 sulla HEAD `cd43abdb7`, 0 sulla base audit) |

I due numeri di dead code non sono equivalenti: `267` è la misura corrente dei
tre entrypoint (`266` dopo il ritiro P2-08; una funzione è arrivata dal WIP
esterno), `219` è la baseline congelata del guard di produzione. Il guard
corrente misura **216** funzioni tracciate (4 entry untracked esterne sono
segnalate e non contano) e resta sotto il max **219**; il test-support
`pgtest`, `sqltest`, `testsupport`, `mutationprobe` è inventariato fuori dal
guard. Allo sweep la vista completa era **246** e quella test-aware **82**; la
riconciliazione e la classificazione sono nel ticket 08.

## 3. I ticket

| ID | Area | Priorità | Stato |
| --- | --- | --- | --- |
| [P0-01](01-LOCALIZED-RENDER-FAIL-FAST.md) | Render localizzato: no-op nil → errore tipizzato + preflight | P0 | **DONE** |
| [P0-02](02-QUEUE-ENQUEUE-COMPLEXITY.md) | `queue.Enqueue` cyclo 73 → helper nominati | P0 | **DONE** |
| [P0-03](03-MAP-CAMERA-VALIDATION-COMPLEXITY.md) | `MapOverlay` camera validation 77/58 → validator | P0 | **DONE** |
| [P0-04](04-CYCLO-RATCHET.md) | Ratchet cyclo eseguibile con baseline | P0 | **DONE** |
| [P1-09](09-ARCHCHECK-HARD-GATE-RESIDUAL.md) | Archcheck: budget `scripts` + `kernel/jsonwire` | P1 | **DONE** |
| [P1-05](05-CAPABILITY-PLATFORM-RESIDUAL.md) | Archi capability→platform: 157→146, coppie 18→17 | P1 | **DONE** |
| [P2-06](06-DUPLICATION-RESIDUAL.md) | 28 → 20 gruppi clone cross-file, owner condivisi | P2 | **DONE** |
| [P2-07](07-ALLOCATION-PROFILING.md) | Allocazioni nei loop: misura assente | P2 | **DONE (misura)** |
| [P2-08](08-DEADCODE-ROOT-RECONCILIATION.md) | 267/216/219: riconciliare i root set, classificare e ritirare | P2 | **DONE** |

## 4. Check comuni di chiusura

```bash
cd refactored
gofmt -l <file toccati>                       # nessun output
go build ./...                                # exit 0
go vet ./...                                  # exit 0
go test ./internal/capabilities/scripts/... -count=1
go test ./internal/capabilities/jobs/queue/... -count=1
go test ./internal/capabilities/overlays/model/... -count=1
go run ./cmd/archcheck --strict               # exit 0, 0 violazioni (P1-09)
go run ./cmd/cyclo-budget -check             # exit 0
make verify-cyclo                            # exit 0
```

`archcheck` è verde: `internal/kernel/jsonwire` è dichiarato in
`architecture/policy.yaml` e il root `scripts` è sceso da 18.999 a 18.749 righe
(spostando la proiezione pura in `scripts/modelvalues` e le aspettative render
in `scripts/renderunits`) senza alzare `architecture/package_hotspots.json`.
Anche `go run ./cmd/cyclo-budget -check` è verde (40/40) dopo lo split di
`cmd/admin/rehydrate_ovl06.main`. La scheda P1-09 è la fonte proprietaria
dell'evidenza.

`verify-main`, `verify-unit` e le suite live non sono eseguite qui: richiedono
Postgres/Master/worker reali. Ogni ticket che ne avrebbe bisogno lo dichiara
come `BLOCKED-ENV`, non come PASS.

## 5. Regola di chiusura

- Un ticket `DONE` ha il diff, il test e l'output dei check nella scheda.
- Un ticket `OPEN` ha acceptance verificabile e nessun numero ereditato.
- Nessun ticket di questa wave alza il guard del dead code o la baseline cyclo.
- Un problema fuori perimetro apre una scheda nuova, non allarga quella corrente.

## 6. Stato finale e prossimi passi (2026-11-26)

Chiusi con evidenza: **P0-01, P0-02, P0-03, P0-04, P1-05, P1-09, P2-06,
P2-07, P2-08**.

Aperti in questa wave: **nessuno**.

Bonifica operativa 2026-11-26 (Taglio 5 payload + Taglio 6 disco/systemd/DB):
`docs/tickets/ops-hardening-2026-11-26/00-INDEX.md`.

Verifica finale eseguita:

```text
go build ./...                                      → exit 0
go vet <package toccati>                            → exit 0
go test scripts overlays/model renderinggen cyclo  → tutti ok
go test capabilities + platform + app -count=1      → exit 0 (P1-05, P2-06)
dupl -t 100 internal cmd pkg                        → 149/47/20 (P2-06)
make verify-cyclo                                   → exit 0 (40/40)
archcheck --strict                                  → exit 0, 0 violazioni
deadcode guard                                      → GREEN 216 / 219 (P2-08; 4 untracked escluse)
```

Prossimi passi, in ordine di rischio:

1. residuo `dupl` (20 gruppi): boilerplate `WalkDir` degli scanner archcheck e
   alias deliberati; aprire un ticket dedicato solo se si vuole scendere
   sotto 20;
2. rieseguire `make verify-main` quando il working tree esterno è stabile.
