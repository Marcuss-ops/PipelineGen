# MODULE_OWNERSHIP.md - Mappa dei Sistemi Viv

Questo documento definisce la proprietà e lo stato di ogni modulo del sistema.
Serve agli agenti per capire cosa è attivo, cosa è sperimentale e cosa deve morire.

## Regola Generale

- **Un solo sistema per funzione** - Se esistono duplicati, uno deve morire
- **Sperimentale = feature flag OFF + internal/experimental/** - Niente codice sperimentale in `internal/service/`
- **I vecchi sistemi sono in quarantena** - Non estendere, non migrare. Solo eliminare.

---

## Mappa dei Sistemi Viv

| Area              | Sistema Vecchio                | Sistema Nuovo            | Stato Nuovo     | Azione            |
| ----------------- | ------------------------------ | ------------------------ | --------------- | ----------------- |
| **Jobs**          | `internal/cron/*`              | `internal/service/jobs/` + `internal/core/jobs/` | **ATTIVO**     | Eliminare cron/*  |
|                   | `internal/repository/harvester/` |                          | **DEPRECATO**   | Migrare a jobs    |
| **Media**         | `internal/repository/clips/`   | `internal/service/mediaasset/` | **ATTIVO** | Mediaregistry in valutazione |
|                   |                                | `internal/service/assetregistry/` | **SPERIMENTALE** | Valutare se utile |
| **Drive Destination** | Config diretto in `pkg/config/types.go` | `internal/upload/drive/` | **ATTIVO** | Eliminare riferimenti diretti |
|                   |                                | `internal/service/assetdestination/` | **ATTIVO** | Unificare se duplicato |
| **Artlist**       | Pipeline diretta (old)          | `internal/service/artlist/` | **ATTIVO** | Usa job system |
| **YouTube Clips** | Service custom (old)            | `internal/service/mediaasset/` processor | **ATTIVO** | Usa MediaProcessor |
| **Voiceover**     | Batch diretto                   | `internal/service/assetregistry/` + voiceover adapter | **SPERIMENTALE** | Completare o rimuovere |
| **Workflow**      | N/A                             | `internal/service/workflowrunner/` | **DISABILITATO** | Feature flag OFF, in quarantena |
| **Script**        | Python scripts                  | `internal/api/handlers/script/` | **ATTIVO** | Documentare meglio |

---

## Proprietà Moduli (Chi mantiene cosa)

### Artlist Module
- **Owner**: `internal/module/artlist/`
- **Service principale**: `internal/service/artlist/service.go`
- **Processor**: `internal/service/mediaasset/processor.go` (condiviso)
- **Job Handler**: `internal/service/artlist/job_handler.go`
- **API**: `internal/api/handlers/artlist/`
- **Stato**: ATTIVO (feature flag OFF di default)

### YouTube Module
- **Owner**: `internal/module/youtubeclip/`
- **Service**: `internal/service/youtubeclip/`
- **Processor**: `internal/service/mediaasset/processor.go` (condiviso)
- **API**: `internal/api/handlers/youtube-clips/`
- **Stato**: ATTIVO (feature flag OFF di default)

### Jobs Module
- **Owner**: `internal/service/jobs/` + `internal/core/jobs/`
- **Repository**: `internal/repository/jobs/`
- **Database**: `jobs.db.sqlite`
- **Worker**: `internal/service/jobs/worker.go`
- **Stato**: **SISTEMA CANONICO** - Tutto l'async deve passare da qui

### Workflow Module
- **Owner**: `internal/service/workflowrunner/` (da spostare in experimental)
- **API**: `internal/api/handlers/workflow/` (dietro feature flag OFF)
- **Problemi**: context.Background(), goroutine without job, in-memory maps without mutex
- **Stato**: **DISABILITATO** - Da riscrivere o eliminare

### Media Asset (Processor Canonico)
- **Owner**: `internal/service/mediaasset/`
- **Interfaccia**: `MediaProcessor` in `processor.go`
- **Usato da**: Artlist, YouTube, (future) Voiceover
- **Stato**: **CANONICO** - Unico processore media approvato

### Drive Destination
- **Owner**: `internal/upload/drive/`
- **Resolver unificato**: `internal/service/assetdestination/`
- **Config**: `pkg/config/types.go` (DriveConfig)
- **Stato**: ATTIVO

### Media Registry (Finalizer)
- **Owner**: `internal/service/assetregistry/`
- **Interfaccia**: `Registry`, `Finalizer`
- **Adapter**: `clips_adapter.go`, `voiceover_adapter.go`
- **Stato**: **SPERIMENTALE** - Valutare se necessario o se `mediaasset.Processor` basta

---

## Regole di Sopravvivenza

### Per i moduli ATTIVI:
1. Devono avere test
2. Devono usare il job system per operazioni > 3 secondi
3. Non possono usare `context.Background()` negli handler
4. Devono propagare il context correttamente

### Per i moduli SPERIMENTALI:
1. Deveno stare in `internal/experimental/`
2. Devono avere feature flag OFF di default
3. Non possono essere usati da moduli attivi senza review
4. Devono avere un piano di uscita (completamento o eliminazione)

### Per i moduli DEPRECATI:
1. Non ricevono nuove feature
2. Vengono eliminati gradualmente
3. Il codice viene rimosso, non lasciato a marcire

---

## Sistemi da Eliminare (Quarantena)

### 1. `internal/cron/*`
- **Perché**: Sistema job legacy, rimpiazzato da `internal/service/jobs/`
- **Azione**: Eliminare dopo migrazione harvester
- **File**: `db_backup.go`, `catalog_sync.go`, `db_maintenance.go`, `harvester_cron.go`

### 2. `internal/repository/harvester/`
- **Perché**: Usa sistema cron legacy
- **Azione**: Migrare logica in job system o eliminare

### 3. `internal/service/workflowrunner/`
- **Perché**: Pericoloso (context.Background(), goroutine scollegate, in-memory maps)
- **Azione**: Spostare in `internal/experimental/` o eliminare
- **Stato**: Disabilitato dietro feature flag

### 4. `assetregistry.VoiceoverRegistry` (codice morto)
- **Perché**: Mai usato, duplicato da `voiceover.NewVoiceoverRegistryAdapter()`
- **Azione**: Eliminare `internal/service/assetregistry/voiceover_adapter.go` (VoiceoverRegistry)

### 5. `workflowrunner.Registry.Get()` e `List()`
- **Perché**: Funzioni mai chiamate
- **Azione**: Eliminare se non serve, o usarle correttamente

---

## Package da Eliminare (Thin Wrappers)

| Package | Motivo | Azione |
|---------|--------|--------|
| `pkg/idutil` | 13 linee, wrap inutile | Inlineare `StableSlugID()` |
| `pkg/jsonutil` | 15 linee, wrap inutile | Inlineare `ReadJSON()` |
| `internal/service/assetpipeline` | Solo pass-through | Eliminare, chiamare direttamente i componenti |
| legacy assetstore interfaces | Rimossa l'implementazione | Usare helper e contratti canonici già spostati |

---

## Contratti Canonici (Da Stabilire)

### 1. Asset
- **Modello**: Da definire in `internal/core/asset/`
- **Processore**: `mediaasset.MediaProcessor` (canonico)
- **Registry**: `assetregistry.Registry` (sperimentale, valutare)

### 2. Job
- **Modello**: `internal/core/jobs/types.go` (canonico)
- **Status**: `JobStatus` enum
- **Retry**: Max 3 retries di default
- **Storage**: `jobs.db.sqlite` (persistente)

### 3. Destination
- **Resolver**: `assetdestination.Resolver` (da valutare se unico)
- **Drive**: `drivedestination.Service` (attivo)

### 4. Processor
- **Unico**: `mediaasset.MediaProcessor` (canonico)
- **Input**: `AssetInput`
- **Output**: `AssetResult`

### 5. Module
- **Registry**: `internal/module/registry.go`
- **Interface**: `module.Module`
- **Feature Flags**: `pkg/config/types.go` (`FeaturesConfig`)

---

## Regola Anti-Fake Endpoint

Ogni endpoint deve avere:
1. Test handler
2. Errore coerente
3. Feature flag se sperimentale
4. Documentazione ATTIVA (non README obsoleti)
5. Limite/timeout
6. Comportamento reale

**Da eliminare**:
- `/test` in `internal/api/handlers/media/common_handler.go:161`
- Endpoint che restituiscono "not implemented"
- Handler con solo placeholder

---

## TODO per Agenti

1. **Migrare harvester** da `internal/cron/harvester_cron.go` al job system
2. **Eliminare** `internal/cron/*` dopo migrazione
3. **Spostare** `workflowrunner` in `internal/experimental/`
4. **Rimuovere** codice morto identificato (VoiceoverRegistry, Get/List mai usate)
5. **Consolidare** drive destination (assetdestination vs drivedestination)
6. **Stabilire** il contratto Asset canonico in `internal/core/asset/`
7. **Aggiungere** test architetturali per:
   - `context.Background()` negli handler
   - Endpoint senza test
   - Package thin wrapper

---

**Ultimo aggiornamento**: 2026-05-05
**Manutenzione**: Aggiornare ad ogni cambiamento architetturale
