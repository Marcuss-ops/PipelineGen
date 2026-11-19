# TICKET — Kernel and tool commands: dead registries, catalogs and model methods (2026-11-19)

- **Status:** OPEN (2026-11-19) — 40 unreachable functions in `internal/kernel` and `cmd/`
- **Severity:** P2 — introspection catalogs and model methods that look supported but are not
- **Owner:** kernel owners; tools owner for `cmd/`
- **Source:** `deadcode -test=false ./...` on `refactored@17752ccfb` (445 unreachable functions); frozen list in [`deadcode-2026-11-19.txt`](deadcode-2026-11-19.txt)
- **Rule:** delete only after confirming no interface, reflection or build tag reaches the symbol; no test assertion weakened

## Problem

`kernel/observability` contributes 7 (`AllComponents`, `AllOperations`,
`AllStages`, `StageMilestones`, `RunFromContext`, `DeriveBatchReport`,
`derivePhaseConcurrency`); `kernel/entitytype` 4; `kernel/digest` 4;
`kernel/job` 4; `kernel/models` 3; `kernel/script` 6; `kernel/media` 2;
`kernel/idempotency` 2; then `kernel/embedding`, `kernel/sceneir` and
`kernel/media/clipsequence` with one each. `cmd/admin` adds 3 and
`cmd/archcheck` 2.

The observability catalogs and the model registry methods are the category that
matters: they are introspection APIs that read as the supported way to enumerate
stages or validate a model, but nothing calls them. Either they become the
source the runtime uses or they are deleted; leaving them is what makes an
operator trust a list that drifts.

## Scope (measured)

| File | # |
|---|---:|
| `internal/kernel/entitytype/entitytype.go` | 4 |
| `internal/kernel/observability/registry.go` | 4 |
| `internal/kernel/models/registry.go` | 3 |
| `internal/kernel/digest/perceptual.go` | 2 |
| `internal/kernel/media/assembly_contract.go` | 2 |
| `internal/kernel/script/clip_preplan.go` | 2 |
| `cmd/admin/internal/cleanup/flatten_images_plan.go` | 1 |
| `cmd/admin/internal/cli/helpers.go` | 1 |
| `cmd/admin/internal/rendering/gen_api_docs_manifest.go` | 1 |
| `cmd/archcheck/gate/gate.go` | 1 |
| `cmd/archcheck/scan/textban/textban.go` | 1 |
| `internal/kernel/digest/panel_collage.go` | 1 |
| `internal/kernel/digest/validate.go` | 1 |
| `internal/kernel/embedding/handshake.go` | 1 |
| `internal/kernel/idempotency/keys.go` | 1 |
| `internal/kernel/idempotency/keys_hash.go` | 1 |
| `internal/kernel/job/deferral.go` | 1 |
| `internal/kernel/job/payload_match.go` | 1 |
| `internal/kernel/job/preparation.go` | 1 |
| `internal/kernel/job/stage_progress.go` | 1 |
| `internal/kernel/media/clipsequence/presets.go` | 1 |
| `internal/kernel/observability/concurrency_sweep.go` | 1 |
| `internal/kernel/observability/context.go` | 1 |
| `internal/kernel/observability/report.go` | 1 |
| `internal/kernel/sceneir/validator.go` | 1 |
| `internal/kernel/script/builtin_audio_aliases.go` | 1 |
| `internal/kernel/script/generation_errors.go` | 1 |
| `internal/kernel/script/resolved_plan.go` | 1 |
| `internal/kernel/script/source_spec.go` | 1 |
| **Total** | **40** |

Key symbols: `observability.AllComponents/AllStages`, `models.Model.Validate`, `digest.ValidateSHA256`

## Plan

1. Settle `kernel/observability` first: wire the catalogs into the metric/report
   path with a test, or delete them and the milestone table.
2. Then `kernel/models` and `kernel/digest` (validators): same wire-or-delete
   rule, because these are checks.
3. Sweep `kernel/job`, `kernel/script`, `kernel/entitytype`, `kernel/media`,
   `kernel/idempotency` and the commands.
4. Re-measure after each group.

## Acceptance criteria

- [ ] 0 unreachable functions under `internal/kernel` and `cmd/`.
- [ ] The observability and model catalogs are either consumed or gone.
- [ ] `go build ./...`, `go vet ./internal/kernel/... ./cmd/...` and the kernel
      tests are green.
- [ ] The global deadcode guard records the new count.

## Verification

```sh
deadcode -test=false ./... | grep -E '^(internal/kernel|cmd)/'
go test ./internal/kernel/... -count=1
```
