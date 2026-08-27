# Ottimizzazioni LLM / TTS wall / Google Docs — diagnosi ed esito (2026-08-26)

Baseline di riferimento: 20-clip cold (`20-clip-cold.report.json`, TOTAL WALL 215.2 s).

## Riepilogo numeri (dal RunReport 20-cold)

| Blocco | Wall | Accumulato | Call | Parallelismo effettivo |
|---|---|---|---|---|
| LLM `generate` | 71.8 s | 239.3 s | 20 | **3.3x** |
| TTS `voiceover` | 76.6 s | 76.6 s | 20 | 1.0x *apparente* (vedi sotto) |
| Docs `document.publish` | 14.0 s | 14.0 s | 1 | — |

## LLM — cappato dal server, non dal client (nessuna modifica di codice)

- Gate client (`script_generation_concurrency`) = **4**; il server Ollama ha
  `OLLAMA_NUM_PARALLEL=3` esplicito in
  `/etc/systemd/system/ollama.service.d/gpu.conf`.
- Il parallelismo misurato (239.3 s / 71.8 s = 3.33x) coincide con `num_parallel=3`:
  il client è GIÀ sopra la capacità del server. Alzare il gate client non riduce il wall.
- GPU: **RTX A4000 16 GB, 2.7 GB liberi** con il modello caricato → `num_parallel`
  non è alzabile senza OOM (il model 5.1B + KV cache occupa ~13 GB).
- **Leva reale**: ridurre la latenza per-call (12 s/scena: prompt lungo con clip
  evidence) o batch di più scene per chiamata — entrambi toccano il prompt/parsing
  (rischio qualità), da valutare con benchmark dedicati, oppure GPU più grande.

## TTS wall — è l'arrivo delle scene dall'LLM, non il provider

- Con lo streaming SceneTextReady attivo, la TTS di scena i parte quando il testo
  della scena i è finale: wall TTS ≈ wall LLM + coda (~71.8 + 3.8 ≈ 76.6 s).
- Il pool TTS (4) e il semaforo provider (`max_concurrent_tts: 4`, plateau misurato
  dell'Edge worker) sono già allineati; l'apparente "1.0x" è l'arrivo seriale.
- **Conseguenza**: accelerare la TTS wall richiede accelerare l'LLM (sopra).
  Nessuna modifica di codice lato pipeline.

## Google Docs — implementato: publish 7 → 5 round trip API

`CreateDocIdempotent` (fresh path) faceva 7 chiamate API sequenziali:
find → create → batchUpdate → **get-parents → update-move → tag-gen_id → tag-hash**.

Ora (`internal/platform/drive/doc_client.go`):
- Nuovo `createDocWithProps` + `moveToFolderAndTag`: il move e i DUE tag
  (`pipelinegen_generation_id` + `pipelinegen_content_sha256`) sono fusi in
  **una singola `Files.Update`** (AddParents + RemoveParents + AppProperties).
- `CreateDoc` delega a `createDocWithProps(..., nil)` (comportamento identico;
  con folderID vuoto e props nil l'update è saltato: create-only = 2 round trip).
- Semantica non-fatale preservata: se il tag fallisce, il doc esiste → ritorna
  doc + errore con "idempotency" → l'adapter mappa a `ErrDocumentReferencePreserved`.
- Test: `TestCreateDocIdempotent_FreshPathFoldsMoveAndTagIntoOneRoundTrip` pinna
  le 5 chiamate e il corpo dell'update con entrambe le proprietà.

Atteso: ~14 s → ~10-11 s sul 20-clip. **Win strutturale più grande** (14 s fuori
dal critical path): pubblicare il doc in parallelo con `audio_compile` (il doc
dipende solo da scene + voiceover link, non dal master audio) — da fare con
verifica live.

## Verifica `audio_encode_passes` e assenza di pass intermedi AAC (richiesta successiva)

**Esito: single-pass confermato, e la metrica ora è evidence-based.**

- Report: `audio_encode_passes = 1` in entrambe le baseline (20-cold, 10-cold).
- **Prima**: il valore era hard-coded dal Go (`CombinedAudioRenderer.Render` →
  `metrics.AudioEncodePasses = 1` in `video_processor.go`), mentre il Rust
  lasciava `audio_encode_passes: None` nella response (`render_audio_probe.rs`) →
  il "1" era asserito, non misurato.
- **Ora**: il Rust imposta `audio_encode_passes = Some(1)` nel punto esatto
  dove vive l'encode (`render_audio_execution.rs`), e il Go lo legge
  (`RenderAudioPlanWithMetrics`) con fallback al contratto single-pass per
  binari vecchi.
- **Catena verificata (nessun encode/decode intermedio sul master)**:
  1. `render_audio_plan` → UNA invocazione ffmpeg: filter graph
     (atrim/asetpts/aresample/aformat/adelay/volume → amix → apad/atrim/alimiter)
     → `-c:a aac` direttamente al master `.m4a` (commento storico: l'intermedio
     PCM è stato rimosso perché causava un secondo pass completo di
     read/write/encode sull'intera timeline).
  2. `publish_output` = solo `fs::rename` (part → final), nessuna conversione.
  3. `probe_audio` = ffprobe (decode read-only di certificazione, mai re-encode).
  4. `sha256_file` = solo hash.
  5. Upload Drive (6.1 s) = trasferimento, nessun decode.
- I decode/encode esistenti sono sui **sorgenti**, non sul master: cleanup
  per-item dei voiceover TTS (`RemoveSilence`) e materializzazione audio clip
  (prefetch) — entrambi producono gli INPUT del master, non pass intermedi.
- Test: suite e2e `rustexec` con binario reale (pinnano `AudioEncodePasses == 1`
  per `render_audio_plan` e `0` per `render_clip` copy) — tutti PASS.

## Impatto atteso combinato sul 20-clip cold (215.2 s)

| Intervento | Stato | Delta atteso |
|---|---|---|
| Finalize parallelo (bounded 4) | già in HEAD | −45 s |
| `eval=once` + skip aresample canonico | già in HEAD (binario ricompilato) | −2-5 s (mix) |
| Docs round-trip (5 vs 7) | **implementato ora** | −3-4 s |
| LLM/TTS | **nessuna modifica possibile** (server-bound) | 0 |
| Docs ∥ audio_compile | candidato (verifica live) | −14 s |
