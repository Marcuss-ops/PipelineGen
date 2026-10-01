# Milton final job and performance remediation

Work tracking for the requested final-video fix and critical-path changes.

## Final-video audio and subtitle QA rerun

- [x] Trace the three reported defects against the succeeded final artifact and persisted request.
- [x] Preserve protected original audio from fixed intro/outro sections in the certified final-job master, including the resolved-scene projection.
- [x] Lower the normalized BGM bed from -1 dB to -10 dB and its duck target to -16 dB; align the editing-assets policy defaults and projection.
- [x] Set `render.subtitles.enabled=false` in the rerun request; keep the channel profile from filling subtitles over the explicit false value.
- [x] Save the corrected rerun payload at `ops/jobs/milton_leite_preso_ptbr_finalvideo_fixed.generate.json` with `final_job=true` and a fresh correlation ID.
- [x] Cancel the first QA rerun after it exposed a second gap: final-job audio skipped materializing protected intro clip audio. Update the audio phase to materialize those fixed clips and rebuild the resolved audio projection.
- [x] Build and restart the local PipelineGen service with the complete fixes.
- [x] Submit the corrected Milton request with a fresh idempotency key (`job_1790685246972813718_7a031056`).
- [x] Monitor the corrected job through completion (`SUCCEEDED`; durable run `run_1790685247091783167_13034883abe7`, remote render `job_afddd7f468816b81`).
- [x] Inspect the final artifact: MP4 downloaded from worker 51, H.264 Main / yuv420p / 1920×1080 / 24 fps + AAC-LC 48 kHz stereo, 1,327.792 s (22:07), 492,750,250 bytes. The original intro audio is present on all three 5 s intro segments (first 15 s mean -15.4 dB / peak 0 dB); body mix at 15–35 s averages -23.2 dB / peaks -6.1 dB. Audio plan confirms BGM gain -10 dB. Frame inspection confirms the extra pipeline transcript captions are gone; visible lower thirds are embedded in source TV footage. The 15 requested phrase overlays remain. Worker 51 job `job_208996a016b4626c` SUCCEEDED, artifact id `79d3c581f8bff2b71e27859489a1e6dd`, downloaded to `/tmp/milton-final-v3.mp4`, SHA-256 `01461998392e675e81805f4f9cb47aa386fd9f804dd0cc276cbb86b44b0d196b`.

- [x] Cancel the deterministic first final-job retry (`job_1790675141082688511_a54c5b0b`).
- [x] Chunk clip-only scenes to their canonical timeline windows and preserve the 40 ms A/V gate.
- [x] Add clip-only and Milton-shaped regression tests; verify they pass.
- [x] Recover remote failure detail: 92 video scenes rounded beyond the certified audio duration at the 24 fps copy-only mux.
- [x] Align remote scene durations to whole frames within the certified audio budget; verify 92-scene regression test.
- [x] Build and restart PipelineGen with the frame-alignment fix.
- [x] Finish and inspect final-video rerun `job_1790677315895996155_34d605fa` (SUCCEEDED; remote `job_66395c1b40568706`; 13/13 final clips; 6m32s elapsed).
- [x] Set per-item overlay render concurrency to 4 in the service environment; restart and verify effective configuration.
- [x] Reconcile RenderingGen's checked-in default/native config with its live 3-lane config and PipelineGen's 3-slot gate.
- [~] Docs child cutover — root cause FIXED and verified in production: the child published a real document (`job_1790682954389435751_42dacb21` SUCCEEDED, doc `12ObDML1c3Q6t-0vO2XKSZB2ZGlZ8X_y2p0peeExdAYo`). Root cause was the durable run holding the starter placeholder (`docs.enabled=false`); fixed by `SaveRequest` persisting the canonical request before execution (`generation_handler.go`, `run_repository.go`), plus `DocsPublishChildJobID` checkpoint and the `waiting_children` envelope from the generation handler. Fail-closed gates pinned by `TestPublishRunDocuments_FailsClosedForAnEnabledDocRunWithoutAPublisher` and the miswired-publisher route. REMAINING: one clean end-to-end run (parent not restarted mid-flight) observed through parent → waiting_children → child SUCCEEDED → parent COMPLETED via the existing aggregator.
- [x] Start the overlay sibling stage as soon as its canonical plan is ready while final AAC renders; defer result checkpoint until both branches join. Full scripts tests and race tests pass.
- [x] Verify per-item queue jobs already use the persistent warm Chronon IPC service (`mode: ipc`, active `chronon3d.service`); no cold per-item process spawn exists on this deployment, so a nested `DaemonPool` would duplicate daemon ownership.
- [x] Inspect the audio graph: it uses `volume`, `amix`, `apad`, `atrim`, and the existing `alimiter`; there is no `loudnorm`. Add `-threads:a 0`, build the release binary, and compare a 120s AAC sample (2.70s auto vs 3.05s single-threaded; roughly 0.35s sample gain, no support for a 20–30s claim).
- [x] Rebuild/restart with the overlap and AAC changes, rerun finalvideo with fresh idempotency key `milton-final-job-inline-docs-check-20260929T1112Z`; job `job_1790680098346939116_7b6fe57d` SUCCEEDED after one VidRush retry, 13/13 final clips, final audio 1,481,950 ms, certified documents present. Final attempt timing: 5m48s; overlap 106.0s; audio compile 80.3s (AAC operation 66.1s); overlays 137.1s; remote final render 36.5s; inline Docs 17.1s. The retry from the initial `images selected = 0, expected 1` added delay before this successful attempt and is not part of the 5m48s timing window.
- [x] Run relevant repository tests/builds: scripts and wiring Go packages pass; Rust suite 89/89; production Go binary and Rust release binary build successfully. Race checks for combined timeline/overlay pass.

## Subtitle opt-out implementation and final verification

- [x] Carry explicit `render.subtitles.enabled=false` through localized render input and localization plan; omit subtitle track resolution and ASS compilation, and include the option in render fingerprinting.
- [x] Build and restart service after wiring the explicit opt-out.
- [x] Submit fresh final-video rerun `job_1790686427181755584_88c0b9ab` (`final_job=true`, run `run_1790686427284540695_a5e192860378`); it SUCCEEDED. Pipeline wall time was 6m00s, worker 51 render job `job_208996a016b4626c` completed in about 9s.
- [x] Download and inspect the worker 51 MP4, inspect the source and localized clip frames, probe codecs/duration, and measure first-15s and body audio levels.

## Overlay tail corruption — PipelineGen payload fix

- [x] Confirm the final artifact's long overlay spans came from the overlay plan/payload boundary, before worker 51's packet-copy stitch.
- [x] Map both overlay endpoints to the same 24 fps frame grid and cap each outgoing window at the certified overlay artifact frame count in `internal/capabilities/scripts/final_job_payload.go`.
- [x] Update the focused payload assertion and run `go test ./internal/capabilities/scripts -run '^TestBuildFinalJobPayloadsSendsDriveStockAndPublishedOverlaysToWorker$' -count=1` (PASS).
- [x] Rebuild/restart PipelineGen; `pipelinegen.service` is active.
- [x] Superseded by the successful active-image hybrid run below. Earlier fanout/provider failures no longer block a render; image generation and entity extraction remained enabled and the certification gate stayed active.
- [x] Superseded by the successful active-image hybrid run below; no stock-only request was used.

### Verification completed — 2026-09-30

- [x] Fixed fixed-media fallback timing: after protected audio intents are projected out, the final-job payload now uses each clip's selected source window instead of the full source-file duration. This removed the deterministic +32 s mismatch.
- [x] Focused regression test passes: `go test ./internal/capabilities/scripts -run '^TestBuildFinalJobPayloadsFixedMediaPrefersTheCertifiedRenderedClip$' -count=1`.
- [x] Built and restarted PipelineGen; `scripts/systemd/pipelinegenctl restart-verify` returned `PASS`.
- [x] Submitted one hybrid `final_job` with `generate_scene_images=enabled`, `extract_entities=enabled`, YouTube disabled: `job_1790766625750517675_07426904` (`SUCCEEDED`), remote master `job_57f3f58cb8266004` (`SUCCEEDED`). The 504,382,444-byte MP4 is `/tmp/milton-overlay-fixed-final.mp4`.
- [x] MP4 duration is 1420.000 s; certified final audio is 1420.008 s. In sampled frames, the planned phrase disappears after its 13:31.527–13:36.815 window and the image card is gone just after its 13:34.040–13:39.040 window. No stale card appears in sampled frames at 14:47–14:55. This run's new overlay plan contains no item in the 14:47–14:55 window, so that window was checked for a lingering card only.
- [x] No YouTube publication was started.
