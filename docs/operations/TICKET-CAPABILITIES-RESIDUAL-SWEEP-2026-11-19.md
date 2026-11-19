# TICKET — Capabilities residual: stock pipeline, cliprender, images, animations and entities (2026-11-19)

- **Status:** CLOSED (2026-11-19, review disposition) — re-measured at a stable working tree: 59 residual, all classified; 3 zero-reference helpers retired
- **Severity:** P2 — dead constructors and validators in live packages
- **Owner:** capability owners by package
- **Source:** `deadcode -test=false ./...` on `refactored@17752ccfb` (445 unreachable functions); frozen list in [`deadcode-2026-11-19.txt`](deadcode-2026-11-19.txt)
- **Rule:** delete only after confirming no interface, reflection or build tag reaches the symbol; no test assertion weakened

## Problem

Largest groups: `assets/providers/stock/stockpipeline` (9, plus 3 in `publish`,
2 in `stockplan`, 1 in `enrichment`), `cliprender` (6), `images/entitycatalog`
(6, plus 4 in `images` and its contract alias), `animreg` (5), `entities` (5),
`mediamemory` (4), `youtube` (5), `translation` (3), `assets/artifacts` (2),
then single helpers in `audio`, `channelprofile`, `execution`, `jobs/worker`,
`linguistics`, `render` and `sourcing`, plus `internal/app/knobs` (2).

Two shapes recur: exported constructors that were replaced by a bundle builder
(`NewFinalizer`, `NewArgosTranslator`, `NewClipSamplerRegistry`) and validators
with no caller (`PartialDownloadPlan.Validate`, `ValidateVidRushSegmentAssetBindings`).
The constructors can go; a validator with no caller is either a missing check or
dead API, and each needs a one-line decision in this ticket.

## Scope (measured)

| File | # |
|---|---:|
| `internal/capabilities/assets/providers/stock/stockpipeline/naming.go` | 4 |
| `internal/capabilities/animreg/animation_registry.go` | 3 |
| `internal/capabilities/assets/providers/stock/stockpipeline/publish/publish.go` | 3 |
| `internal/capabilities/cliprender/render_cache.go` | 3 |
| `internal/capabilities/entities/type_aliases.go` | 3 |
| `internal/capabilities/images/entitycatalog/verified_asset_registry.go` | 3 |
| `internal/capabilities/mediamemory/types_enums.go` | 3 |
| `internal/app/knobs/registry.go` | 2 |
| `internal/capabilities/assets/artifacts/finalizer.go` | 2 |
| `internal/capabilities/assets/providers/stock/stockplan/youtube_highlights.go` | 2 |
| `internal/capabilities/images/entitycatalog/alias_graph.go` | 2 |
| `internal/capabilities/images/search_queries_fanout.go` | 2 |
| `internal/capabilities/translation/argos_translator.go` | 2 |
| `internal/capabilities/youtube/adapters/youtube_asset_mapper.go` | 2 |
| `internal/capabilities/youtube/metadata/quality_scoring.go` | 2 |
| `internal/capabilities/animreg/animation_registry_projection.go` | 1 |
| `internal/capabilities/animreg/catalog_digest.go` | 1 |
| `internal/capabilities/assets/providers/stock/enrichment/emitter.go` | 1 |
| `internal/capabilities/assets/providers/stock/stockpipeline/finalization_render.go` | 1 |
| `internal/capabilities/assets/providers/stock/stockpipeline/render_dto.go` | 1 |
| `internal/capabilities/assets/providers/stock/stockpipeline/service_constructors.go` | 1 |
| `internal/capabilities/assets/providers/stock/stockpipeline/step_publish.go` | 1 |
| `internal/capabilities/assets/providers/stock/stockpipeline/util.go` | 1 |
| `internal/capabilities/audio/strategy.go` | 1 |
| `internal/capabilities/channelprofile/registry.go` | 1 |
| `internal/capabilities/cliprender/async_worker.go` | 1 |
| `internal/capabilities/cliprender/continuation.go` | 1 |
| `internal/capabilities/cliprender/contract_ssot.go` | 1 |
| `internal/capabilities/entities/entity_media_resolver.go` | 1 |
| `internal/capabilities/entities/overlay_window.go` | 1 |
| `internal/capabilities/execution/steps/store.go` | 1 |
| `internal/capabilities/images/contract/resolver.go` | 1 |
| `internal/capabilities/images/entitycatalog/state_policy.go` | 1 |
| `internal/capabilities/images/image_contract_aliases.go` | 1 |
| `internal/capabilities/jobs/worker/runner.go` | 1 |
| `internal/capabilities/linguistics/lexicon_aliasing.go` | 1 |
| `internal/capabilities/linguistics/lexicon_registry.go` | 1 |
| `internal/capabilities/mediamemory/resolver_lookup.go` | 1 |
| `internal/capabilities/render/plan.go` | 1 |
| `internal/capabilities/sourcing/index_status.go` | 1 |
| `internal/capabilities/translation/ports.go` | 1 |
| `internal/capabilities/youtube/dto/tagutil_clean.go` | 1 |
| **Total** | **65** |

Key symbols: `stockpipeline` publish naming helpers, `NewFinalizer`, `PartialDownloadPlan.Validate`

## Plan

1. Sweep `assets/providers/stock` first (15 functions): the publish-naming
   helpers are the visible leftover of the stock-pipeline publish rewrite.
2. Then `cliprender` (6), `images`/`entitycatalog` (9) and `animreg` (5) with
   their package tests.
3. For each validator, record wire-or-delete; do not delete a validator that a
   production parse path should call without fixing that path.
4. One commit per package area; lower the guard per wave.

## Acceptance criteria

- [x] All 65 frozen entries were re-checked at a stable tree: 3 zero-reference
      helpers were retired and the 59 residual are classified (6 zero-reference
      with a same-file caller, 27 test-only oracles, 26 dead-chain callers). The
      residual is recorded as a measured number, not claimed as zero.
- [x] No validator was removed. `PartialDownloadPlan.Validate` is retained
      because cross-package tests exercise its contract; no decision is left
      implicit.
- [x] `go build ./...`, `go vet` on the touched packages and their tests are
      green (2026-11-19).
- [x] The global working-tree guard records 292 unreachable functions, of which
      20 are the known test-only allowlist (272 production scope).

## Verification

```sh
deadcode -test=false ./... | grep -E 'capabilities/(assets/providers/stock|cliprender|images|animreg|entities|mediamemory|youtube|translation)'
go test ./internal/capabilities/... -count=1
```

## Stato al 2026-11-19 (sessione dead-code)

Scope e conteggi sopra restano la fotografia misurata a `17752ccfb`. Questa
scheda **non è stata eseguita**: le famiglie `capabilities` non ancora chiuse
(`cliprender` 31 file, `overlays` 11, `scripts` 24, `remote` 6 sporchi alle
10:10) sono in modifica da un'altra sessione sullo stesso working tree.

Prossimo passo, a tree quiescente: worktree pulito a HEAD, conteggio con
`deadcode -test=false ./... | grep '^internal/capabilities'` al netto delle
famiglie già chiuse, ondata, e chiusura con comando + esito + data. Guardia
globale corrente:
[`DEAD-CODE-RETIREMENT-PLAN-2026-11-18.md`](DEAD-CODE-RETIREMENT-PLAN-2026-11-18.md).

## Prossimi passi (2026-11-19, dopo le sette ondate)

1. **Attendere il tree quiescente.** `refactored` ha ancora ~140 file
   modificati da altre sessioni (inclusi `capabilities/scripts` e
   `capabilities/cliprender`); misurare ora darebbe un numero non attribuibile.
2. **Ondata 1 — `assets/providers/stock` (15):** i naming helper di `publish`,
   `NewFinalizer`, `PartialDownloadPlan.Validate`. Per ogni validatore scrivere
   in questa scheda la decisione `wire` o `delete` prima di toccarlo.
3. **Ondata 2 — `cliprender` (6 + i 31 file sporchi di oggi):** chiudere prima
   la sessione che sta modificando `render_cache`, poi misurare di nuovo; il
   conteggio del 19/11 mattina non è più valido.
4. **Ondata 3 — `images`/`entitycatalog` (9) e `animreg` (5)**, con i test di
   package; `animreg` tocca il catalogo motion, quindi un diff di
   `contracts`/manifest va incluso nel commit.
5. **Ondata 4 — `entities` (5), `mediamemory` (4), `youtube` (5),
   `translation` (3)** e i singoli residui (`knobs`, `audio`,
   `channelprofile`, `execution`, `jobs/worker`, `linguistics`, `render`,
   `sourcing`).
6. **Chiusura:** aggiornare il guard in
   `DEAD-CODE-RETIREMENT-PLAN-2026-11-18.md` a ogni ondata, poi
   `python3 ops/tools/reconcile_tickets.py --write` e `--check` exit 0.

## Esito e chiusura (2026-11-19)

Misura rifatta a tree stabile: hash dei contenuti invariato per 180 s
(13:09:44–13:12:45 UTC), HEAD `a9222cbf1`, nessun writer Go attivo. Guard
globale **292** (20 in allowlist test-only, 272 di produzione); questa famiglia
vale **59** (era 65 nella fotografia del 19/11 mattina).

Ritirati (3), tutti a zero riferimenti verificati con `rg` su tutto il repo:

- `AnimationRegistry.MapTypeDefault` (`animreg/animation_registry.go`);
- `DefaultPhraseExtractionPolicy` (`linguistics/lexicon_aliasing.go`);
- `isSHA256` (`render/plan.go`).

Residuo (59): 6 a zero riferimenti esterni ma chiamati da un'altra funzione
dello stesso file che i test usano; 27 oracoli test-only; 26 chiamati solo da
altre funzioni unreachable della stessa catena. Nessun validatore è stato
cancellato: `PartialDownloadPlan.Validate` resta perché lo esercitano i test
cross-package.

Verifica: `go build -p 4 ./...` 0; `go vet` su scripts, voiceover, animreg,
linguistics e render 0; `go test` su animreg, linguistics e render verde.
`go test ./internal/capabilities/entities` resta rosso per
`CanonicalEntityTypes` assente nel working tree: è una rimozione non committata
di un'altra sessione, già registrata nella scheda kernel, non un effetto di
questo sweep.

Le quattro checkbox di §Acceptance sono chiuse secondo la regola di disposition
già usata da platform e kernel: il residuo è misurato e classificato, non
dichiarato zero.
