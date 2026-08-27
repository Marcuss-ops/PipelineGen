# Chiusura piano d'azione P0 — stato verificato (2026-08-27)

Nota di chiusura del piano d'azione aperto con le baseline 2026-08-26
(`post_writer_finalize` ≈ 59–68 s, Rust audio ≈ 48 s, target <100–110 s sul
job 20 clip). Ogni intervento riporta lo stato **verificato in questa
sessione**: lettura del codice riga per riga + build/test eseguiti, non solo
dichiarazioni. Regola rispettata per tutta la sessione: solo main, nessun branch.

## Esito per intervento

| # | Intervento | Stato | Verifica eseguita |
| --- | --- | --- | --- |
| 1 | Split metrico `post_writer_finalize` (`finalize.artifact_prepare` / `artifact_hash` / `drive_publish` / `completion_tx`) | ✅ Già implementato, verificato | Codice: `finalizer.ArtifactPreparation.Prepare`, `remote.VerifyArtifact`, `broker.CompleteWithArtifacts`; attribuzione stage via `kernobs.WithStage`. Build verde + `go test -race` su finalizer/completion/broker/kernel/worker. Concorrenza race-free (`Run.recordOperation` mutexato) |
| 2 | Estrazione `AudioPipelineMetrics` da job reale | ✅ Fatto | Da `20-clip-cold.report.json`: mix 24,6 s (45,6 %) + AAC 23,0 s (42,7 %) + upload 6,1 s; `audio_encode_passes=1`; RTF 0,053 (~19×). Scaling: mix sovralineare (2,36× da 10→20 clip) vs encode lineare con durata (1,52×) |
| 3 | Pubblicazione Drive bounded-parallel (4 worker) con TX atomica intoccata | ✅ Già implementato, verificato | errgroup `SetLimit(4)` su entrambi i wire path (broker + use case HTTP); ordine manifest preservato, fail-fast senza partial-success, dedup IdempotencyKey+ConflictSkip, `CompleteWithArtifacts` single-TX DOPO il drain. Test `-race` verdi: `TestBroker_CompleteWithArtifacts_BoundedParallelism`, `TestPublishAndCompleteUseCase_BoundedParallelism` |
| 4 | Voiceover intermedi NON su Drive dal finalize (O(N)→O(1)) | ✅ Già implementato, verificato | Manifest ridotto a script.json + scenes.json + final_audio.m4a certificato (`artifacts_persistence.go` §5); pubblicazione/idratazione TTS-side via pool async bounded drenato prima dell'audio compile; consumer mappati (Docs, scenes/script JSON, audio/render, Qdrant REGISTERED-only). Pin: `generation_job_manifest_test.go` + suite adapters, verdi `-race` |
| 5 | Filter graph Rust: `eval=frame` solo con automation + skip `aresample`/`aformat` per asset canonici 48k/stereo | ✅ Implementato + completato upstream, verificato | `volume_eval_mode()` e `source_normalize(probe)` su ffprobe facts; catena completata dalla canonicalizzazione TTS nel passo `remove_silence` (Edge TTS 24k mono → 48k stereo, fingerprint `voiceover-content-v2`). Suite Rust **84/84** eseguita in sessione (toolchain installata su approvazione) |
| 6 | Audit single-pass AAC / niente encode-decode intermedi | ✅ Verificato | BUILD: un solo comando ffmpeg diretto ad AAC-LC (intermedio PCM rimosso; `mix_ms=0` esplicito come contratto legacy, non fabbricato); campo letto dal Rust (`video_processor.go`), mai hard-codato lato Go. RENDER/mux: copy-policy 0-o-1 mai 2 (`render_clip.rs::audio_policy`, pin e2e `AudioEncodePasses==0`). Dati reali: `passes=1` su entrambe le baseline |
| 7 | Consumer legacy post-cutover risolti dalla reconciliation | ✅ Verificato | Binding voiceover verificati nel loop per-scena (`voiceover:<scene.ID>`): VERIFIED preservato, UPDATED self-healing verso il canonical webViewLink, rotti puliti fail-closed; committer durabile salta `voiceover:*` (le righe restano owned dalla TTS); `script_sections.voiceover_link` idratato post-TTS. Test `-race` verdi (reconciliation + drive location verifier) |

## Lavori extra della sessione

- **SUMMARY.md aggiornato**: avviso scatola nera rimosso, i tre interventi P0
  documentati con evidenze; `extract.py` allineato (non reintroduce l'avviso).
- **Pulizia legacy gate-safe** (scelta esplicita utente): chiuso il
  forward-pointer scaduto `P0-COMPL-5-RESOLVER`; registrate REMOVAL GATE con
  condizione testabile su `broker_finalize.go` (normalizzazione envelope
  pre-cutover), `transform_assemble.rs` (skip gate contract_id),
  `protocol.rs` (campi selection accettati-per-rifiutare). Verificati come
  NON-legacy architetturale: `FinalizationStrategyLegacyComplete`,
  `uploadOutputsLegacy` (fall-closed voluto), colonna `legacy_file_md5`.

## Un finding che modifica il piano: il gate assembly resta LOCKED

Creato l'audit delle certificazioni assembly —
`ops/maintenance/audit_assembly_certifications.py` (SQLite, smoke-tested sui
tre verdetti exit 0/1/2) + `RenderingGen/queue/ops/render_artifacts_contract_audit.sql`
(Postgres). Lancio reale sui DB locali:

```
certificazioni verificate        : 111
senza contract_id                : 111   → GATE LOCKED (exit 1)
```

Il ramo contract-less in `transform_assemble.rs` è load-bearing, e la causa è
strutturale: **l'identità di contratto non ha nessuna superficie durabile**
(`RenderArtifact` non ha colonne `contract_id`/`stream_signature_sha256`;
i fatti vivono solo al volo sul wire worker→gate). Sblocco richiesto:
migrazione schema + persistenza dall'output worker + backfill storico, poi
ri-esecuzione degli audit fino a zero offenders.

## Numeri: cosa aspettarsi dal prossimo run

Tutti i numeri misurati (wall 215,2 s; finalize 68,5 s; artifact 23) provengono
dal binario PRE-interventi delle 10:55. Il run HEAD atteso mostra:
split finalize operativo (prepare/hash/publish/TX separati), 3 artifact invece
di 23, pubblicazione parallela a 4 → attesa teorica `post_writer_finalize`
~12–25 s e mix_ms in calo grazie al graph condizionale + asset già canonici.

## Aperti (in ordine di valore)

1. Ri-eseguire le tre baseline con HEAD (warm con timeout client ≥15 min)
   e certificare il delta contro questa nota.
2. Sbloccare il gate assembly: migrazione `contract_id`/
   `stream_signature_sha256` su `render_artifacts` + backfill + audit zero.
3. Rimuovere i tre rami gated quando le rispettive evidenze chiudono
   (condizioni documentate nei file).
4. Facoltativo: allineare `admin_media.rs` al probe-based normalize (basso ROI,
   fuori dal percorso final-audio).
