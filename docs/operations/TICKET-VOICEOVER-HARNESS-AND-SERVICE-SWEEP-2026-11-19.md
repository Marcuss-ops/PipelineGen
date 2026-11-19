# TICKET — Voiceover dead surface and test-only harness (2026-11-19)

- **Status:** OPEN (2026-11-19) — 47 unreachable functions: 23 in `voiceover/service`, 19 in the operational harness files, 3 rate-limit publishers, 2 mixed
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
| `tests/operational/voiceover_harness.go` | 6 |
| `tests/operational/voiceover_probes.go` | 6 |
| `tests/operational/voiceover_report.go` | 6 |
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
| `tests/operational/voiceover_test_helpers.go` | 1 |
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

- [ ] 0 unreachable functions under `internal/capabilities/voiceover`,
      `internal/app/wiring/voiceover` and `tests/operational/voiceover*`.
- [ ] No production file in `tests/operational` declares a harness helper: the
      surviving ones are in `_test.go`.
- [ ] `make verify-voiceover` (or its Go equivalent) and
      `go test ./internal/capabilities/voiceover/... -count=1` are green.
- [ ] The global deadcode guard records the new count.

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
