# internal/core/ - Contratti Canonici

Questa directory contiene i contratti canonici (interfacce e tipi) per il sistema PipelineGen.
Tutti i moduli devono adattarsi a questi contratti, non creare i propri mini-mondi.

## Contratti Canonici (5)

### 1. Asset (Media)
**Package**: `internal/core/media/`
**File**: `model.go`, `models.go`

Il modello canonico per asset video/audio/image.
- `MediaAsset` - asset principale
- `MediaFile` - file associati
- `Item`, `File` - modelli alternativi/search

### 2. Job
**Package**: `internal/core/jobs/`
**File**: `types.go`

Il modello canonico per job, status, retry, progress, event.
- `JobType` - tipo di job (enum)
- `JobStatus` - stato del job (enum)
- `Job` - struttura principale

### 3. Destination
**Package**: `internal/core/destination/`
**File**: `types.go`

L'unico resolver per Drive/local/output.
- `Resolver` - interfaccia canonica
- `ResolveRequest` / `ResolveResult` - tipi di richiesta/risposta

### 4. Processor
**Package**: `internal/core/processor/`
**File**: `types.go`

L'unico processore per download, process, hash, upload.
- `Processor` - interfaccia canonica
- `ProcessInput` / `ProcessResult` - tipi di input/output

*Nota*: L'implementazione attuale è in `internal/service/mediaasset/Processor`.

### 5. Module
**Package**: `internal/module/`
**File**: `module.go`

L'unico registry per abilitare/disabilitare/wiring moduli.
- `Module` - interfaccia che tutti i moduli devono implementare
- `Registry` - registry centrale per la gestione dei moduli

## Regola d'Oro

> "Tutto il resto deve adattarsi a questi 5, non creare il proprio mini-mondo."

Se un modulo ha bisogno di un nuovo contratto, deve essere discusso e aggiunto qui, non creato localmente nel package del modulo.

## Mapping Attuale

| Contratto | Package Core | Implementazione Attuale |
|-----------|--------------|----------------------|
| Asset | `internal/core/media/` | `internal/service/mediaasset/` (tipi), `internal/core/media/` (modelli) |
| Job | `internal/core/jobs/` | `internal/service/jobs/` |
| Destination | `internal/core/destination/` | `internal/service/assetdestination/`, `internal/service/drivedestination/` |
| Processor | `internal/core/processor/` | `internal/service/mediaasset.Processor` |
| Module | `internal/module/` | `internal/module/`, `internal/bootstrap/` |

## Da Fare

1. Consolidare `internal/core/media/` - unificare `model.go` e `models.go`
2. Migrare `assetdestination.Resolver` a `internal/core/destination.Resolver`
3. Migrare `mediaasset.Processor` a `internal/core/processor.Processor`
4. Rimuovere duplicati e adattare tutti i moduli ai contratti canonici
