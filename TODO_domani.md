# TODO Domani

## 1. Eliminare raw Drive API calls residue ✅

3 file hanno ancora `Files.List/Get/Create` diretti:

- [x] `internal/media/catalogsync/service.go:135,356`
- [x] `internal/storage/drivecleanup/reconcile.go:111,136`
- [x] `internal/media/voiceoversync/service.go:248`

Azione: usare `drive.Uploader` (come fatto oggi) invece del raw `driveClient.Files.Xxx()`.

## 2. Replace context.Background() in production ✅

File che usano `context.Background()` invece di propagare il request context:

- [x] `cmd/admin/*` (visto che sono CLI, va bene ma meglio propagare se possibile)
- [x] `internal/repository/catalog/*` (Query non-Context)
- [x] `internal/media/images/service.go`
- [x] `internal/media/clipindexer/service.go`
- [x] `internal/api/handlers/sources/stock_handler.go` (già parzialmente fixato)

Azione: aggiungere parametro `ctx context.Context` alle funzioni, propagare dal chiamante.

## 3. Split large files (God Objects) ✅

I seguenti file sono troppo grandi e vanno divisi in file più piccoli nella stessa directory:

- [x] `internal/media/images/service.go` (già diviso)
- [x] `internal/repository/clips/repository.go` (già diviso)
- [x] `internal/media/stockpipeline/service.go` (già diviso)

Azione: spostare gruppi di metodi (es: search, ingestion, helper) in file dedicati.

## 4. Pulizia untracked dirs e .gitignore ✅

- [x] Decidere se tenere o ignorare: `Crociata/`, `Epica Medievale/`, `Notte Gotica/`, `Rosone Cattedrale/` (Aggiunti a .gitignore)
- [x] Valutare `migrations/media/005_add_storage_fields.sql` e `migrations/sqlite/010_drop_unused_tables.sql` (Confermati utili)
- [x] Aggiornare `.gitignore` se necessario (Aggiornato)

## 5. Verifica finale: test e build ✅

- [x] `go build ./...` (OK)
- [x] `go test ./...` (OK)
