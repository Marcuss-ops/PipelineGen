# Verifica prefetch audio + render clip (Dolly Parton, 5 clip)

Due run a **cache clip-audio fredda** (`data/tmp/audioassets/assets/` spostata via `mv`
prima del submit), stesso manifest
`ops/jobs/dolly_parton_5clips_wm_dualbg_subsstyle.generate.json`
(5 clip, watermark top-right + stroke/shadow, plate `drive-background-05`,
subs `subs-young` burn, BGM+SFX, `mix_policy VOICEOVER_DUCKED_CLIP`).

| | run 1 | run 2 |
|---|---|---|
| build | `7ea9515b` (bin 11:58) | `6ac4aecc0` (bin 16:04:57) |
| job | `job_1790092706062892483_2a044771` | `job_1790093191569826084_fbcfed1c` |
| esito | SUCCEEDED, 0 warning | SUCCEEDED, 0 warning |
| wall | 181 s | 159 s |
| `render_metrics` | 5/5 ok, 0 fail, wall 95.9 s | 5/5 ok, 0 fail, wall 57.6 s |
| `clip_audio_prepare_ms` | **0** | **0** |
| watermark verificato nel plan | `VELOX EDITING V2` | `VELOX EDITING V3` |
| cache clip-audio scritta | 16:00:32 → 16:00:57 | 16:08:19 → 16:08:44 |

## Cosa funziona (verificato)

- **Render**: 5/5 UPLOADED su Drive, `sha256` per scena, upload verificati
  (`drive upload verified` + `delivery: file published`, folder
  `1sDcm7-gH5v0f-dpHwBWzIO-4m2R2O5Lw` = `<docs root>/<job id>/en`) nel run 2;
  run 1 in `1-23kdf4GcfZF3Xx8_8RFI5TaAlxul7hu` con `docs.folder_id` esplicito.
- **Watermark/subs/plate** risolti nel plan (`watermark_text`, `background_mode=asset`,
  `has_subtitle`, `has_subtitle_style`).
- **`docs.folder_id` omesso** nel run 2 → destinazione = default di deploy
  (`PIPELINEGEN_SCRIPT_DOCS_FOLDER_ID`, allineato a `1ST6FxPu…`) ✅.
- **Polling**: `GET /api/jobs/{id}/full` espone stato, stage, progress, eventi,
  warning, timing per-operazione e l'intero `result.result` (render_metrics,
  localized_renders con sha256/metrics/drive ids, audio_metrics, audio_plan,
  phrase_timings, canonical_timeline…).

## Cosa NON funziona: il prefetch clip-audio non consegna

Sequenza ricostruita dai log del servizio (journald, `-o cat`) e dagli mtime su disco.

### run 2 (`/tmp/run2.log`)

```
16:07:20 → 16:07:38   materializzazione clip per il RENDER (branch=cas_cache, hit=True)
16:07:37 → 16:08:10   render locali scritti (data/tmp/localization/*.en.mp4)
16:08:15.101          canonical_materializer.materialize.start   yt_vLRjqTIiMjc   ← goroutine del PREFETCH
16:08:15.104          canonical_materializer.drive_download.failed  duration_ms=0  error="context canceled"
                      (stacktrace: ...app/wiring.(*audioAssetSourceAdapter).ResolveClipAudioAsset
                                   ...capabilities/scripts.PrefetchAudioAssets.func2)
16:08:15.148 → 16:08:45.619   loop SINCRONO sequenziale, ordine scena, cache_hit=False:
                      Bc9gTqiljLA → tnoMGevqWAM → pfaIAdqvlig_457 → pfaIAdqvlig_929 → vLRjqTIiMjc
16:08:19 → 16:08:44   i 5 file compaiono in data/tmp/audioassets/assets/ (mtime = fine download)
```

Run 1 identico: cancel a 16:00:27.06 (2 download uccisi a 12–19 ms), poi loop sincrono
16:00:27.13 → 16:00:58.87 nello stesso ordine scena, con scritture su disco 16:00:32 → 16:00:57.

**Conclusione**: le goroutine del prefetch raggiungono il materializer ma vengono
**cancellate in volo** (la prima `materialize.start` del loop sincrono segue di 44 ms il
cancel); il prefetch non lascia nulla in cache, quindi `audio_compile` ripaga **~26–32 s
di download Drive sequenziali** dentro `prepareClipAudioAssets`. Il prefetch, nato per
togliere quell'I/O dal critical path, oggi lo aggiunge solo come lavoro sprecato.

### Perché è invisibile

1. `internal/capabilities/scripts/runner.go:314` → `NewRunner` inizializza
   `log: zap.NewNop()`; `SetLogger` (`runner_deps.go:40`) **non ha call site non-test**
   (verificato su tutto il repo). Il warn
   `"audio prefetch failed — audio compile will run with synchronous resolution"`
   (`runner_execution.go:330`) non può quindi mai comparire in produzione: nei log del run
   ci sono **0** righe del logger `scriptgeneration`.
2. `AudioPrefetch *AudioPrefetchResult json:"-"` (`model_result.go:184`) → il payload di
   polling non dice nulla su prefetch (asset risolti, path, durata, hit/miss).
3. Nessuna metrica dedicata: il prefetch non compare in `timing.operations`.
4. `audio_metrics.clip_audio_prepare_ms = 0` anche quando il prepare sincrono scarica
   ~215 MB: `runner_phase_audio.go:132` assegna
   `compileTimings.ClipAudioPrepareMS = clipPrepareMS`, ma la riga successiva
   `..., compileTimings, err = CompileCanonicalAudioPlanAudioOnlyWithIntents(...)`
   **sovrascrive l'intera struct** con il valore misurato dentro il compile
   (`audio_timeline.go:479`). Il costo reale del prepare viene perso.

### Meccanica nel codice

- `runner_execution.go:266-345` (`parallelFanOut`): `prepareCtx, cancelPrepare := context.WithCancel(e.ctx)`
  con `defer cancelPrepare()`; il prefetch gira sul ramo assets con `prepareCtx`.
  Quando il join completa (`semanticDone` + `assetsDone`), `cancelPrepare()` uccide
  qualunque goroutine del ramo ancora viva.
- `audio_prefetch.go:112-175`: `concurrent.WithContext(ctx)` con **first-error-wins**:
  al primo errore il gruppo cancella il proprio ctx → un solo asset che fallisce
  (`g.Go("prefetch-bgm-sfx-*")` / `"prefetch-clip-audio-*"`) **butta via** tutti gli asset
  già risolti e fa tornare `(nil, err)`; il chiamante lascia `prefetched = nil` e
  `audio_compile` rifà tutto con l'adapter reale.

## FIX + RIVERIFICA (stesso giorno, build `6ac4aecc0` + fix)

I punti 1–4 sopra sono stati corretti e riverificati a cache clip-audio fredda.

| | run 16:08 (pre-fix) | run 16:40 (fix 1ª tornata) | run 16:46 (fix completo) |
|---|---|---|---|
| job | `job_1790093191569826084_fbcfed1c` | `job_1790095227964156605_5e1e3e84` | `job_1790095581868067261_4eb69ac2` |
| wall job | 159 s | **135 s** | **91 s** |
| `render_metrics.wall_ms` | 57.6 s | 50.2 s | 24.4 s (render riusato dal cache deterministico) |
| `audio_prefetch` nel polling | assente (`json:"-"`) | presente, **degraded** (alias BGM/SFX non canonicalizzati) | presente, **8/8 consegnati, non degradato** |
| clip audio materializzati dal prefetch | 0 (cancel in volo) | 5/5 | 5/5 (dedup: richiesti**5**, non 10) |
| BGM/SFX | mai risolti dal prefetch | falliti (`bgm1 not found`) | risolti via alias→Drive id (3/3) |
| log prefetch in produzione | assente (logger nop) | `WARN audio prefetch degraded` | nessun warn (referenza pulita) |
| `parent_job_id` sulla coda remota | `-` | **`prefetch-fix-1790095227`** | (render riusato, nessun nuovo job) |

Dettaglio del run finale (`audio_prefetch` nel payload di polling):

```json
{"bgm_requested":1,"sfx_requested":2,"clip_audio_requested":5,
 "bgm_sfx_resolved":3,"clip_audio_ready":5,"duration_ms":21283,
 "assets":[
   {"kind":"bgm","asset_id":"bgm1","resolved_id":"1X4-wfIwrR51eDxIegciuBAJzKSdP3gcX","ok":true,"duration_ms":193},
   {"kind":"sfx","asset_id":"whoosh1","resolved_id":"1T7TJuqrwtvR3se1nlvY2k19lA5zAOODs","ok":true,"duration_ms":5},
   {"kind":"sfx","asset_id":"whop1","resolved_id":"1Fgr2jWQC1G6EHo-jhBAwjGtdcZo1PfaX","ok":true,"duration_ms":28},
   {"kind":"clip_audio","asset_id":"yt_Bc9gTqiljLA_0_36_v1","ok":true,"duration_ms":17423},
   {"kind":"clip_audio","asset_id":"yt_pfaIAdqvlig_457_509_v1","ok":true,"duration_ms":17354},
   {"kind":"clip_audio","asset_id":"yt_pfaIAdqvlig_929_980_v1","ok":true,"duration_ms":11412},
   {"kind":"clip_audio","asset_id":"yt_tnoMGevqWAM_1841_1903_v1","ok":true,"duration_ms":21283},
   {"kind":"clip_audio","asset_id":"yt_vLRjqTIiMjc_211_256_v1","ok":true,"duration_ms":20823}]}
```

`clip_audio_prepare_ms = 0` adesso è la verità: tutte le sorgenti erano in cache
dal prefetch (prima mentiva perché il valore misurato veniva sovrascritto dal
compile). Render 5/5 UPLOADED, `sha256` certificati, 0 warning.

Report della catena completa (master + coda + worker + telemetria + cache):

```bash
scripts/collect_chain_debug.sh job_1790095227964156605_5e1e3e84
# → ops/benchmarks/chain-debug/job_1790095227964156605_5e1e3e84.md
```

dove la tabella della coda remota mostra `parent_job_id = prefetch-fix-1790095227`
per tutti e 5 i job di render: l'anello master → coda è finalmente univoco.

## Note operative

- Il materializer audio usa uno scratch dedicato (`data/tmp/audioassets`,
  `script_generation_runtime.go:231`), separato dal CAS dei render
  (`data/tmp/materialized/...`): anche quando il render ha già il clip in CAS,
  il ramo audio riparte da un download Drive.
- Un job di maintenance ("Starting system-wide cleanup", 16:07:59) gira in parallelo:
  da tenere presente quando si legge il timing dei download.

## Riproduzione

```bash
cd refactored
mv data/tmp/audioassets/assets data/tmp/audioassets/assets.warm-$(date +%H%M%S)   # cache fredda
curl -X POST http://127.0.0.1:8000/api/script/generate \
  -H "Authorization: Bearer $VELOX_ADMIN_TOKEN" -H 'Content-Type: application/json' \
  -H "Idempotency-Key: prefetch-cold-$(date +%s)" \
  -d @ops/jobs/dolly_parton_5clips_wm_dualbg_subsstyle.generate.json
# poi: journalctl -u pipelinegen -o cat --since ... | grep -E 'canonical_materializer|prefetch'
#      find data/tmp/audioassets/assets -printf '%TH:%TM:%TS %f\n'
```

Evidenze: `poll-final.json` (run 1), `poll-final-cold2.json` (run 2), `last-job.txt`,
log completo del run 2 in `/tmp/run2.log`.
