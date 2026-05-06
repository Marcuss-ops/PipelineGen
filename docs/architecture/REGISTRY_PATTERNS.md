# REGISTRY_PATTERNS.md - Pattern Canonici per Registry e Resolver

**Ultimo aggiornamento**: 2026-05-06
**Scopo**: Definire i pattern canonici per evitare duplicazione di logica. Tutte le nuove feature devono usare questi pattern.

---

## Regola d'Oro

> **Niente duplicazione di logica. Ogni nuova feature deve entrare in un registry, resolver o sampler comune.**

---

## Pattern Canonici

### 1. Module Registry (Registrazione Moduli)

**Interface**: `module.Module` in `internal/module/module.go`

**Registry**: `module.Registry` - gestisce il ciclo di vita dei moduli

**Quando usare**: Per ogni nuova funzionalità che espone API o ha un ciclo di vita (Start/Stop)

**Esempio**:
```go
// 1. Implementa module.Module
type MyModule struct {
    cfg *config.Config
}

func (m *MyModule) Name() string { return "my-module" }
func (m *MyModule) Enabled(cfg *config.Config) bool { return cfg.Features.MyModuleEnabled }
func (m *MyModule) RegisterRoutes(rg *gin.RouterGroup) { ... }
func (m *MyModule) Start(ctx context.Context) error { ... }
func (m *MyModule) Stop(ctx context.Context) error { ... }

// 2. Registra nel bootstrap/registry.go
func WireRegistry(...) {
    registry.Register(myModule)
}
```

**Antipattern**: Creare handler diretti nel router senza passare da `module.Module`

---

### 2. Destination Resolver (Risoluzione Destinazioni)

**Interface Canonica**: `core/destination.Resolver` in `internal/core/destination/types.go`

```go
type Resolver interface {
    Resolve(ctx context.Context, req *ResolveRequest) (*ResolveResult, error)
}
```

**Implementazione**: `assetdestination.Resolver` -> adatta a `core/destination.Resolver` via `assetdestination.ToCoreResolver()`

**Quando usare**: Per risolvere destinazioni di asset (Drive, locale, S3, etc.)

**Esempio**:
```go
// Usa il resolver canonico
var resolver destination.Resolver

result, err := resolver.Resolve(ctx, &destination.ResolveRequest{
    Source:        "youtube",
    Group:         "boxe",
    SubfolderName: "Mike Tyson",
})
```

**Antipattern**: Usare direttamente `drivedestination.Service` invece del resolver canonico

**Migrazione**: Sostituire usi diretti di `drivedestination.Service` con `core/destination.Resolver`:
- `internal/service/youtubeclip/service.go` - usa `drivedestination.Service` direttamente
- `internal/service/artlist/drive_service.go` - espone `GetDriveDestination()`
- `internal/service/assetops/destination.go` - usa `drivedestination.Service` direttamente

---

### 3. Asset Registry (Registro Asset)

**Interface**: `assetregistry.Registry` in `internal/service/assetregistry/registry.go`

**Quando usare**: Per operazioni CRUD su asset (clips, immagini, voiceover)

**Esempio**:
```go
type ClipRegistry interface {
    SearchClips(ctx context.Context, term string) ([]*models.Clip, error)
    GetClip(ctx context.Context, id string) (*models.Clip, error)
    UpsertClip(ctx context.Context, clip *models.Clip) error
}
```

**Registrazione**:
```go
registry := assetregistry.NewRegistry(log)
registry.RegisterClipSource(assetSourceYouTube, youtubeRepo)
registry.RegisterClipSource(assetSourceArtlist, artlistRepo)
```

**Note**: `mediaregistry.Registry` è SPERIMENTALE e potrebbe essere rimosso. Usare `assetregistry.Registry` per nuovi codici.

---

### 4. Media Processor (Processore Media)

**Interface Canonica**: `mediaasset.MediaProcessor` in `internal/service/mediaasset/processor.go`

**Quando usare**: Per processare media (download, upload, transcode, etc.)

**Status**: **CANONICO** - Unico processore media approvato

**Usato da**: Artlist, YouTube, (future) Voiceover

**Antipattern**: Creare processor custom per ogni modulo

---

### 5. Job System (Sistema Job)

**Interface Canonica**: `internal/core/jobs/` + `internal/service/jobs/`

**Quando usare**: Per operazioni asincrone o lunghe (> 3 secondi)

**Status**: **SISTEMA CANONICO** - Tutto l'async deve passare da qui

**Database**: `jobs.db.sqlite`

**Esempio**:
```go
// Enqueue job
err := jobService.Enqueue(ctx, &models.Job{
    Type: "media.artlist",
    Payload: map[string]interface{}{"term": "boxe"},
})
```

**Antipattern**: Usare `context.Background()` negli handler o goroutine scollegate

---

## Matrice delle Decisioni

| Nuova Feature | Usa Pattern | Perché |
|---------------|-------------|---------|
| Nuovo modulo API | `module.Module` | Gestione ciclo vita, route registration |
| Nuova destinazione | `core/destination.Resolver` | Unificato, supporta Drive/locale/S3 |
| Nuovo asset type | `assetregistry.Registry` | CRUD comune, source-based |
| Nuovo processor | `mediaasset.MediaProcessor` | Processore canonico media |
| Nuova operazione async | `jobs.Service` | Job system canonico |

---

## Checklist per Nuove Feature

Prima di aggiungere una nuova feature, controlla:

- [ ] Esiste già un registry/resolver per questa funzione?
- [ ] Se sì, posso estendere quello esistente?
- [ ] Se no, devo creare un nuovo registry o usare uno dei pattern canonici?
- [ ] Il codice usa `context.Background()` negli handler? (NO!)
- [ ] Il codice usa il job system per operazioni > 3 secondi?
- [ ] Ho registrato il modulo in `bootstrap/registry.go`?
- [ ] Ho aggiornato `docs/architecture/MODULE_MAP.md`?

---

## Da Eliminare (Quarantena)

| Pattern/Sistema | Motivo | Sostituire Con |
|------------------|--------|----------------|
| `mediaregistry.Registry` | SPERIMENTALE, non usato | `assetregistry.Registry` o rimuovere |
| `internal/service/assetpipeline/` | Thin wrapper inutile | Chiamate dirette ai componenti |
| Usi diretti di `drivedestination.Service` | Bypassa resolver canonico | `core/destination.Resolver` |

---

## CI Checks

Eseguire `scripts/ci-architectural-checks.sh` per validare:
- Nessun `context.Background()` negli handler
- Uso dei pattern canonici
- Nessun thin wrapper inutile
- Nessun codice morto

---

## Note per Agenti

1. Prima di creare nuovo codice, controllare se esiste già un pattern per quella funzione
2. Usare sempre le interface canoniche in `internal/core/`
3. Registrare nuovi moduli tramite `module.Registry`
4. Usare il job system per operazioni asincrone
5. Non creare duplicati "perché è più veloce" - usa il pattern comune
