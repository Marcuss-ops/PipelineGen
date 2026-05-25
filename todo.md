# Refactoring TODO

## ✅ Refactoring completato — tutto il progetto compila

Il progetto compila senza errori (`go build ./...`).

---

### God Object splittati

**`internal/media/images/service.go`** — da 1074 → ~130 righe
Split in 4 file:
- `service.go` — struct, costruttore, configurazione, Slugify
- `ingest.go` — download, ingesta immagini (SHA256 dedup, pipeline ingest, fallback diretto)
- `nvidia.go` — NVIDIA AI image generation (flux), animazioni, Drive sync
- `search.go` — ricerca immagini via Wikipedia/DuckDuckGo, Wikidata disambiguazione

**`internal/repository/clips/repository.go`** — da 839 → 230 righe
Split in 4 file:
- `repository.go` — Repository struct + CRUD base
- `folders.go` — ClipFolder operations (UpsertClipFolder)
- `search.go` — ListClips, ricerca
- `utility.go` — GetFolderChildren + utility

### Context propagation (catalog)
Aggiunto parametro `context.Context` a `SearchAll`, `SearchStock`, `SearchArtlist`, `SearchClips` — eliminati `context.Background()`.

### Fix applicati

| Fix | File |
|-----|------|
| Aggiunto campo `animationsDir` | `service.go` |
| Aggiunto campo `mu sync.Mutex` | `service.go` |
| Aggiunti import: `io`, `ingest`, `driveapi` | `nvidia.go` |
| Rimosso import inutilizzato: `math/rand` | `nvidia.go` |
| Aggiunto import: `driveapi` | `ingest.go` |
| Rimosso import inutilizzato: `time` | `ingest.go` |
| Aggiunti import: `context`, `io`, `math/rand` | `search.go` |
| Rimosso import inutilizzato: `sync` | `search.go` |
| Puliti import non usati (8 rimossi) | `service.go` |