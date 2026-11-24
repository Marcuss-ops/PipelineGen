# Inventario consumer `generation` — 2026-11-24

**Contesto:** criterio di riapertura BON-01 «campi piatti generation di
`GenerateRequest`/`GenerateResult`». La fonte è il censimento sul checkout:
`rg -l 'GenerateRequest|GenerateResult' internal --glob '!internal/capabilities/scripts/**' --glob '!*_test.go'`
→ **18 file**.

**Vista generation:** `contract.GenerateRequest.Generation()` e
`contract.GenerateResult.Generation()` restituiscono rispettivamente
`contracts/generation.Request` e `contracts/generation.Result`. La vista è di
sola lettura e copre solo la fase generation (editorial, traduzione, motion,
budget, script spec, provenzienza build). Source, audio, submission, publication
e final-job non sono nella vista: aggiungerli la trasformerebbe in un secondo
god model, che è esattamente ciò che `contracts/generation` esiste per evitare.

## Esito del censimento

| File | Classe | Motivo / azione |
|---|---|---|
| `internal/app/modules/script/scene_text_helpers.go` | **migrato** | `buildEditorialPromptFromGenReq` e `genRequestToResolutionContext` leggono i campi generation attraverso `req.Generation()`. I campi source restano su `req.Source`. |
| `internal/platform/sqlite/scripts/run_repository.go` | **già sulla porta** | Usa `scripts/ports/run.GenerateRequest/Result`, il surface sanzionato che ri-esporta il contract. Non importa il package business. |
| `internal/app/modules/media/preflight.go` | confine di fase | Il preflight legge `MediaPlan`, `ExtractEntities`, `GenerateSceneImages`: fase source/media, non generation. |
| `internal/app/modules/script/fake_final_job.go` | confine di fase | Test double del confine `FinalJobSubmitter`; la firma deve restare quella del contratto completo. |
| `internal/app/modules/script/remote_final_job.go` | confine di fase | Costruisce il payload del final job, che consuma anche render, audio, overlay e receipt: la sola vista generation perderebbe dati. |
| `internal/app/modules/script/scene_text_generator.go` | adapter generation | Genera testo leggendo insieme campi generation e source (`Source`, `MediaPlan`); è l'implementazione della fase, non un consumer esterno. |
| `internal/app/modules/script/scene_text_sources.go` | migrato (vista generation) | Il piano generation usa `req.Generation()`; `Source`, `MediaPlan` e i flag di persistenza restano sul DTO completo. |
| `internal/app/modules/voiceover/adapters_voiceover_scriptgen.go` | confine di fase | Adapter audio/voiceover: legge configurazione audio e output, non solo generation. |
| `internal/app/wiring/build_bundles_ai.go` | pass-through | Composition root: passa il DTO ai adapter; non legge campi generation. |
| `internal/app/wiring/script_generation_bindings.go` | pass-through | Wiring del plan resolver; la firma del callback è il contratto completo. |
| `internal/app/wiring/wire_script.go` | pass-through | Wiring del composition root; nessuna lettura di campo generation. |
| `internal/capabilities/script/handler_generate_handler.go` | pass-through | Ingresso del builder: costruisce il DTO completo, non ne legge la fase generation. |
| `internal/capabilities/images/generation/types.go` | falso positivo | Il tipo è `GenerateImageRequest` (alias `GenerateRequest`), un DTO immagini diverso. |
| `internal/capabilities/jobs/delivery_visibility.go` | falso positivo | Solo un commento che nomina `GenerateResult`. |
| `internal/platform/observability/metrics_scripts.go` | falso positivo | Solo un commento sul checkpoint del risultato. |
| `internal/platform/ollama/client/client_generate.go` | falso positivo | `GenerateResult` è il tipo legacy del client Ollama. |
| `internal/platform/ollama/types/client_types.go` | falso positivo | `GenerateRequest` è il tipo legacy del client Ollama. |
| `internal/platform/sqlite/jobs/result_store.go` | falso positivo | Solo un commento sul payload `script.generate`. |

## Esito sintetico

- **Lettori generation non classificati: 0.**
- **2 adapter di fase** (`scene_text_generator`, `scene_text_sources`):
  implementano la generation; non sono consumer esterni.
- **4 confini di fase** (`media/preflight`, `fake_final_job`,
  `remote_final_job`, `voiceover/adapters_voiceover_scriptgen`): attraversano
  source/audio/final job e non possono usare la sola vista generation.
- **4 pass-through** (`build_bundles_ai`, `script_generation_bindings`,
  `wire_script`, `handler_generate_handler`): costruiscono o passano il DTO,
  non leggono la fase generation.
- **1 migrato** (`scene_text_helpers`), **1 già sulla porta**
  (`sqlite/scripts/run_repository`), **6 falsi positivi** (tipi legacy,
  commenti).

## Regola per i nuovi consumer

Un consumer che legge **solo** campi generation usa
`req.Generation()`/`result.Generation()`, mai i campi piatti del DTO completo.
Un consumer che attraversa più fasi (source, audio, submission, final job)
resta sul DTO completo e va aggiunto a questo elenco con il motivo: non è un
lettore generation, è un confine di fase.

## Verifica

- `go test ./internal/capabilities/scripts/contract/... -count=1` → PASS
  (`TestGenerationViewProjectsRequest`, `TestGenerationViewProjectsResult`,
  `TestGenerationViewJSONRoundTrip`, `TestGenerateResultJSONExcludesRuntimeOnlyState`).
- `go test ./internal/app/modules/script/... -count=1` → PASS sulla superficie
  che usa `Generation()`, incluso il piano sorgente migrato.
- `go build ./...` → RC 0.
