# P3 `app/wiring` slimming inventory (2026-11-15)

## Current size

- 167 production Go files, 36,080 production LOC.
- The package is still a mix of composition and domain adapters/validators.
  Composition-only is the target: constructors, dependency selection, and
  lifecycle ordering; no domain policy, SQL, provider transport, or validation
  rules.

## Largest owned files

| LOC | File | Classification |
|---:|---|---|
| 598 | `rendering/handlers.go` | rendering composition + handlers |
| 595 | `localized_render_enqueuer.go` | render enqueue adapter |
| 590 | `build_outbox_handlers.go` | outbox handler composition |
| 585 | `build_bundles_domain_media.go` | bundle composition |
| 579 | `search/search_backend_semantic.go` | semantic search composition |
| 569 | `build_bundles_texttracks.go` | bundle composition |
| 559 | `assets_register_sourcing.go` | asset sourcing registration |
| 556 | `script/remote_final_job.go` | script/final-job adapter |
| 548 | `voiceover/adapters_voiceover_repo.go` | voiceover repository adapter |
| 546 | `build_bundles_ai.go` | bundle composition |
| 533 | `script/scene_text_sources.go` | script source adapter |
| 528 | `canonical_media_committer.go` | media committer adapter |
| 518 | `script_generation_runtime.go` | generation runtime composition |
| 504 | `build_bundles_core.go` | bundle composition |
| 492 | `lifecycle/lifecycle_adapters.go` | lifecycle adapters |
| 492 | `composition_helpers.go` | composition helpers |

## Residual adapter/validator inventory

These files are the first candidates for a contract-first move. None is moved
in this change: each move needs the target capability's port frozen first.

| File | LOC | Target owner | Prerequisite |
|---|---:|---|---|
| `search/adapters_qdrant_transport_health.go` | 243 | `platform/qdrant` | transport-health port; no search-domain policy in the adapter |
| `brain_ranker_adapter.go` | 224 | brain/rank capability | ranker port and error contract |
| `voiceover/adapters_voiceover_repo.go` | 548 | `capabilities/voiceover` | repository port; remove wiring-only DTOs |
| `voiceover/adapters_voiceover_scriptgen.go` | 388 | `capabilities/voiceover` | scriptgen port |
| `voiceover/adapters_voiceover_tts.go` | 328 | `capabilities/voiceover` | TTS port |
| `voiceover/adapters_voiceover_resolver.go` | 198 | `capabilities/voiceover` | resolver port |
| `voiceover/adapters_voiceover_repo_tx.go` | 174 | `capabilities/voiceover` | transaction boundary port |
| `script/source_adapters.go` | 485 | `capabilities/scripts` | source adapter port; no import back into wiring |
| `script/scene_text_sources.go` | 533 | `capabilities/scripts` | scene-text source contract |
| `assets/catalogsync_adapters.go` | 122 | asset catalog capability | catalog sync port |
| `audio/sound_effect_adapters.go` | 108 | audio capability | sound-effect port |
| `clips/index_adapters.go` | 92 | clips capability | index port |
| `clips/config_adapter.go` | 133 | clips capability | timeout policy already in scheduling; move only the adapter |
| `stock/adapters.go`, `stock/enrichment_wiring.go` | 136 + 136 | stock capability | provider port and finalizer contract |
| `canonical_identity_adapter.go` | 74 | kernel identity | identity port |
| `critical_handler_validator.go` | 168 | app/bootstrap validator | validator is composition-time; keep unless a kernel validation port is frozen |

## Required move protocol

1. Freeze the target capability port and its error contract.
2. Move the adapter/validator implementation, not the composition call.
3. Keep the constructor call in `app/wiring`; wiring may import the capability,
   the capability must not import wiring.
4. Run `go build ./...`, `go run ./cmd/archcheck --strict`, and the target
   capability tests.
5. Reject the move if it creates a new import cycle or moves domain policy into
   wiring.

## Acceptance

- `app/wiring` contains only composition, lifecycle ordering, and dependency
  selection for the moved vertical.
- No new reverse imports from capabilities/platform into wiring.
- The file/LOC reduction is measured against the 167/36,080 baseline.
- No adapter is moved before its port is frozen; this prevents the
  file-move-without-contract failure mode seen in `capabilities/scripts`.
