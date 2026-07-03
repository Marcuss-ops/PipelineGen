# Porting Analysis — DataServer `enqueue_clips.go` → PipelineGen

> **Data richiesto**: 2026-07-03
> **Commit DataServer analizzato**: `9095373` (`fix(enqueue): make voiceover timeline durations canonical`) su `2ee71ce`
> **Commit PipelineGen corrente**: `c700123f` (`origin/main`)
> **Fonti**: AGENTS.md, `docs/plans/2026-07-03-dataserver-action-plan.md`

---

## Pattern 1: Voiceover duration correction

### Cosa fa DataServer

`enqueue_clips.go::BuildClipPayloadForMaster` costruisce un payload narrato (voiceover bed + final clip) per il worker di rendering. Due helper estratti:

- **`resolveSceneVoiceoverDuration(scene, probe)`** — gerarchia stretta:
  1. `voiceover_duration_seconds` esplicito nel payload
  2. Probe dinamico (`audioDurationProbe` → `DetectAudioDurationSecs`)
  3. **Errore** se nessuno dei due produce una durata (contratto stretto)
- **`resolveSceneFinalClipDuration(scene)`** — `final_clip_duration_seconds` o legacy `clip_duration_seconds`; default 4.0s
- `duration_seconds` generico **NON** usato per il timing voiceover
- Totale = `voiceoverDuration + finalClipDuration`
- Offset incrementa di entrambe le durate, non solo una
- Segmento voiceover aggiunto a `items`/`audioTracks` solo se `voiceoverURL` presente

### Cosa ha PipelineGen

PipelineGen ha un modulo voiceover (`internal/application/voiceover/`) che genera **file audio TTS standalone**, non una timeline multi-traccia con offset:

- `BatchItem` (types.go) ha `Status`, `Error`, `Errors` ma **nessun campo `DurationSeconds`**
- `processLanguage()` (process.go) orchestra 3 stage: synthesize → destination → finalize
- **Nessun concetto di "voiceover bed + final clip"** — PipelineGen non compone timeline

### Verdetto: ❌ NON applicabile

Gli ambiti architetturali sono diversi. DataServer compone una timeline narrata; PipelineGen genera file TTS standalone. Il pattern di "duration correction" non ha un equivalente diretto in PipelineGen.

**Opportunità correlata (P2, non urgente)**: PipelineGen potrebbe tracciare la durata audio effettiva dopo la sintesi TTS (`synthesizeStage` → `BatchItem.DurationSeconds`). Questo migliorerebbe il consumo downstream ma è un enhancement, non un bug fix.

---

## Pattern 2: Delivery plan validato prima del rendering

### Cosa fa DataServer

Il piano delivery viene letto dal payload e scritto **nella stessa transazione di Job, Task e TaskSpec**. L'enqueue viene annullato completamente quando:

1. Manca il piano in produzione
2. La destinazione non esiste
3. La destinazione globale è disabilitata
4. Ci sono destinazioni duplicate
5. Il piano è ambiguo o invalido

Payload deve contenere uno di:
- `delivery_destination_id` (singola destinazione)
- `delivery_destination_ids` (lista)
- `delivery_plan` (array strutturato con `destination_id`, `priority`, `retry_budget`)

Flag `VELOX_DELIVERY_GLOBAL_FALLBACK` può rilassare il requisito in ambienti non-production.

### Cosa ha PipelineGen

PipelineGen **NON** valida le delivery destination prima dell'enqueue:

| Componente | Validazione attuale | Gap |
|---|---|---|
| `GenerationEnvelopeV2.Validate()` | `Version==2`, `Items` non vuoto, `Source.Type` popolato, campi specifici per tipo | **Nessuna validazione delivery** |
| `enqueueEnvelope()` (handler) | Chiama `env.Validate()`, check `jobsSvc!=nil`, legge `Idempotency-Key` | **Nessuna validazione delivery** |
| `EnqueueGenerationJob()` (jobs) | Marshala envelope, popola `MaxRetries` da registry, chiama `jobsSvc.Enqueue()` | **Nessuna validazione delivery** |
| `DestinationRequest.Validate()` (voiceover) | Solo `SubfolderName` per path traversal | **Non controlla esistenza/abilitazione destinazione** |
| `DeliveryDestination` (types_media.go) | Tipo con campo `Enabled` | **Mai usato in fase di enqueue** |

Un job può completare correttamente il rendering e scoprire solo durante `FinalizeVerified` che manca il piano delivery o la destinazione è disabilitata.

### Verdetto: ✅ APPLICABILE — Piano di porting raccomandato

PipelineGen ha già l'infrastruttura necessaria (`DeliveryDestination` con `Enabled`, `DestinationRequest` con `Kind`, `OutputSpec` con `DriveFolderID`/`VoiceoverGroup`/`VoiceoverFolderID`). Manca solo il gate di validazione pre-enqueue.

---

## Mini-piano di porting — Delivery validation pre-enqueue

### Fase 1: Aggiungere `DeliverySpec` opzionale a `GenerationEnvelopeV2` (dominio)

**File**: `internal/domain/script/generation_envelope.go`

Aggiungere un campo `Delivery *DeliverySpec` a `GenerationEnvelopeV2`:

```go
type DeliverySpec struct {
    DestinationID  string   `json:"delivery_destination_id,omitempty"`
    DestinationIDs []string `json:"delivery_destination_ids,omitempty"`
    Plan           []DeliveryPlanEntry `json:"delivery_plan,omitempty"`
}

type DeliveryPlanEntry struct {
    DestinationID string `json:"destination_id"`
    Priority      int    `json:"priority,omitempty"`
    RetryBudget   int    `json:"retry_budget,omitempty"`
}
```

**Test**: `TestDeliverySpec_RoundTrip` — marshal/unmarshal preserva tutti i campi.

### Fase 2: Aggiungere porta `DeliveryValidator` nell'application layer

**File**: `internal/application/scripts/ports/ports.go`

```go
type DeliveryValidator interface {
    ValidateDestination(ctx context.Context, destID string) error
}
```

**Implementazione concreta** in `internal/application/scripts/adapters/`:

```go
type deliveryValidator struct {
    destRepo DeliveryDestinationReader
}

func (v *deliveryValidator) ValidateDestination(ctx context.Context, destID string) error {
    dest, err := v.destRepo.GetByID(ctx, destID)
    if err != nil {
        return fmt.Errorf("%w: %s", ErrDestinationNotFound, destID)
    }
    if !dest.Enabled {
        return fmt.Errorf("%w: %s", ErrDestinationDisabled, destID)
    }
    return nil
}
```

### Fase 3: Validazione in `enqueueEnvelope()`

**File**: `internal/api/script/handler_legacy_adapters.go`

Aggiungere prima di `jobs.EnqueueGenerationJob()`:

```go
if env.Delivery != nil {
    if err := h.deliveryValidator.ValidateDeliverySpec(c.Request.Context(), env.Delivery); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
        return
    }
}
```

### Fase 4: Feature flag `VELOX_DELIVERY_GLOBAL_FALLBACK`

**File**: `internal/platform/config/types.go`

```go
DeliveryGlobalFallback bool `yaml:"delivery_global_fallback" env:"VELOX_DELIVERY_GLOBAL_FALLBACK" default:"false"`
```

Quando `true`, la validazione è rilassata (log warn, non 400). Default `false` = produzione strict.

### Fase 5: Test end-to-end

- `TestEnqueue_FailsWhenDestinationMissing` — 400 se destinazione inesistente
- `TestEnqueue_FailsWhenDestinationDisabled` — 400 se `Enabled=false`
- `TestEnqueue_SucceedsWithGlobalFallback` — 200 con `VELOX_DELIVERY_GLOBAL_FALLBACK=true` anche se destinazione mancante
- `TestEnqueue_FailsWhenPlanAmbiguous` — 400 se sia `destination_id` che `destination_ids` popolati
- `TestEnqueue_FailsWhenDuplicateDestinations` — 400 se `destination_ids` contiene duplicati

---

## Riepilogo

| Pattern | Applicabile? | Azione |
|---------|-------------|--------|
| Voiceover duration correction | ❌ No | Domini architetturali diversi (timeline narrata vs TTS standalone). P2 enhancement opzionale: tracciare `DurationSeconds` post-TTS. |
| Delivery plan pre-rendering | ✅ Sì | Mini-piano 5 fasi sopra. Aggiungere `DeliverySpec` al dominio, validatore pre-enqueue, feature flag, test. |

**Stima effort**: ~4-6 ore (dominio + adapter + handler + config + test)
**Rischio**: Basso — feature flag off-by-default, il comportamento corrente è preservato finché il flag non viene attivato.
