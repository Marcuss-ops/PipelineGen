# TICKET — Kernel and tool commands: dead registries, catalogs and model methods (2026-11-19)

- **Status:** REVIEWED (2026-11-19) — 3 of 40 retired; the other 37 are reached by tests or interfaces in other packages
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

## Stato al 2026-11-19 (sessione dead-code)

Scope e conteggi sopra restano la fotografia misurata a `17752ccfb`. Questa
scheda **non è stata eseguita**: il working tree conteneva 141 file non
committati alle 10:10, quindi qualunque nuovo conteggio su `internal/kernel` e
`cmd/` sarebbe inquinato da lavoro in corso di un'altra sessione.

Prossimo passo, a tree quiescente: worktree pulito a HEAD, conteggio con
`deadcode -test=false ./... | grep -E '^(internal/kernel|cmd/)'`, ondata, e
chiusura con comando + esito + data. Guardia globale corrente:
[`DEAD-CODE-RETIREMENT-PLAN-2026-11-18.md`](DEAD-CODE-RETIREMENT-PLAN-2026-11-18.md).

## Prossimi passi (2026-11-19, sera)

1. **Sbloccare la misura:** 141 file non committati alle 10:10Z includevano
   `internal/kernel`; il conteggio va rifatto a HEAD pulito dopo il commit
   delle sessioni di oggi.
2. **Ondata 1 — cataloghi observability:** sono dati o costruttori? Se sono
   cataloghi, o li legge il runtime o vanno cancellati con il loro test.
3. **Ondata 2 — `Model.Validate`:** decidere `wire` o `delete` una volta per
   tutte; è il caso tipico di validatore senza chiamante che sembra una
   verifica mancante.
4. **Ondata 3 — helper `digest` e residui `cmd/`**, un commit per area.
5. **Chiusura:** `go build ./...`, `go vet`, test kernel verdi, guard
   aggiornato, indice rigenerato.

Da completare: nessuna delle checkbox di §Acceptance è spuntata; il ticket è
fermo al solo conteggio.

## Esito (2026-11-19)

La misura a HEAD pulito è stata rifatta e ogni simbolo è stato confrontato con
**tutti** i file Go del repository, test inclusi. Il risultato capovolge la
premessa della scheda: la maggior parte dei 40 non è codice morto, è superficie
di contratto esercitata da test di altri package.

Ritirati (3), con i test dello stesso package che li nominavano:

- `textban.CountCommentResidue` — zero riferimenti ovunque.
- `clipsequence.Presets` — catalogo non letto da nessun chiamante; con lui sono
  stati ritirati i due test che lo fissavano.
- `sceneir.MissingProfileCount` — zero riferimenti ovunque.

Mantenuti (37) con evidenza, perché ritirarli avrebbe richiesto di cancellare
assertion di altri package:

- `detail.Service.Get` e i cataloghi observability sono raggiunti da interfacce
  di produzione (`wiring/media.AssetDetailsLookup`) o da test di
  `capabilities/scripts` (`AllStages`, `StageMilestones`).
- `Model.Validate`, `ClipPrePlan.Validate`, `ResolvedClipSlot.Validate`,
  `NarrativeClipView.Validate`, `ClipPrePlan`/`ResolvedClipSlot` sono chiamati
  dai test di accettazione in `tests/acceptance/`.
- `NewResolvedGenerationPlan`, `BuiltInAudioCanonicalAliases`,
  `IsClipNativePlanningFailed`, `IsPerceptualHash`, `NewLanguageRegistryFromCodes`,
  `IsLanguageUndeterminable`, `job.Deferred`, `job.PreparationClaimBandName`,
  `asset.AliasSpellings` sono chiamati da test in `capabilities/*` e
  `platform/sqlite`.
- `CorrectSurfaceType`/`surfaceTypeTokens` e `Families` restano perché il loro
  test di package è l'unica specifica del comportamento.
- `observability.DeriveBatchReport`/`derivePhaseConcurrency` e
  `RunFromContext` sono concatenati fra loro e coperti da
  `observability_test.go`.
- `cmd/admin` (`childFolder`, `ResolveDBPath`, `writeRuntimeRouteManifest`) e
  `gate.Walk` sono helper usati dai test di gate e drive: `gate.Walk` è lo
  scaffold condiviso da oltre venti `gate_test.go`.

Verifica: `go build ./...` 0; `go vet ./...` senza errori introdotti (restano
solo due undefined di lavoro in corso di un'altra sessione:
`CanonicalEntityTypes`, `hasPanelGridCollage`); test dei package toccati verdi.

Criterio §Acceptance "0 unreachable" non raggiungibile senza riscrivere i test
di altri package: la scheda è chiusa come **reviewed**, non come sweep
completato.
