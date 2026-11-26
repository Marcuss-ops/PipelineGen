# P1-09 — Archcheck hard gate: package scripts + kernel/jsonwire

**Data:** 2026-11-26
**Priorità:** P1 (gate hard rosso, ma fuori dal perimetro dei tre P0)
**Stato:** **DONE**
**Area:** `architecture/package_hotspots.json`, `architecture/policy.yaml`,
`internal/capabilities/scripts`, `internal/kernel/jsonwire`

## 1. Problema verificato

`go run ./cmd/archcheck --strict` su HEAD `cd43abdb7` (working tree con le
modifiche P0) esce **1** con due violazioni:

```json
{
  "summary": {
    "total_violations": 2,
    "by_reason": {
      "kernel_subzone_undeclared": 1,
      "pkg_size": 1
    },
    "by_severity": { "error": 2 }
  },
  "violations": [
    {
      "directory": "internal/kernel/jsonwire",
      "matched_rule": "kernel_subzone_undeclared",
      "severity": "error"
    },
    {
      "package": "internal/capabilities/scripts",
      "actual_lines": 18999,
      "max_lines": 18957,
      "matched_rule": "package_line_budget_growth",
      "severity": "error"
    }
  ]
}
```

Il budget `internal/capabilities/scripts` era **già superato a HEAD**:
18.966 righe contro 18.957 (+9). Le modifiche P0-01 aggiungono +31 righe di
produzione (fail-fast + preflight) e le modifiche esterne già presenti nel
working tree aggiungono +2, portando a 18.999. Non è corretto alzare
`max_loc`: la regola del repo è che il budget scende, non sale.

`internal/kernel/jsonwire` è una directory **non tracciata** comparsa nel
working tree (`?? internal/kernel/jsonwire/`, `strict.go`); non è dichiarata in
`architecture/policy.yaml` e non appartiene a questa wave.

Il warning `cmd_main_max_lines` su `cmd/cyclo-budget/main.go` è stato chiuso
dividendo il comando in `main.go` (90 righe), `scan.go`, `baseline.go` e
`check.go`.

## 2. Soluzione

1. `internal/kernel/jsonwire` è dichiarato nella subzona kernel di
   `architecture/policy.yaml` (`kernel_subzones: ..., overlayplan, jsonwire`).
   La dichiarazione era già presente nel working tree ed è stata verificata:
   il package possiede il primitivo di decode strict condiviso da
   `kernel/script` e `kernel/media`, quindi è una subzona kernel legittima.
2. `internal/capabilities/scripts/durable_mapper.go` (221 righe, proiezione
   pura `contract.GenerateResult` → `kernel/script.GenerationResult`) è stato
   spostato in `internal/capabilities/scripts/modelvalues/durable_mapper.go`.
   Il package `modelvalues` importa già `contract` e `kernel/script` e non
   importa il root: nessun ciclo. Il root conserva la superficie pubblica con
   `durable_mapper_facade.go` (15 righe) che delega a
   `modelvalues.DurableResultToDomain`.
3. `architecture/package_hotspots.json` non è stato toccato: il budget
   `18.957` resta invariato e il root scende a **18.793** righe.

## 3. Acceptance

- [x] `go run ./cmd/archcheck --strict` esce **0** con `has_hard_gate_hits: false`.
- [x] `internal/capabilities/scripts` torna a `≤ 18.957` righe **senza alzare**
  `architecture/package_hotspots.json`.
- [x] `internal/kernel/jsonwire` è dichiarato nella subzona kernel di
  `architecture/policy.yaml`.
- [x] Nessuna nuova voce in `architecture/cyclo_budget.json` e nessun aumento
  del guard dead code.

## 4. Evidenza

- HEAD `cd43abdb7`: scripts 18.966 / 18.957 (pre-esistente +9).
- Working tree P0: scripts 18.999 / 18.957 (+31 P0-01, +2 esterno).
- Dopo il move: scripts **18.793 / 18.957** (`find ... -maxdepth 1 ... | wc -l`).
- `go build ./internal/capabilities/scripts/...` → exit 0.
- `go vet ./internal/capabilities/scripts/...` → exit 0.
- `go test ./internal/capabilities/scripts ./internal/capabilities/scripts/modelvalues -count=1`
  → `ok ... 41.485s`; `modelvalues` non ha test propri, ma la facciata root è
  coperta da `durable_mapper_test.go`.
- `gofmt -l` sui due file toccati → nessun output.
- `/tmp/archcheck --strict` → `exit 0`, `total_violations: 0`,
  `has_hard_gate_hits: false`.

**Verifica finale congiunta (stesso working tree, dopo P0-04).**

- Le aspettative e l'identità render — `ErrRendererNotWired`,
  `LocalizedRenderRequested`, `RecordExpectations`,
  `RenderSetSatisfiesExpectation`, `DedupeLocalizedRenders` — sono state
  estratte in `internal/capabilities/scripts/renderunits/render_expectations.go`
  e il root le ri-esporta in `public_aliases_b.go`. Il root scende a
  **18.749 / 18.957** (208 righe di headroom), sempre senza toccare
  `architecture/package_hotspots.json`.
- `go run ./cmd/cyclo-budget -check` →
  `✅ cyclo budget passed: 40 functions above 40, 40 baseline entries` (exit 0).
  `cmd/admin/rehydrate_ovl06.main` (cyclo 42, entrato nel working tree dopo la
  baseline) è stato diviso in `loadManifest`, `startRuntime`, `rehydrateOne`,
  `download`, `resolveDuration` e `buildClip`, senza nuove voci in
  `architecture/cyclo_budget.json`.
- `go build ./...` → exit 0; `go vet ./internal/capabilities/scripts/... ./cmd/admin/rehydrate_ovl06/`
  → exit 0; `go test ./internal/capabilities/scripts -count=1` →
  `ok ... 24.645s`.

## 5. Note

Il warning `cmd_main_max_lines` era già stato chiuso dividendo
`cmd/cyclo-budget/main.go` in quattro file. La chiusura di questo ticket non
alza nessun budget: il root e il gate architetturale tornano verdi con la
baseline esistente.
