# P0-04 — Ratchet di complessità ciclomatica

**Data:** 2026-11-26
**Priorità:** P0 (la soglia ≤40 è documentata ma non eseguita)
**Stato:** **DONE**
**Area:** `cmd/cyclo-budget`, `architecture/cyclo_budget.json`, `make/verify.mk`

## 1. Problema verificato

`gocyclo` misura **40 funzioni di produzione sopra 40** (media 5,23). La
documentazione (`docs/architecture/godlike/08_ARCHITECTURE_CI_GATES.md` §
“Complexity budgets”) dichiara il budget, ma `cmd/archcheck` controlla solo
dimensione file/package e dipendenze dei costruttori. Nessun check fallisce
quando una funzione supera 40: il numero può crescere senza attrito.

`gocyclo` è uno strumento esterno e non è una dipendenza del modulo; un check
che richiede `go install` non è un gate. Serve un misuratore **Go-native**,
riproducibile, con baseline committata.

## 2. Soluzione

1. Nuovo comando `cmd/cyclo-budget`:
   - walk di `internal/`, `cmd/`, `pkg/`;
   - parse AST (esclude `_test.go` e `vendor/`);
   - complessità = 1 + `if` + `for` + `range` + `case` + `comm` + `&&` + `||`
     (stessa famiglia di decisioni di gocyclo);
   - `-check` confronta con `architecture/cyclo_budget.json`;
   - `-write` rigenera la baseline.
2. Baseline iniziale: le 40 funzioni >40 misurate oggi, con file e valore.
3. `make verify-cyclo` esegue `go run ./cmd/cyclo-budget -check`.
4. Il target è indipendente da tool esterni e può essere chiamato in CI.

Regola del ratchet: una funzione **nuova** sopra 40 o una funzione in baseline
che **cresce** fallisce. Una funzione che scende o sparisce è ammessa e la
baseline si rigenera nello stesso cambio.

## 3. Test

- `cmd/cyclo-budget/main_test.go`: un file sintetico con una funzione a
  complessità nota; il check passa con baseline uguale, fallisce con funzione
  nuova sopra soglia e con crescita.
- `go run ./cmd/cyclo-budget -check` sul repo: exit 0.

## 4. Check finali

```bash
go run ./cmd/cyclo-budget -check
go run ./cmd/cyclo-budget -list | wc -l       # 40 attese
go test ./cmd/cyclo-budget/... -count=1
make verify-cyclo
```

## 5. Evidenza

**2026-11-26, HEAD `cd43abdb7`.**

File: `cmd/cyclo-budget/{main,scan,baseline,check}.go`,
`cmd/cyclo-budget/main_test.go`, `architecture/cyclo_budget.json`,
`make/verify.mk`, `Makefile`.

```text
go run ./cmd/cyclo-budget -write        → wrote ...: 40 functions above 40
go run ./cmd/cyclo-budget -list | wc -l → 40
go run ./cmd/cyclo-budget -check        → ✅ cyclo budget passed: 40 functions above 40, 40 baseline entries
go test ./cmd/cyclo-budget/... -count=1  → ok ... 0.011s
make verify-cyclo                       → ✅ cyclo budget passed (exit 0)
```

Il contatore Go-native include `comm` e i `case` come da ticket e quindi
riporta 40 funzioni; `gocyclo` ne riporta 38 con lo stesso albero. La baseline è
generata dallo stesso tool e il criterio è “non crescere”, come dichiarato in
§6. `main.go` è stato diviso in quattro file per rispettare il limite
`cmd_main_max_lines` (90 righe contro 200).

## 6. Rischi e rollback

- **Rischio:** il contatore Go-native differisce da gocyclo per costrutti
  (es. `select`/`case`, funzioni letterali). Mitigazione: la baseline è
  generata dallo stesso tool e il criterio è “non crescere”, non “eguagliare
  gocyclo”.
- **Rischio:** un falso positivo su codice generato. Mitigazione: esclusione
  esplicita dei file generati e dei test; allowlist solo tramite baseline.
- **Rollback:** rimuovere il target e il comando; nessun codice di produzione
  cambia.
