# PipelineGen — Canonical Sources

Only current, executable information belongs in the working tree. Historical plans and closure evidence belong in Git history.

| Concern | Canonical source |
|---|---|
| Engineering and Git rules | `AGENTS.md` |
| Runtime build identity (which binary/commit/config a process runs) | `internal/platform/buildinfo` — published as the `build` object on `/health` and `/ready` |
| Worker runtime build identity (same JSON contract) | `RenderingGen/renderinggen/internal/buildinfo` — published as `build` on the worker `/health` |
| Video delivery identity on disk (container, codec, profile/level, geometry, fps, pixel format, timebase, GOP, B-frames, closed GOP, audio layout) | `internal/kernel/media/assembly_contract.go` (`DefaultAssemblyMediaContract`) — the ONE declaration. Encoder filters, the compiled plan's `output` block, the fragment census, the reuse gate and the receipt all derive from it; a second copy of `1920x1080 / 24 / yuv420p / GOP 48 / 1/90000` is a defect, not documentation |
| H.264 packet-copy bitstream identity (exact `avcC` SPS/PPS hash + encoder-lane revision) | `internal/kernel/media/assembly_contract.go` (`CanonicalVideoExtradataSHA256`, `CanonicalVideoProfileRevision`) and the ONE reader in `internal/kernel/media/avc_extradata.go`. Every producer and publication gate compares the bytes, never the nominal profile alone; the revision is part of the fragment work/cache identity, so an encoder-lane change cannot be answered by an older artifact whose avcC happens to match |
| Overlay artifact profile + media contract (`output_profile_id` ↔ `media_contract`) | `internal/capabilities/overlays/model` (`OverlayPlan.StampOutputProfile`, enforced by `Validate`; derivation in `ContractIDForProfile`) — stamped once at the seal and in the batch children. An H.264/yuv420p artifact never declares `overlay-v1` (ProRes 4444 with alpha) |
| Content-address verification of a file before it is trusted or published | `internal/kernel/digest` (`Verifier`, `SHA256File`) — one memoized authority, used by the render asset prefetch boundary too |
| Measured final-job/overlay throughput and where each number came from | `ops/jobs/garlasco_editorial_e2e_20261030/REPORT-2026-11-01-final-job-timing-map-cache.md` — the single owner of measured timings. Tickets cite it by section; they do not restate its numbers, and a figure without a run id there is an estimate |
| Asset staging into RenderingGen's content-addressed store | `internal/platform/renderinggen` (`NewHTTPAssetPrefetcher`) — a producer `LocalPath` is an optimization that must hash to the address, never a source of truth |
| Overlay lane certification (build → deploy → identity → canary → verify → bundle) | `scripts/certify_overlay_lane.sh` via `make certify-overlay-lane` |
| Portable overlay+t timing job bundle for another machine | `scripts/certify/overlay_lane_canary.json` + the run bundle under `ops/benchmarks/overlay-lane-*` |
| Current system architecture and target root decision | `ARCHITECTURE.md` |
| Build and startup | `README.md` |
| Live HTTP routes | `docs/api/ACTIVE_API_GENERATED.md` |
| Pipeline phase (stage) names recorded on a Run, and their canonical order | `internal/kernel/observability/registry.go` (`AllStages`, `StageMilestones`) — one owner for every phase literal; capabilities alias it instead of declaring their own |
| Workflow-progress stages reported to a parent job | `internal/kernel/job/stage_progress.go` (`CanonicalStageOrder`) — the progress dimension, distinct from the execution-phase registry above |
| Architecture policy, target roots, and legacy-root restrictions | `architecture/policy.yaml` |
| Legacy-root migration ownership and deadlines | `architecture/package_hotspots.json` |
| Architecture policy navigation | `docs/architecture/godlike/INDEX.md` |
| Capability ownership | `architecture/ownership.generated.yaml` |
| Active exceptions only | `architecture/current.yaml` and `architecture/issues.yaml` |
| Compatibility removals | `architecture/deprecations/` |
| Qdrant schema | Go adapter under `internal/platform/qdrant/schema` (owned by `internal/platform/qdrant`). NOT a media source of truth: the media Qdrant projection is retired, the machine-readable `architecture/qdrant/v3-schema.json` was deleted with it, and only the non-media consumers (mediamemory frames/concepts, maintenance DR, admin audit) read this schema |
| Operational procedures | current files under `docs/operations` |
| Active performance work (what is still measured as FAIL) | `docs/operations/TICKET-FINAL-JOB-BITSTREAM-REUSE-2026-11-01.md` — current state and open items. `TICKET-MAX-THROUGHPUT-OVERLAY-ARCHITECTURE.md` keeps its historical sections; where the two disagree on present state, the live ticket wins |
| Clip pre-planner pipeline (input → planner → search → sampler → view redaction → generator → binding) | `docs/operations/clip-pre-planner.md` |
| CI exceptions | allowlists under `docs/migrations` |

## Conflict rule

When documentation conflicts with code, generated routes, tests, or machine-readable policy, executable sources win. Correct or delete the stale prose immediately.For the internal tree, the binding decision is singular: `app`,
`kernel`, `capabilities`, and `platform` are the only roots — and the only ones
on disk. The former `application`, `api`, `infrastructure`, and `domain` roots
are DELETED (verified absent 2026-09-13); `architecture/policy.yaml` declares
`legacy_internal_roots` intentionally absent. Re-creating one of them is a
violation enforced forward by `percheck_legacy_root_new_code`,
`percheck_legacy_root_ban`, and `percheck_api_infrastructure_imports`.

## Documentation policy

Do not add action-plan diaries, completed wave reports, snapshots, evidence dumps, future-dated completion reports, or duplicate architecture explanations. Use issues for active work and Git history for completed work.
