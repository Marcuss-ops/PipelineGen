# TICKET — Voiceover dead surface and test-only harness (2026-11-19)

- **Status:** CLOSED (2026-11-19, review disposition) — harness is test-only; all eight service declarations classified, none zero-reference
- **Severity:** P2 — constructors and helpers that no request reaches, plus harness code declared outside `_test.go`
- **Owner:** voiceover maintainers
- **Source:** `deadcode -test=false ./...` on `refactored@17752ccfb` (445 unreachable functions); frozen list in [`deadcode-2026-11-19.txt`](deadcode-2026-11-19.txt)
- **Rule:** delete only after confirming no interface, reflection or build tag reaches the symbol; no test assertion weakened

## Problem

The voiceover service is live, but 23 of its declarations have no production
caller: constructors (`NewService`, `NewVoiceoverFinalizer`,
`NewUploadIntentUseCase`), fingerprint builders (`BuildVoiceoverContentFingerprint`,
`BuildVoiceoverContentFingerprintV3`, `DefaultVoiceoverPostPolicy`), request
normalizers and helpers (`normalizeBatchRequest*`, `voiceOverridesFromMetadata`,
`resolveVoiceForLanguage`, `voiceOverrideFor`, `randomSuffix`).

The operational files are a different case. `tests/operational/voiceover_harness.go`,
`voiceover_probes.go` and `voiceover_report.go` are not `_test.go` files but only
test binaries compile them, and `make verify-voiceover` / `smoke-voiceover` do
invoke the harness. So the 19 dead methods are not a dead suite: they are methods
no test calls, declared in a package the production build never includes. They
must be moved into `_test.go` or deleted, and the suite must stay.

The three rate-limited publisher constructors in `app/wiring/voiceover` have no
caller at all: the wiring builds the publisher another way.

## Scope (measured)

| File | # |
|---|---:|
| `tests/operational/voiceover_harness_helpers_test.go` (renamed) | 6 |
| `tests/operational/voiceover_probes_test.go` (renamed) | 6 |
| `tests/operational/voiceover_report_test.go` (renamed) | 6 |
| `internal/capabilities/voiceover/service/types_helpers.go` | 5 |
| `internal/app/wiring/voiceover/build_bundles_voiceover_rate_limits.go` | 3 |
| `internal/capabilities/voiceover/service/process_segment_fingerprint.go` | 3 |
| `internal/capabilities/voiceover/service/process.go` | 2 |
| `internal/capabilities/voiceover/service/types.go` | 2 |
| `internal/capabilities/voiceover/service/upload_intent.go` | 2 |
| `internal/capabilities/scripts/runner_phase_voiceover.go` | 1 |
| `internal/capabilities/voiceover/service/dedupe.go` | 1 |
| `internal/capabilities/voiceover/service/filename.go` | 1 |
| `internal/capabilities/voiceover/service/finalizer.go` | 1 |
| `internal/capabilities/voiceover/service/language.go` | 1 |
| `internal/capabilities/voiceover/service/metadata.go` | 1 |
| `internal/capabilities/voiceover/service/parent_state.go` | 1 |
| `internal/capabilities/voiceover/service/pipeline_error.go` | 1 |
| `internal/capabilities/voiceover/service/service.go` | 1 |
| `internal/capabilities/voiceover/service/stylegroup.go` | 1 |
| `pkg/defaults/voiceover.go` | 1 |
| `tests/operational/voiceover_test_helpers_test.go` (renamed) | 1 |
| **Total** | **47** |

Key symbols: `NewService`, `BuildVoiceoverContentFingerprintV3`, `VoiceoverHarness.ProbeJobs`, `rateLimitedPublisher.Publish`

## Plan

1. Service: per symbol, confirm `rg -w`; delete constructors that duplicate a
   live one, and move genuinely useful test oracles into `_test.go`.
2. Harness: keep only methods reached by `voiceover_harness_test.go`,
   `voiceover_e2e_smoke_test.go` and the other `tests/operational/voiceover_*`
   tests, move them into `_test.go`, delete the rest. Do not remove the
   `verify-voiceover` / `smoke-voiceover` targets.
3. Wiring: delete `NewRateLimitedPublisher`, `NewRateLimitedPublisherWithGate`
   and `rateLimitedPublisher.Publish` unless a caller is restored.
4. Run the voiceover suite, not just the build.

## Acceptance criteria

- [x] All eight residual declarations were re-measured and classified: 6
      test-only oracles, 1 JSON reflection seam (`BatchItem.UnmarshalJSON`),
      1 dead-chain helper (`randomSuffix`). None has zero references; the
      residual is recorded, not claimed as zero.
- [x] No production file in `tests/operational` declares a harness helper: the
      harness, probes, reports, and smoke DB guard are all in `_test.go`.
- [x] The Go equivalent of the voiceover gate is green:
      `go test ./internal/capabilities/voiceover/... -count=1` and
      `go test ./tests/operational -count=1` (2026-11-19).
- [x] The global working-tree guard records 292 unreachable functions, of which
      20 are the known test-only allowlist (272 production scope).

## Verification

```sh
deadcode -test=false ./... | grep -E 'voiceover|tests/operational'
go test ./internal/capabilities/voiceover/... ./tests/operational -count=1
```

## Stato al 2026-11-19 (sessione dead-code)

Scope e conteggi sopra restano la fotografia misurata a `17752ccfb`. Questa
scheda **non è stata eseguita**: `internal/capabilities/voiceover` ha 3 file
non committati di un'altra sessione, quindi il conteggio va rifatto a tree
quiescente prima di scegliere tra ritiro del servizio e cablaggio dell'harness.

Prossimo passo: worktree pulito a HEAD, conteggio con
`deadcode -test=false ./... | grep '^internal/capabilities/voiceover'`, decisione
datata sull'esito `BuildBundles`/`BuildBundlesWithPlan`, ondata, chiusura con
comando + esito + data. Guardia globale corrente:
[`DEAD-CODE-RETIREMENT-PLAN-2026-11-18.md`](DEAD-CODE-RETIREMENT-PLAN-2026-11-18.md).

## Prossimi passi (2026-11-19, sera)

1. **Decidere il destino del servizio voiceover** prima di cancellare: se
   `BuildBundles`/`BuildBundlesWithPlan` sono il punto di ingresso reale,
   cablarli; se sono residui, ritirare il servizio con i suoi test. La
   decisione va scritta qui con data, non lasciata implicita nel commit.
2. **FATTO (2026-11-19):** spostati harness, probe, report e smoke DB guard in
   file `_test.go`; `go test ./tests/operational -count=1` PASS e il deadcode
   scan non segnala più funzioni in `tests/operational`.
3. **FATTO (2026-11-19):** eliminati gli helper senza chiamanti (`NewService`,
   builder fingerprint V2/V3/default, normalizer/parser legacy, resolver voce,
   `Service.resolveDestination`, e accessor temporaneo fan-out); gli oracle
   richiesti dai test sono in `_test.go`. `go test
   ./internal/capabilities/voiceover/service/... -count=1` PASS. Restano 8
   dichiarazioni nella scansione: costruttori/supporto test, decode JSON e
   l'entrypoint UploadIntent, da valutare prima della rimozione.
4. **Chiusura:** `go test ./internal/capabilities/voiceover/... ./tests/operational
   -count=1`, guard aggiornato, indice rigenerato.

Da completare: la scelta wire-or-retire resta aperta; la scansione del working
tree aggiornata conta 8 dichiarazioni nel service e zero nel runner scripts;
non tutte sono eliminabili in sicurezza senza una decisione di prodotto.

## Avanzamento (2026-11-19)

I quattro file della sola suite operativa sono stati rinominati in
`*_test.go`. Sono rimasti disponibili a `go test ./tests/operational`, ma non
entrano più nella build di produzione. La scansione aggiornata elenca ancora
8 dichiarazioni in `internal/capabilities/voiceover/service` e tre helper nel runner
scripts; nessuno è stato cancellato senza una decisione sul confine del
servizio. ## Esito e chiusura (2026-11-19)

Le otto dichiarazioni residue sono state classificate a tree stabile:

- test-only: `NewVoiceoverFinalizer`, `NewPipelineError`,
  `BatchRequest.PayloadMap`, `BatchItem.fail`, `NewUploadIntentUseCase`,
  `UploadIntentUseCase.Execute`;
- seam di reflection: `BatchItem.UnmarshalJSON` — `encoding/json` la invoca
  senza chiamata diretta, quindi ritirarla cambierebbe il decode wire;
- catena morta: `randomSuffix`, chiamata da `types.go`, a sua volta unreachable.

Nessuna ha zero riferimenti: la cancellazione avrebbe rimosso copertura o
cambiato il contratto di decode. L'harness resta test-only e la scheda si chiude
come disposition, con il residuo registrato e non dichiarato zero.

Verifica: `go test ./internal/capabilities/voiceover/... -count=1` PASS;
`go test ./tests/operational -count=1` PASS.
