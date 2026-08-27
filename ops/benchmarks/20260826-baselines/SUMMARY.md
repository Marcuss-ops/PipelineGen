# Baseline 2026-08-26 — sintesi

Eseguite oggi alle ~10:55 su worker `YOutube_219819_1` con binario **pre-interventi P0** (il commit `28e75f86d` — split metrico finalize + concurrency 4 — è arrivato alle 12:22 UTC, dopo questi run). I numeri della tabella sono quindi il punto di partenza storico: il prossimo run con HEAD misura anche lo split operativo di `post_writer_finalize`.

| Baseline | job | wall | post_writer_finalize | bottleneck | audio (mix+AAC+upload) | LLM (ollama) | TTS |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 20-clip-cold | `cf2ad88e` | **215.2s** | 68.5s | generate | 24.6s+23.0s+6.1s | 71.8s | 76.6s |
| 10-clip-cold | `7b0af271` | **120.7s** | 40.8s | post_writer_finalize | 10.4s+15.2s+4.3s | 38.5s | 34.9s |

## Interventi P0 attivi dal binario HEAD (2026-08-26)

### 1. Split metrico di `post_writer_finalize`

Lo stage non è più una scatola nera: il RunReport espone le quattro operazioni misurate via `kernobs.MeasureOperation`, attribute allo stage con `kernobs.WithStage(ctx, "post_writer_finalize")` (fallback neutro `publish` per i publisher diretti):

| Operazione | Cosa misura | Proprietario |
| --- | --- | --- |
| `finalize.artifact_prepare` | validazione envelope fail-fast per artifact | `finalizer.ArtifactPreparation.Prepare` |
| `finalize.artifact_hash` | SHA-256 on-disk (os.Stat + digest) | `remote.VerifyArtifact` |
| `finalize.drive_publish` | upload Drive (`PublisherPort.Publish`) | `finalizer.ArtifactPreparation.Prepare` |
| `finalize.completion_tx` | transazione terminale single-TX SQLite | `broker.CompleteWithArtifacts` |

### 2. Pubblicazione Drive bounded-parallel (4 worker)

La preparazione/upload degli staged artifact gira su errgroup con limite 4 (`finalizePublishConcurrency` sul broker in-process, `artifactPublishConcurrency` sull'use case HTTP complete-with-artifacts):

- ordine manifest preservato (risultati scritti per indice input)
- fail-fast alla prima failure: nessun partial-success, la richiesta fallisce come nel contratto sequenziale
- dedup dei retry concorrenti garantito da IdempotencyKey + ConflictSkip Drive
- la `CompleteWithArtifacts` terminale resta una singola transazione SQLite atomica, eseguita DOPO tutte le pubblicazioni

Test che fissano il contratto: `TestBroker_CompleteWithArtifacts_BoundedParallelism`, `TestPublishAndCompleteUseCase_BoundedParallelism` (bound raggiunto ma mai superato, TX strettamente successiva).

### 3. Voiceover intermedi: O(N) → O(1) upload su finalize

I voiceover per-scena NON sono più emessi come artifact del manifest finalize: la pipeline TTS li pubblica su Drive durante la generazione (`VoiceoverItemExecutor`: TTS → publish → finalize) e idrata i binding scene con il DriveLink. Il manifest finalize publica solo:

- `script.json` (required)
- `scenes.json` (optional, con local path stripzati)
- `final_audio.m4a` certificato

Su un job 20 clip gli artifact scendono da 23 a 3 (~20 upload × ~2,5 s risparmiati a monte della transazione). Consumer verificati: Google Doc ← `Bindings.Voiceover.Link`; scenes/script JSON ← bindings; audio compile/render ← path locali + master; Qdrant ← audio REGISTERED-only. Gli asset legacy con binding TTS pre-esistenti restano risolti dalla reconciliation asset-location (VERIFIED/UPDATED preservati, link rotti puliti fail-closed).

## Prossimo run atteso (HEAD)

Con il binario HEAD il report conterrà lo split `artifact_prepare`/`artifact_hash`/`drive_publish`/`completion_tx`, beneficiando già del parallelismo a 4 e del manifest O(1): l'attesa teorica è `post_writer_finalize` da ~68 s verso ~12–25 s sul job 20 clip, senza cambiare qualità né contratto atomico.

## 20-clip WARM — FALLITO (da ripetere)

`job_1787742092785484662_5f036405` (`matt-damon-20-clips-baseline-warm-20260826-105515-request`)
- Eventi: `job_running` → `leased` (11:06:33) → `job_failed` (11:07:44, dopo 71s)
- Errore: `generate job handler: read durable run result: context canceled`
- Diagnosi: **cancellazione del contesto client/submitter**, non un errore di pipeline. Il worker stava ancora lavorando quando il contesto HTTP del chiamante è scaduto/è stato cancellato (nessun errore applicativo nel run).
- Azione: rilanciare con timeout lato client adeguato (es. ≥ 15 min) e con il binario HEAD per catturare anche lo split finalize.
