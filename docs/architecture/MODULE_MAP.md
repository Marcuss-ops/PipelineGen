# MODULE_MAP.md - PipelineGen Module Map Ufficiale

**Ultimo aggiornamento**: 2026-05-06
**Scopo**: Mappa ufficiale dei moduli attivi, sperimentali e deprecati. Single source of truth per agenti e sviluppatori.

---

## Legenda Stato

| Stato | Significato | Azione |
|-------|-------------|--------|
| **ATTIVO** | Modulo stabile, in produzione | Mantenere, testare |
| **SPERIMENTALE** | In sviluppo, feature flag OFF | Valutare, completare o rimuovere |
| **DEPRECATO** | Da eliminare, non estendere | Migrare e rimuovere |
| **DISABILITATO** | Feature flag OFF, in quarantena | Riscrivere o eliminare |

---

## Module Map Ufficiale

### Core System

| Modulo | Stato | Feature Flag | Descrizione | Database | Modulo Path |
|--------|-------|--------------|-------------|----------|--------------|
| **System** | ATTIVO | sempre ON | Health, diagnostics, doctor endpoint | nessuno | `internal/module/system/` |
| **Jobs** | ATTIVO | sempre ON | Coda job, worker, eventi | `jobs.db.sqlite` | `internal/service/jobs/` + `internal/core/jobs/` |

### Media Processing

| Modulo | Stato | Feature Flag | Descrizione | Database | Modulo Path |
|--------|-------|--------------|-------------|----------|--------------|
| **Artlist** | ATTIVO | `ARTLIST_ENABLED` | Pipeline Artlist (search, download, upload) | `artlist.db.sqlite`, `velox.db.sqlite` | `internal/service/artlist/` |
| **YouTube Clips** | ATTIVO | `YOUTUBE_ENABLED` | Estrazione clip YouTube | `clips.db.sqlite` | `internal/service/youtubeclip/` |
| **Media** | ATTIVO | sempre ON | Manifest export, asset management | `velox.db.sqlite` | `internal/module/media/` |
| **MediaAsset Processor** | ATTIVO | sempre ON | Processore canonico media (condiviso) | varie | `internal/service/mediaasset/` |
| **AssetRegistry** | SPERIMENTALE | n/a | Finalizer, registry pattern | varie | `internal/service/assetregistry/` |

### Content Generation

| Modulo | Stato | Feature Flag | Descrizione | Database | Modulo Path |
|--------|-------|--------------|-------------|----------|--------------|
| **ScriptDocs** | ATTIVO | `SCRIPT_DOCS_ENABLED` | Generazione script via Ollama | `velox.db.sqlite` | `internal/api/handlers/script/` |
| **Script History** | ATTIVO | `SCRIPT_CLIPS_ENABLED` | Storico script generati | `velox.db.sqlite` | `internal/module/script_history/` |
| **Voiceover** | SPERIMENTALE | `VOICEOVER_ENABLED` | Generazione voiceover, sync | `voiceover.db.sqlite` | `internal/service/voiceover/` |

### Asset Management

| Modulo | Stato | Feature Flag | Descrizione | Database | Modulo Path |
|--------|-------|--------------|-------------|----------|--------------|
| **Assets** | ATTIVO | sempre ON | Ricerca unificata asset | `velox.db.sqlite`, `artlist.db.sqlite` | `internal/module/assets/` |
| **Images** | ATTIVO | `IMAGES_ENABLED` | Ricerca e sync immagini | `images.db.sqlite` | `internal/service/images/` |
| **Drive Destination** | ATTIVO | sempre ON | Upload Drive, resolver dest. | nessuno (API) | `internal/upload/drive/` + `internal/service/assetdestination/` |
| **Asset Destination** | ATTIVO | sempre ON | Resolver unificato destinazioni | nessuno | `internal/service/assetdestination/` |

### Automation

| Modulo | Stato | Feature Flag | Descrizione | Database | Modulo Path |
|--------|-------|--------------|-------------|----------|--------------|
| **Workflow** | DISABILITATO | `WORKFLOW_ENABLED` | Workflow runner, goroutine | `velox.db.sqlite` | `internal/service/workflowrunner/` |
| **Scraper** | ATTIVO | sempre ON | Node.js scraper integration | nessuno | `internal/module/scraper/` |

### Utilities

| Modulo | Stato | Feature Flag | Descrizione | Database | Modulo Path |
|--------|-------|--------------|-------------|----------|--------------|
| **Utility** | ATTIVO | sempre ON | Utilità varie, slug generation | nessuno | `internal/module/utility/` |
| **ContentPackage** | ATTIVO | sempre ON | Job handler per content.package | `jobs.db.sqlite` | `internal/service/contentpackage/` |

---

## Database Boundaries (Desired Schema)

| Database | Tabelle | Moduli che lo usano |
|----------|---------|---------------------|
| `velox.db.sqlite` | scripts, monitored_sources, harvester_jobs, media_items, media_files, media_tags, video_metadata, script_stock_matches, video_stats_history, artlist_runs | System, ScriptDocs, Artlist, Media, Assets |
| `stock.db.sqlite` | clips (stock), clip_folders (stock) | Assets |
| `clips.db.sqlite` | clips (YouTube), clip_folders, segment_embeddings | YouTube Clips |
| `artlist.db.sqlite` | clips (Artlist), clip_folders, artlist_runs | Artlist |
| `images.db.sqlite` | (vuoto o image tables) | Images |
| `voiceover.db.sqlite` | (vuoto o voiceover tables) | Voiceover |
| `jobs.db.sqlite` | jobs, job_events | Jobs, ContentPackage |

---

## Da Eliminare (Quarantena)

| Sistema Vecchio | Motivo | Sostituito Da | Azione |
|-----------------|--------|---------------|--------|
| `internal/cron/*` | Job system legacy | `internal/service/jobs/` | Eliminare dopo migrazione harvester |
| `internal/repository/harvester/` | Usa cron legacy | Job system | Migrare o eliminare |
| `internal/service/workflowrunner/` | Pericoloso (context.Background) | N/A | Spostare in experimental o eliminare |
| `internal/service/assetpipeline/` | Thin wrapper inutile | Chiamate dirette | Eliminare |
| `pkg/idutil`, `pkg/jsonutil` | Wrap inutili | Inlineare | Inlineare funzioni |

---

## Regole di Sopravvivenza

### Moduli ATTIVI
1. Devono avere test
2. Devono usare job system per operazioni > 3 secondi
3. Non possono usare `context.Background()` negli handler
4. Devono propagare il context correttamente

### Moduli SPERIMENTALI
1. Devono stare in `internal/experimental/` (se non già)
2. Feature flag OFF di default
3. Non possono essere usati da moduli attivi senza review
4. Piano di uscita obbligatorio (completamento o eliminazione)

### Moduli DEPRECATI
1. Non ricevono nuove feature
2. Vengono eliminati gradualmente
3. Codice rimosso, non lasciato a marcire

---

## Contract Architetturale

### Unico Processore Media
- **Canonico**: `mediaasset.MediaProcessor`
- **Input**: `AssetInput`
- **Output**: `AssetResult`
- **Usato da**: Artlist, YouTube, (future) Voiceover

### Unico Job System
- **Canonico**: `internal/service/jobs/` + `internal/core/jobs/`
- **Storage**: `jobs.db.sqlite`
- **Worker**: 2 background workers
- **Retry**: Max 3 di default

### Unico Module Registry
- **Registry**: `internal/module/registry.go`
- **Interface**: `module.Module`
- **Lifecycle**: `Start()`, `Stop()`, `RegisterRoutes()`

---

## Note per Agenti

1. Prima di aggiungere un nuovo modulo, controllare se esiste già funzionalità simile
2. Usare sempre `module.Module` interface per nuovi moduli
3. Registrare nuovi moduli in `internal/bootstrap/registry.go`
4. Aggiornare questa mappa ad ogni cambiamento architetturale
5. Eseguire `scripts/ci-architectural-checks.sh` per validare
