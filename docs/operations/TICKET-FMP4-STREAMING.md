# TICKET-FMP4-STREAMING

**Stato:** IMPLEMENTATO 24/10/2026 (fasi 1–3 + staging store/client); gate
live residui elencati sotto. Default invariato: il MP4 classico resta il
default, gli artefatti overlay silenziosi chiedono il frammentato.
**Origine:** analisi Drive/formati del 24/10/2026: gli artefatti overlay sono
intermedi "byte finali appena appesi" — il container frammentato toglie
l'attesa del completamento file (moov backpatch) da hash, upload e receipt, e
l'upload streaming li manda fuori mentre il mux scrive.
**Ordine consigliato:** completare i gate live qui sotto, poi abilitare il
percorso a stream (tail-upload) nel worker.

## Principio

Un artefatto overlay è **video-only, frammentato (fMP4/CMAF-style,
`frag_keyframe+empty_moov+default_base_moof`)**: ogni byte è finale appena
appeso. Conseguenze: hash append-only valido senza reread, upload in streaming
possibile (staging + finalize per-hash), nessun audio dentro l'overlay (l'audio
canonico entra una volta sola nel master finale Rust `mux_audio_copy`).

## Implementato (mappa file)

**Chronon3d (producer):**
- `include/.../video/packet_assembler.hpp` — `MuxOpenConfig.fragmented_mp4`,
  `SegmentAssemblyRequest.fragmented_mp4`, membro `MuxSession::fragmented_mp4_`.
- `src/media/video/mux/packet_assembler_session.cpp` — movflags frammentati in
  `write_header` (solo muxer ISO-BMFF), include `<string_view>`.
- `src/media/video/mux/packet_assembler.cpp` — `assemble_segments` propaga la
  flag.
- `include/.../video/video_config.hpp` — `VideoOutputConfig.fragmented_mp4`;
  `src/media/video/encode/native_av_sink.cpp` lo passa al mux.
- Plumbing richiesta→job→encoder (specchio di `gop_copy_only`):
  `runtime/job/render_job_request.hpp` (OutputSpec + fingerprint),
  `timeline/render_job.hpp`, `runtime/job/render_job_normalizer.cpp`
  (fingerprint `out_fragmented_mp4`), `commands/render/command_render_plan.hpp`,
  `commands/render/register_render_commands.cpp` (`--fragmented-mp4`),
  `commands/render/command_render_plan_internal.cpp`,
  `commands/video/common/video_export_common.hpp`,
  `commands/video/common/pipe_export_helpers.cpp`,
  `utils/video/ffmpeg_pipe_encoder.hpp`, `utils/video/native_av_encoder_open.cpp`,
  `utils/video/video_job_execute_options.cpp`,
  `daemon/daemon_service_ipc_protocol.cpp` (vocabolario chiuso + decode
  spec-or-root + `ASSEMBLE_SEGMENTS`), `daemon/daemon_render_job_payload.hpp`.
- Test: `tests/video/test_output_contract.cpp` —
  "MuxSession fragmented MP4 writes fragments and skips the checksum reread"
  (ftyp+moof presenti, digest append-only == sha256 file, zero reread) e
  "MuxSession A/B: classic and fragmented outputs decode to identical
  frames" (parità framehash FNV-1a su 24 frame decodificati, stesso
  sorgente incapsulato in entrambi i container).

**RenderingGen (worker):**
- `internal/chronon/client.go` — `OutputSpec.FragmentedMP4`
  (`fragmented_mp4` wire), `render_args.go` (`--fragmented-mp4`),
  `ipc.go` (`AssembleRequest.FragmentedMP4` → `ASSEMBLE_SEGMENTS`).
- `internal/storage/growing_file.go` — `GrowingFileReader`: tail di un file
  a cui un altro processo appende (EOF solo dopo `Finish`, `Abort` no-op
  dopo il commit, errore esplicito se il file non compare mai).
- `internal/storage/staging_client.go` — `Client.SupportsStaging` /
  `Client.PutStaged` (aggancio reale di `StagedUploader`), abort della
  sessione su upload/finalize falliti.
- `internal/processor/tail_upload.go` — lane di upload in streaming:
  `startTailUpload` prima del render, `Finish`/`Wait` in `storeArtifact`
  DOPO i gate di verifica (commit tardivo: un job rigettato non installa
  byte), `Abort` su ogni errore; session-id per-attempt conforme alla
  grammar del store.
- `internal/processor/gpu_run.go` / `pipeline.go` / `finalize_job.go` /
  `store_artifact.go` — hand-off dell'upload dal render lane allo store
  phase (`PreparedJob.TailUpload`), identità dell'artefatto dal digest
  dello stream (cross-check con la receipt identity, fail-closed su
  mismatch), fallback a hash-then-PUT se lo staging fallisce.
- `internal/processor/gpu_run.go` — artefatto silenzioso
  (`AudioSourcePath == ""`) ⇒ fragmented; artefatti con audio sorgente
  restano MP4 classico.
- `internal/processor/finalization.go` — parent assembly frammentata quando
  nessun segmento figlio porta audio (`AudioStreams == 0`).
- `internal/storage/staging_client.go` — `StoreStaged`: upload in streaming su
  `/objects/staging/{session}` con hash calcolato dallo stream + finalize con
  assert del digest (rifiuta mismatch client/store). Test:
  `growing_file_test.go` (5 casi) e `processor/tail_upload_test.go`
  (overlap, abort, fallback, mismatch, session grammar).

**ObjectStore (byte transport):**
- `internal/store/staging.go` + `memory_staging.go` — `StagingBackend`
  (`StagingWrite`/`StagingFinalize`/`StagingAbort`): pending workspace
  privato, verify-before-install invariato (l'assert del caller deve
  coincidere col digest calcolato dallo stream, altrimenti 422 + discard).
  Test `TestStoreExposesNoUnverifiedWritePath` aggiornato con la
  classificazione dei nuovi metodi.
- `internal/server/server_staging.go` — route
  `PUT /objects/staging/{session}`, `POST .../finalize`, `DELETE ...`
  (abort idempotente, 501 onesto su backend senza estensione). Test su
  entrambi i backend.

**Muscles Rust (consumatori):**
- `rust/pipelinegen-muscles/src/transform_assemble.rs` — `assemble_copy`
  video-only: quando nessun input ha audio la mappa audio **non viene
  emessa** (zero operazioni audio); l'audio entra solo in `mux_audio_copy`.

## Gate di accettazione

- [x] Build + test: `go test` RenderingGen (32 pacchetti ok, incluso
      `TestNoDeadExports`), `go test` objectstore (store+server staging su
      entrambi i backend), `cargo test` pipelinegen-muscles (164+3 test
      verdi, 0 failed).
- [x] Mux fragmented: test doctest (ftyp+moof, checksum append-only senza
      reread) — vedi sopra.
- [x] **A/B parità framehash**: test doctest
      `MuxSession A/B: classic and fragmented outputs decode to identical
      frames` — gli stessi 24 pacchetti H.264 incapsulati in classico e
      frammentato decodificano a sequenze di framehash identiche (FNV-1a
      sui piani raw, convenzione della suite), e entrambi i container
      producono il digest delle proprie byte finali.
- [ ] **A/B live su piano golden** (residuo LIVE): la parità pixel è ora
      coperta dal test A/B sopra; resta da misurare la coda
      `artifact_available_at − queue_completed_at` prima/dopo su un piano
      golden eseguito dal worker (richiede run live con daemon+GPU).
- [x] **Tail-upload end-to-end (wiring)**: `GrowingFileReader`
      (`internal/storage/growing_file.go`) alimenta `StoreStaged` mentre il
      mux appende; il lane è agganciato al render path
      (`tail_upload.go`, `gpu_run.go` hand-off → `storeArtifact` commit DOPO
      i gate di verifica, abort su ogni errore) con fallback a
      hash-then-PUT se lo staging fallisce. Test: overlap con producer che
      appende a chunk, digest == sha256(file), abort senza install,
      fallback, mismatch digest fail-closed, session-id grammar.
- [ ] **Misura overlap live** (residuo LIVE): misurare l'overlap encode×upload
      su un item pesante (mappa 42–90 s) in esecuzione reale; oggi
      `objectstore_upload_us` registra la parete dell'upload sovrapposta al
      render (`publish_ms` contiene solo il residuo di drain).
- [ ] **Deploy** (residuo PRODUZIONE): il binario in esecuzione
      (linux-video-release, daemon su /run/chronon3d*.sock) è PRECEDENTE a
      queste modifiche: serve build+deploy del CLI e riavvio dei daemon
      (azione di produzione, non eseguita in questa sessione).
- [x] Re-baseline: non applicabile — nessun default invertito (il MP4
      classico resta il default), tutti i golden byte-identical esistenti
      restano validi.

## Dipendenze e rischi

- Il fragment boundary cade su keyframe (`frag_keyframe`): il GOP dell'encoder
  determina la granularità dello stream; per frammenti ~1 s serve keyint ~1 s.
- `assemble_copy` accetta input frammentati via ffprobe/concat come prima;
  i confini di splice devono restare su keyframe (copy-certification
  invariata).
- L'integrazione tail-upload nel render path è l'ultimo passo mancante per
  il "sputati fuori senza aspettare il completamento del file"; lo staging
  lato store/client è già pronto e testato.
