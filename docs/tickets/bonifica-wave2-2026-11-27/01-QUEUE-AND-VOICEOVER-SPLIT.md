# 01 — Split di `queue_client.go` e `runner_phase_voiceover.go`

**Data:** 2026-11-27
**Priorità:** P1
**Stato:** DONE
**Area:** `internal/platform/renderinggen`,
`internal/capabilities/scripts`

## 1. Problema

I due file erano gli hotspot di churn citati dal secondo audit (§9):

- `internal/platform/renderinggen/queue_client.go`: 743 righe con due
  responsabilità distinte — adattatore wire del client queue e executor del
  clip render (submit, chunking, settle) — nello stesso file, dopo che
  artifact/batch/materialize/metrics erano già stati estratti.
- `internal/capabilities/scripts/runner_phase_voiceover.go`: 653 righe con
  quattro responsabilità — pianificazione del lavoro, orchestrazione della
  fase, sintesi concorrente, proiezione delle metriche.

Nessuna delle due è un problema di dimensione pura: è la proprietà delle
goroutine e la sequenza submit→settle a diventare difficile da seguire.

## 2. Split del queue client

| File | Responsabilità | Contenuto |
| --- | --- | --- |
| `queue_client.go` | adattatore wire | `Client`, `AssetPrefetcher`, `Capabilities/Submit/Get/Children/Wait*/Cancel/Retry` e conversioni `toScriptJob`, `fromQueueProgress`, `toQueueAssets`, `fromQueueAssets` |
| `clip_render_executor.go` | configurazione dell'executor | `ClipRenderExecutor`, `RenderCapabilitySource`, preflight GPU, `SetPollInterval`, `SetChunking`, `RenderJobID`, `jsonEquivalent` |
| `clip_render_submit.go` | submit | `Submit`: mapping del piano, asset refs, prefetch, enqueue e recovery `ErrRenderRequestConflict`/`ErrJobExists` |
| `clip_render_chunking.go` | chunking | `chunkPolicy`, `SubmitChunked`, `rearmChunkFamily` |
| `clip_render_settle.go` | settle e proiezione | `Settle`, `SettleWithin`, `SettleWithJobID`, `settle`, proiezione `RenderOutcome` e metriche |

API invariata: nessun simbolo esportato è cambiato e nessun chiamante è stato
toccato (tutti i file sono nello stesso package).

## 3. Split del voiceover runner

| File | Responsabilità |
| --- | --- |
| `runner_phase_voiceover.go` | tipi del lavoro, `buildVoiceoverWorkForLanguages`, `auditVoiceoverDispatch`, `sanitizeNarrationForTTS` |
| `runner_phase_voiceover_phase.go` | `runVoiceoverPhase` (pianificazione + dispatch) |
| `runner_phase_voiceover_synthesis.go` | `synthesizeVoiceoverWork` (fan-out TTS, ownership delle goroutine) |
| `runner_phase_voiceover_metrics.go` | `projectVoiceoverMetrics` (aggregazione e ledger) |

Anche qui nessuna modifica di logica: solo spostamento di dichiarazioni e
import. Le goroutine restano possedute dal `WaitGroup`/canale della fase.

## 4. Verifiche

| Comando | Esito |
| --- | --- |
| `gofmt -l` sui file toccati | nessun file |
| `go build ./internal/platform/renderinggen/` | exit 0 |
| `go vet ./internal/platform/renderinggen/` | exit 0 |
| `go test ./internal/platform/renderinggen/ -count=1` | ok |
| `go build ./internal/capabilities/scripts/` | exit 0 |
| `go test ./internal/capabilities/scripts/ -count=1` | ok (26 s) |
| `go test ./internal/... ./pkg/... -count=1` | exit 0, nessun FAIL |
| `go run ./cmd/archcheck --strict` | `passed=true`, 0 violazioni |

## 5. Budget architetturale

Lo split P1-11 aggiunge tre file al package root `internal/capabilities/scripts`
(62 → 65 file, 18.696 → 18.715 LOC). Il registro
`architecture/package_hotspots.json` è stato aggiornato con la motivazione:
non è un'esenzione, perché il tetto globale `max_files_per_package=65` resta
invariato e qualunque crescita successiva fallisce subito.

## 6. Cosa non è stato fatto

- `OverlayPlanningInput` continua a ricostruire un `GenerateResult` completo in
  `document()`: l'immutabilità è garantita per i campi che la branch scrive, non
  dal tipo. Resta il residuo P1-9.
- La suite completa con `-race` non è stata rieseguita in questa sessione; è
  verde senza `-race`.
