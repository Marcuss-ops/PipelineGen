# P2-07 — Allocazioni nei loop: misurare prima di toccare

**Data:** 2026-11-26
**Priorità:** P2 (nessuna evidenza di regressione; il rischio è ottimizzare alla cieca)
**Stato:** **DONE (misura eseguita, nessuna modifica di produzione)**
**Area:** `internal/capabilities/scripts`, `internal/platform/renderinggen`, `internal/capabilities/overlays`

## 1. Cosa è stato misurato

- `time.Sleep` in produzione: **7** (nessuno nei loop caldi; `pkg/retry` è il
  owner contestuale).
- `context.Background()`: **174**, concentrati in `platform/observability/run_recorder.go`
  (8), `jobs/worker_execution.go` (6), `cmd/velox/commands.go` (6). In gran
  parte background legittimo; ogni uso su handler/job va rivisto.
- `var` mutabili tipizzati: **14** (atomic/sync/map/slice), nessun global state
  non protetto paragonabile ai casi RA-09.
- **Allocazioni nei loop: non misurate.** Il conteggio grezzo di `make(` vicino
  ai `for` non è una misura e non va usato come baseline.

## 2. Perché non è un P0

Il carico è dominato da render/encoder e I/O, non da un loop CPU noto. Senza
`pprof`/`benchstat` qualsiasi “ottimizzazione” è una scommessa che può cambiare
semantica e latenza. La checklist chiede misure, non sospetti.

## 3. Acceptance

1. Profilare con `pprof` i tre path caldi:
   - `go test ./internal/capabilities/scripts -run '^$' -bench . -benchmem -cpuprofile /tmp/scripts.cpu -memprofile /tmp/scripts.mem` (o benchmark equivalente già presente);
   - `renderinggen` submit/poll;
   - overlay plan/validate su un payload reale.
2. Riportare allocazioni per operazione e `inuse_space` prima/dopo sullo stesso
   snapshot; nessun numero ereditato.
3. Applicare solo interventi con un guadagno misurabile e un test di parità.
4. Se non c'è un hot spot, chiudere il ticket con “nessuna azione necessaria” e
   la misura che lo prova.

## 4. Check

```bash
go test ./internal/capabilities/scripts -run '^$' -bench . -benchmem
go tool pprof -top /tmp/scripts.cpu
go tool pprof -top -alloc_space /tmp/scripts.mem
go run ./cmd/cyclo-budget -check   # nessuna regressione introdotta
```

## 5. Misure 2026-11-26 (prima/dopo identici: nessun codice di produzione toccato)

Benchmark esistenti, profili CPU/heap raccolti con `-cpuprofile`/`-memprofile`:

```text
BenchmarkFinalJobDemolition/PayloadBuild-24   607   1842914 ns/op   223145 B/op   2538 allocs/op
BenchmarkFinalJobDemolition/Retry...-24       576   1949845 ns/op   223103 B/op   2538 allocs/op
BenchmarkFinalJobDemolition/Stock...-24      5018    238569 ns/op    30356 B/op    559 allocs/op
BenchmarkFinalJobDemolition/Duplicate...-24  5960    200519 ns/op    44472 B/op     96 allocs/op
```

`pprof -alloc_space` sui quattro sub-benchmark: `encoding/json.Marshal`
204,99 MB, `encoding/json.Unmarshal` 58,01 MB,
`overlays.uniqueMotionPool` 51,14 MB, `overlayplan.BuildOverlayPlanV2Request`
205,29 MB cumulativi, `overlayplan.sortedLexiconWords` 35,06 MB,
`overlays.ShortPhraseMotionsByWord` 34,52 MB. Non è un loop CPU: è la
costruzione del payload JSON, che richiede un cambio di ownership/contratto e un
test di parità, non un `make` con capacity.

I due path che non avevano benchmark sono stati strumentati (solo file di test):

```text
BenchmarkMapOverlayValidate-24   14388   77165 ns/op   38696 B/op   18 allocs/op
BenchmarkClientSubmit-24          1765  716015 ns/op   23593 B/op  174 allocs/op
BenchmarkClientGet-24             4072  281906 ns/op    8042 B/op   83 allocs/op
```

`pprof` overlay: 83,8% dell'alloc spazio è `digest.SHA256File` →
`io.copyBuffer` (lettura reale del PNG), 14,9% `image/png.DecodeConfig`.
`pprof` renderinggen: domina `net/http` (`dialConn`, `Transport`, buffer di
rete). In entrambi il costo è I/O, non un'allocazione evitabile nel loop.

Conclusione: nessun intervento locale con guadagno misurabile e parità
garantita; nessun numero ereditato. La chiusura segue l'acceptance #4
("nessuna azione necessaria") per i due path I/O-bound; il payload JSON resta
misurato come debito separato e non viene toccato alla cieca.
