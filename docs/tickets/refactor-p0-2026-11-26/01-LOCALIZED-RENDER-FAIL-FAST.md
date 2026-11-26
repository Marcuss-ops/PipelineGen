# P0-01 — Render localizzato: il nil enqueuer non deve essere un no-op

**Data:** 2026-11-26
**Priorità:** P0 (diagnosticabilità e lavoro speso su un job che non può completare)
**Stato:** **DONE**
**Area:** `internal/capabilities/scripts`, `internal/app/wiring`

## 1. Problema verificato

`Runner.enqueueLocalizedRender` apriva con:

```go
if r == nil || r.localizedRenderEnqueuer == nil {
    return nil
}
```

Il composition root (`wireLocalizedRenderEnqueuer`) è dichiaratamente
**best-effort**: se manca il localization service, il text-track store, il cue
writer o l'asset resolver, logga un warning e lascia il campo `nil`.

Il run **non** dichiara un falso successo: `completeRun` applica
`renderSetSatisfiesExpectation(expected, successful, failed)` e richiede
`successful == expected && failed == 0`; un render mancante produce
`INCOMPLETE_RENDER_SET`. Il difetto è il **quando** e il **messaggio**:

- il fallimento arriva a fine run, dopo script, traduzioni, TTS e audio;
- il codice `INCOMPLETE_RENDER_SET` non dice che il renderer non era cablato;
- il percorso `final_job_subtitles` invece fallisce subito con
  `localized renderer is not wired`, quindi i due percorsi non hanno lo stesso
  contratto.

## 2. Soluzione

1. Sentinella `ErrLocalizedRendererNotWired` in `runner_localized_render.go`.
2. `enqueueLocalizedRender` restituisce la sentinella quando l'enqueuer è nil
   (e un errore esplicito se il receiver è nil), invece di `nil`.
3. `setSceneRenderExpectations` diventa un metodo del runner e, quando
   `req.Source.Type == SourceClips && req.Render.Enabled` e
   `expectedRenderUnits(req, scenes) > 0`, rifiuta il run **prima** di
   TTS/render con la sentinella.
4. `assembleSceneTextResult` propaga l'errore con
   `failExecutionStepBestEffort` + `failRunWithRetry`, come gli altri gate della
   fase.

## 3. Test

- `TestEnqueueLocalizedRenderFailsClosedWithoutEnqueuer`: la sentinella è
  restituita, non `nil`.
- `TestSetSceneRenderExpectationsRequiresEnqueuer`: con una scena source-clip
  render-enabled e nessun enqueuer il preflight fallisce; dopo
  `SetLocalizedRenderEnqueuer` passa.

## 4. Check finali

```bash
go test ./internal/capabilities/scripts -run 'LocalizedRender|SceneRenderExpectations' -count=1
go test ./internal/capabilities/scripts -count=1
go build ./...
go vet ./...
go run ./cmd/archcheck --strict
```

Esito: vedi §5.

## 5. Evidenza

**2026-11-26, HEAD `cd43abdb7`.**

- `go test ./internal/capabilities/scripts -run 'LocalizedRender|SceneRenderExpectations' -count=1`
  → `ok github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts 0.632s`
- `go test ./internal/capabilities/scripts -count=1` → `ok ... 28.188s`
- `go build ./...` → exit 0; `go vet ./...` → exit 0
- `TestEnqueueLocalizedRenderSkipsNonRenderRuns` copre il complemento: un run
  audio-only/timeline-only con clip resta un no-op, non un errore.
- `go run ./cmd/archcheck --strict` → **non verde** per due violazioni fuori dal
  perimetro di questo ticket: `internal/capabilities/scripts` 18.999 righe >
  18.957 (il package era già 18.966 a HEAD, +9) e `internal/kernel/jsonwire`
  non dichiarato. La chiusura è tracciata in
  [P1-09](09-ARCHCHECK-HARD-GATE-RESIDUAL.md).

## 6. Rischi e rollback

- **Rischio:** un deployment che oggi omette volontariamente il renderer e
  dipende dal completamento senza video. Mitigazione: il gate scatta solo con
  `source.type=clips` + `render.enabled` + almeno un'unità attesa; i run
  text-only, stock-only e timeline-only restano invariati.
- **Rollback:** revert del singolo commit; nessuna migrazione o contratto wire
  cambia.
