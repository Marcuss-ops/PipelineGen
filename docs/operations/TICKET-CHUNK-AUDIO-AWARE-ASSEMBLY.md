# TICKET: Audio-Aware Chunk Assembly — Sblocco del Chunking per i Clip Source-Only (VeloxEditing)

- **Data:** 2026-10-27
- **Autori:** Pierone + Buffy (Codebuff)
- **Moduli interessati:** `refactored` (PipelineGen: `cliprender`, `internal/platform/renderinggen`), `RenderingGen` (`internal/processor`), `Chronon3d` (solo verifica, nessuna modifica attesa), `BrainRust` (nessuna modifica — contratto artefatto invariato)
- **Stato:** ATTIVO / PROPOSTO DI SBLOCCO
- **Obiettivo:** Rendere il chunking realmente attivo per i clip source-only lunghi (children video-only + mux sorgente una sola volta all'anchor), riducendo la latenza per clip da ~1× a ~1/N sul pool di worker, senza cambiare il contratto dell'arte-fatto pubblicato.

---

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
- Test pin: `TestChunkPolicyIsDormantForEverySealedPlan`, `TestChunkPolicyRefusesPlansWithAudio` (`clip_executor_test.go`)
