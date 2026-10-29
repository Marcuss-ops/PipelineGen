# TICKET: Audio-Aware Chunk Assembly — Sblocco del Chunking per i Clip Source-Only (VeloxEditing)

- **Data:** 2026-10-27
- **Autori:** Pierone + Buffy (Codebuff)
- **Moduli interessati:** `refactored` (PipelineGen: `cliprender`, `internal/platform/renderinggen`), `RenderingGen` (`internal/processor`), `Chronon3d` (solo verifica, nessuna modifica attesa), `BrainRust` (nessuna modifica — contratto artefatto invariato)
- **Stato:** IN IMPLEMENTAZIONE — sblocco atterrato su tutti e tre i moduli (v. §9); parità su fixture reale verificata, gate completi e attivazione operativa ancora aperti
- **Obiettivo:** Rendere il chunking realmente attivo per i clip source-only lunghi (children video-only + mux sorgente una sola volta all'anchor), riducendo la latenza per clip da ~1× a ~1/N sul pool di worker, senza cambiare il contratto dell'arte-fatto pubblicato.

---

## 0. TODO — cosa manca (agg. 2026-10-28; re-rerun 2026-10-29)

Pending dalla sessione Freebuff 2026-10-27 (§9.4 residui + scoperte del confronto parent assemblato/single-job):

- [x] **Divergenza AAC priming: confermata RISOLTA sul worker corrente (rerun 2026-10-29).** `TestAudioAwareChunkParentMatchesSingleJob` → PASS (7,15 s) con `CHRONON_BINARY` = release Chronon3d del 29-10 (`build/chronon/linux-video-release/apps/chronon3d_cli/chronon3d_cli`, 164 MB, build 13:17): parent assemblato vs single-job coincidono su fatti di output sealati (DeepEqual), durata, 360 frame decodificati frame-per-frame e hash sha256 dei pacchetti audio non-negativi (il priming AAC a PTS negativo è escluso da ENTRAMBE le parti: il mux single-job e l'assembler replicano la stessa selezione di origine — v. §9.4 e §10.1.3). La divergenza restava solo nell'inventario del 28-10: non osservata sul codice/binario corrente. Provatelo su una fixture: sine 997 Hz, 12 s, AAC, 2 child GOP-aligned (180 frame ciascuno). **Perimetro invariato: la verifica riguarda questa fixture, non ogni formato di sorgente (§9.4).**
- [x] Regressioni RenderingGen dopo la prova: `go test ./... -count=1` in `RenderingGen/renderinggen` → PASS completo (exit 0, 2026-10-29).
- [x] Completare i gate `refactored`: `make verify-agent` → **PASS** (dopo gofmt su 4 file dello split, 2026-10-29) e `make verify-main` → **PASS** (go test completo + verification di architettura, 2026-10-29). La suite Chronon3D completa e i pin C++ `Audio-aware assembly` sono in ritorna di esecuzione sul build corrente (esito qui sotto).

## 1. Storia e Perché Questo Ticket Esiste

- Il producer dei chunk (I1) fu implementato (`chunk_plan.go` in `cliprender`, `chunk_producer.go` in `internal/platform/renderinggen`, `SubmitChunked`) ma il relativo ticket fu **chiuso come wont-fix** il 2026-10-11 (decisione registrata in `AGENTS.md`, "Documentation rule"; i dettagli vivono nella git history). Il machinario è rimasto collegato ma dormiente.
- Oggi la produzione **configura** comunque il chunking (`clip_render_runtime.go` → `executor.SetChunking(...)`) e la policy dichiara onestamente la dormienza: il guard in `chunkPolicy` (`queue_client.go`) rifiuta ogni piano con audio, e il test `TestChunkPolicyIsDormantForEverySealedPlan` pinna il fatto che **nessun piano valido può mai essere chunkato**.
- Questo ticket ribalta la decisione wont-fix con un design di sblocco concreto e verificabile. L'alternativa "video-only senza assembly audio-aware" è irricevibile: la validazione del seal richiede una audio mode (`copy_if_compatible|transcode`) su ogni piano valido, quindi il nicho "piano senza audio" accettato dalla policy è esattamente l'insieme vuoto dei piani producibili.

## 2. Diagnosi: la Contraddizione che Teneva (e Tene) il Chunking Dormiente

Tre anelli formano un cerchio chiuso:

1. **`plan_seal_validation.go` (cliprender):** ogni piano valido DEVE portare `Audio.Mode` ∈ {`copy_if_compatible`, `transcode`}. Un piano senza audio fallisce `Validate()` ("invalid audio mode").
2. **`queue_client.go` → `chunkPolicy` (platform/renderinggen):** rifiuta di partizionare ogni piano con `plan.Audio.Mode != ""` — cioè tutti i piani validi. Motivo storico: l'assembly dei chunk è video-only (concat packet-copy), e spezzare un piano con audio può produrre child falliti o un parent senza audio.
3. **`Submit` e `RenderJobID`:** validano il piano PRIMA della policy (`Submit` → `plan.Validate()`; il percorso anchor di `RenderJobID` → `BuildChunkSet` → `Validate()`), quindi nemmeno il nicho teorico "video-only" della policy è percorribile.

Risultato: `SetChunking` è cablato in produzione ma **non può mai attivarsi**. Le pin attuali (`TestChunkPolicyRefusesPlansWithAudio`, `TestChunkPolicyIsDormantForEverySealedPlan`) documentano la dormienza onesta invece di fingere che il chunking sia vivo.

## 3. Perché l'Audio Blocca l'Assembly Oggi

- I child renderizzano il `rawPlan` COMPLETO + `FrameRange` (`chunk_producer.go`): ogni finestra porterebbe con sé la politica audio su un sotto-range, ma i confini delle finestre sono allineati ai keyframe (GOP certificato, es. 48 frame), **non** ai pacchetti audio (frame AAC ≈ 21,3 ms): i bordi audio e video non coincidono.
- L'assembly è `assemble_segments` (Chronon): copia pacchetti senza decodifica, richiede child copy-eligible, closed-GOP, keyframe-first. Un parent che porta audio resta MP4 classico; un parent silenzioso può essere MP4 frammentato (`FragmentedMP4: audioPath == ""` in `pipeline.go`).
- Il percorso P4 multi-segmento ha lo stesso vincolo: il trigger richiede `audioPath == ""`.
- Invariante del master: l'audio canonico entra **una sola volta** nel master finale (`mux_audio_copy` in BrainRust); nessun componente deve sintetizzare o doppiare l'audio.

## 4. Design di Sblocco: Audio-Aware Assembly (children video-only + mux all'anchor)

**Principio: separa i due piani.** Il video si divide e si assembla in copia pacchetto (già funzionante); l'audio si applica UNA volta all'anchor, sul video già assemblato, con la stessa politica del master (`mux_audio_copy` — copia del flusso sorgente, mai trascode).

1. **Child video-only:** il producer sottomette ogni finestra con uno spec child esplicitamente *video-only* (nessun `AudioSourcePath`, nessun blocco audio): ogni child produce un segmento silenzioso, frammentato, copy-eligible, closed-GOP, keyframe-aligned — esattamente ciò che l'assembly packet-copy vuole. I confini audio smettono di esistere come problema.
2. **Anchor audio-owner:** l'anchor (assembly-only) concatena i segmenti (percorso esistente) e poi muxa il flusso audio sorgente intero una sola volta. L'artefatto finale ha lo **stesso contratto** del render single-job di oggi (MP4 classico, `AudioStreams: 1`, audio copiato byte-a-byte): nessun cambiamento a valle per BrainRust, Drive o il master.
3. **Policy audio:** `copy_if_compatible` mappa naturalmente (copia del sorgente). `transcode` non è mai stato applicato dalla mux nativa (metrica `AudioModeUnsupported`: "requested audio mode is NOT applied") — il mux copy all'anchor è coerente con la semantica reale degli artefatti odierni.

## 5. Lavori per Modulo

| Modulo | Lavoro | Note |
| :--- | :--- | :--- |
| `refactored` — `platform/renderinggen` | Sostituire il guard di dormienza in `chunkPolicy` con il contratto audio-aware (consentire piani con audio quando il percorso mux-anchor è disponibile); child spec video-only + anchor audio-owner in `chunk_producer.go` | Mantenere `ChunkContractVersion` coerente: se lo shape dei job cambia, bumpare `clip-chunk.v1` per non rimintare identità ambigue |
| `refactored` — `cliprender` | Estendere `BuildChunkSet`/contratto child (campo esplicito video-only), aggiornare le pin: `TestChunkPolicyIsDormantForEverySealedPlan` diventa pin di ATTIVAZIONE | La partizione deterministica keyframe-aligned non cambia |
| `RenderingGen` — `internal/processor` | Finalizzazione anchor: dopo il concat packet-copy, mux audio sorgente una volta (riusare il percorso mux copy già certificato per il master); child senza audio → `FragmentedMP4` invariato | `finalization.go` resta l'unico owner dell'audio del parent |
| `Chronon3d` | Verifica sola: `assemble_segments` su N segmenti silenziosi + finestre keyframe-aligned | Nessuna modifica attesa; il producer cita già la certificazione della parallel admission policy come precondizione opt-in |
| `BrainRust` | Nessuna modifica: il contratto artefatto clip (MP4 classico con audio sorgente) è identico | Regola single-owner audio preservata: il master muxa l'audio del master, l'anchor quello della clip |

## 6. Criteri di Accettazione (Verificabili)

1. **Attivazione:** un piano valido (audio mode `copy_if_compatible`) più lungo di `chunkDuration` con `SetChunking` configurato viene partizionato: `chunkPolicy` ritorna `ok` con N finestre + anchor, sottomessi come famiglia atomica (`SubmitBatch`). Le pin attuali di dormienza sono sostituite da pin di attivazione.
2. **Child video-only:** ogni child risulta `AudioStreams: 0` nei fatti certificati, closed-GOP, copy-eligible, finestre keyframe-aligned con copertura esatta dei frame (nessun gap/overlap — pin già esistenti in `BuildChunkSet`).
3. **Parent audio-owner:** l'anchor assemblato porta `AudioStreams: 1` (copia del sorgente) ed è MP4 classico; il video concat è esatto (conteggio frame uguale al render single-job).
4. **Unico owner audio:** pin unitario sul producer — nessun child porta mai `AudioSourcePath`; solo l'anchor muxa.
5. **Equivalenza artefatto:** parent assemblato vs render single-job dello stesso piano — stream audio identico (copia ⇒ hash del flusso uguale), durata e fatti di output certificati equivalenti (test con fixture in RenderingGen).
6. **Gate:** `make verify-agent` e `make verify-main` verdi in `refactored`; `go test ./...` verde in RenderingGen; suite Chronon3d verde se toccata.
7. **Rollback:** il chunking resta un'ottimizzazione di trasporto pura — una feature flag/policy mantiene disponibile il percorso single-job; disabilitarla ripristina il comportamento odierno senza cambiare gli artefatti.

## 7. Rischi

- **Doppio mux audio** (anchor + master): mitigato dal vincolo single-owner — la clip finita con audio entra nel master come oggi; il master non re-muxa la clip.
- **Identità dei job chunk:** ogni cambio di significato delle finestre richiede bump di `ChunkContractVersion` (regola già scritta in `chunk_plan.go`) per non riusare ID con significato nuovo.
- **Ammissione parallela GPU:** il producer è opt-in finché la parallel admission policy di Chronon non è certificata sulla GPU target (precondizione già dichiarata in `chunk_producer.go`): lo sblocco dell'audio non abilita da solo il chunking in produzione — rimuove solo il blocco strutturale, l'ammissione resta il gate operativo.

## 8. Riferimenti

- `refactored/internal/platform/renderinggen/queue_client.go` — `chunkPolicy` (guard audio + commento dormienza), `SetChunking`, `SubmitChunked`
- `refactored/internal/capabilities/cliprender/chunk_plan.go` — partizione deterministica, `BuildChunkSet`, `ChunkContractVersion`
- `refactored/internal/platform/renderinggen/chunk_producer.go` — famiglia anchor+children, `FrameRange`
- `refactored/internal/capabilities/cliprender/plan_seal_validation.go` — audio mode obbligatoria sui piani validi
- `RenderingGen/renderinggen/internal/processor/pipeline.go` — `AudioSourcePath`, `FragmentedMP4: audioPath == ""`, metrica `AudioModeUnsupported`
- `RenderingGen/renderinggen/internal/processor/finalization.go` — assembly packet-copy, gate copy-eligibility
- Test pin: `TestChunkPolicyChunksAudioPlans`, `TestChunkProducerChildrenAreVideoOnlyAndAnchorOwnsAudio`, `TestParentFinalizerMuxesSourceAudioOnce`, `TestParentFinalizerSilentAnchorStaysFragmented`

## 9. Stato di Implementazione (2026-10-27)

Lo sblocco è atterrato su tutti e tre i moduli, con pin su ogni contratto:

### 9.1 `refactored` — famiglia audio-aware
- `MapClipPlanToOverlayPlanVideoOnly` (`clip_plan_mapper.go`): la shape child del piano — identica all'anchor tranne il blocco `audio` omesso (lo schema `overlay-plan.v1` dichiara audio opzionale: nessun cambio contratto). `MapClipPlanToOverlayPlan` resta la shape single-job con audio.
- `chunk_producer.go`: i child sottomettono la mapping video-only, l'anchor la mapping completa (audio + source) — pin `TestChunkProducerChildrenAreVideoOnlyAndAnchorOwnsAudio`.
- `ChunkContractVersion` → `clip-chunk.v2`: il contenuto render di una finestra è cambiato (child video-only), quindi le identità v1 non vengono riusate.
- `chunkPolicy`: guard di dormienza RIMOSSO — i piani con audio si chunkano; pin `TestChunkPolicyChunksAudioPlans` (finestre (2,48), `RenderJobID` = anchor).

### 9.2 `RenderingGen` — il finalizer è l'audio owner
- `chronon.AssembleRequest` += `AudioSourcePath` + `AudioTargetSampleRate` (stessi nomi wire e stessa semantica di `RenderRequest`: copy verbatim del flusso intero, rebase a PTS 0, resample solo se il target rate differisce).
- `finalization.go`: `anchorAudioSource` è la SINGLE TYPED AUTHORITY — legge il piano anchor via `overlay.CompileSemantic` (lo stesso compiler del render path), risolve il source asset dal compile asset set, materializza via store e passa una sola richiesta di mux all'assembler; parent con audio ⇒ MP4 classico. Fail-closed: piano non decodificabile, audio senza source layer, source asset assente ⇒ errore (mai parent silenzioso che violerebbe il contratto clip).
- Pin: `TestParentFinalizerMuxesSourceAudioOnce` (un solo mux, rate dichiarato, source materializzato verificato per hash) e `TestParentFinalizerSilentAnchorStaysFragmented`.

### 9.3 `Chronon3d` — `assemble_segments` audio-aware
- `SegmentAssemblyRequest` += `audio_source_path` + `audio_target_sample_rate`; `assemble_segments` dichiara lo stream audio sorgente sul `MuxSession` PRIMA dell'header e copia i pacchetti DOPO i segmenti — stesso ordine e stessa semantica packet-copy di `NativeAvEncoder::mux_source_audio` (il percorso `--gop-source` del render single-job), quindi l'artefatto assemblato è ciò che il render single-job avrebbe prodotto.
- Fail-closed: segmenti con audio + source audio ⇒ rifiuto ("one artifact carries one audio owner"); source senza stream audio ⇒ rifiuto.
- Daemon IPC `AssembleSegments` payload esteso: `{input_paths, output_path, fragmented_mp4, audio_source_path, audio_target_sample_rate}`.
- Test C++: "Audio-aware assembly muxes one source stream over video-only segments" (20 packet video, stream AAC presente) e "Audio-aware assembly refuses a second audio owner and a silent audio source". Suite completa `chronon3d_cli_tests`: 261/261 verdi.

### 9.4 Residui dichiarati
- **Criterio 5 (equivalenza artefatto)**: `TestAudioAwareChunkParentMatchesSingleJob` passa sul worker corrente con una fixture AAC di 12 secondi e due child; confronta fatti di output, durata, 360 frame decodificati e hash dei pacchetti audio non negativi della sorgente con parent e single-job. La verifica riguarda questa fixture, non ogni formato o configurazione.
- **Pin C++ "esattamente una volta"**: i due test `Audio-aware assembly` passano (59 assertion); il conteggio riguarda i pacchetti con PTS/DTS non negativo, coerenti con l'origine del single-job. Il pacchetto AAC di priming negativo è escluso anche nell'assembler.
- **Gate**: `go test ./... -count=1` e `go vet ./...` passano in `RenderingGen/renderinggen`; `make verify-agent` passa in `refactored` (17 componenti da cache, 3 eseguiti). `verify-main` e la suite Chronon3d completa non sono stati rieseguiti, quindi il ticket non è chiuso. Il test Vulkan surface materializer, precedentemente bloccato al linking, è stato ricompilato e passa 27 test / 162 assertion.
- **Ammissione parallela GPU**: resta il gate operativo per l'attivazione in produzione (precondizione già dichiarata in `chunk_producer.go`); `SetChunking` resta l'opt-in cablato.

## 10. Scoperte dall'Implementazione (2026-10-27)

### 10.1 Scoperte architetturali (utili per chi riprende il thread)
1. **Il mux canonico di Chronon supportava GIÀ l'audio**: `MuxSession` espone `AudioStreamConfig`, `submit_audio`, `AudioResampler` (copy verbatim, trim, resample con `target_sample_rate`). Lo sblocco non ha richiesto nessun nuovo primitivo di mux — solo esporlo in `assemble_segments`.
2. **Lo schema `overlay-plan.v1` dichiara `audio` OPZIONALE** (non è nel `required`, nessun campo obbligatorio dentro): i child video-only sono contrattualmente validi SENZA alcuno cambio di schema o ai parity test. Un cambio contratto non sarebbe stato necessario nemmeno per l'anchor.
3. **Semantica audio del render single-job**: `NativeAvEncoder::mux_source_audio` copia i pacchetti sorgente nella finestra `audio_start/end_seconds`, DOPO i pacchetti video e PRIMA del trailer. Con origine zero esclude i pacchetti con PTS/DTS negativo o assente; l'assembler replica tale selezione per l'anchor. La parità è verificata sulla fixture indicata in §9.4, non inferita per tutte le sorgenti.
4. **Le due single typed authority del percorso audio**: `audioSourcePathFromPlan` (render path, legge `Plan.Output.Audio` + layer "source" del piano compilato) e `anchorAudioSource` (finalizer, `overlay.CompileSemantic` sul piano anchor + source asset dal compile asset set). Nessuno dei due legge il piano semantico "a mano".
5. **Producer: due mapping dello stesso piano sealed** — child = mapping video-only, anchor = mapping completa. Il contratto wire non cambia; cambia solo quali blocchi vengono omessi.
6. **`clip-chunk.v1` → `clip-chunk.v2`**: il contenuto render di una finestra è cambiato (child video-only), quindi nuove identità job. Nessuna famiglia v1 esiste in nessuna coda: la dormienza storica garantisce zero legacy.

### 10.2 Scoperte operative sull'ambiente di build/verifica
1. **Build Chronon3d**: `ninja -j 12` OOM-kill di `cc1plus` (32 GB RAM con llama-server residente ~5 GB + 3 daemon); `-j 3` è stabile. Build completo del target test ≈ 20-25 minuti da freddo.
2. **Gate lenti per gitleaks**: `verify-agent`/`verify-main` spendono ~5 minuti nella sola scansione secret (10.5 GB); non è un hang.
3. **Build Go concorrenti vs build cache**: con più build paralleli può comparire `could not import ... (open : no such file or directory)` (export data evictata) — è un artefatto di cache, NON una regressione: riprendere con la cache calda.
4. **Tree condiviso con altri agent concorrenti**: durante questa sessione un altro processo Freebuff ha lavorato in parallelo su `refactored/cmd/semantic-extract/`, `refactored/internal/platform/sqlite/twinddl/` e 13 file Chronon3d (`image_writer.cpp` etc.). Conseguenze osservate: `verify-format` e `verify-unit-fast` rossi per file estranei (enforcement atomicwrite su `cmd/semantic-extract`), rebuild Chronon3d bloccato da un errore TBB nei LORO file. Regola acquisita: ogni gate rosso va diagnosticato per OWNERSHIP (git status + mtime) prima di toccare codice — e i fix di formattazione su file altrui vengono sovrascritti dal lavoro in corso.
