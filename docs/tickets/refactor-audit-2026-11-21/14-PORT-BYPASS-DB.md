# RA-14 — Port bypass `interface{ DB() *sql.DB }`

**Data:** 2026-11-21
**Priorità:** P1 (accoppiamento all'implementazione concreta)
**Stato:** **DONE (2026-11-21)** — porta `EngineHandle` tipizzata, reader media in `platform/postgres/media`, SQL rimosso da `ClipsRegistry`, percheck `percheck_capability_db_bypass` attivo
**Owner:** `app/wiring` + `capabilities/assets/artifacts` + `platform/postgres/media`
**Origine:** report esterno 2026-11-21 §1.2; verificato sul codice
**Verifica sul codice:** **CONFERMATO** nel meccanismo; **RIDIMENSIONATO** nella causa
(vedi §1: il bypass è deliberato e documentato, il difetto è che è untyped e vive in wiring)

## 1. Problema

Otto type assertion su una interfaccia anonima estraggono il `*sql.DB` dal committer:

| File | Righe |
| --- | --- |
| `internal/app/wiring/canonical_media_committer_derived_reads.go` | 44, 73, 103, 137, 171 |
| `internal/app/wiring/build_bundles_voiceover.go` | 122 |
| `internal/app/wiring/script_generation_bindings.go` | 40 |
| `internal/capabilities/assets/artifacts/clips_adapter.go` | 371 (`pgDBGetter`) |

Il commento in `canonical_media_committer_derived_reads.go:1-38` spiega perché esiste: le
letture derivate devono usare **lo stesso engine** del committer (PostgreSQL), altrimenti il
mirror SQLite e la scrittura canonica divergono. È un requisito reale, non un hack casuale.

Il difetto è che il requisito è soddisfatto con:
- un'**interfaccia anonima** (`interface{ DB() *sql.DB }`), quindi nessun contratto, nessuna
  documentazione nel tipo, nessun modo di testare senza un `*sql.DB`;
- il **composition root** che fa SQL e conosce lo schema (`pgmedia.NewMediaSearcher(db)`);
- la capability (`clips_adapter.go`) che fa lo stesso, con una seconda copia del meccanismo.

## 2. Soluzione

1. **Definire una porta tipizzata** in `internal/platform/postgres/media` (o in
   `internal/kernel/media` se il contratto è neutro):
   ```go
   // EngineHandle is the read side of the same engine that owns the write.
   // It exists so derived readers cannot drift onto the operational SQLite mirror.
   type EngineHandle interface {
       MediaDetailsReader() *AssetDetailsReader
       MediaDriveFileLister() *DriveFileLister
       // …un metodo per ciascuna lettura derivata oggi ottenuta via DB()
   }
   ```
   L'implementazione resta `*MediaCommitter` (o un wrapper), che già possiede il `*sql.DB`.
   `DB()` può restare **non esportato** o sparire dall'interfaccia pubblica.
2. **Spostare le letture derivate** in `platform/postgres/media` come metodi del committer o
   del reader: la logica di "quale engine" resta lì, dove è verificabile.
3. **Sostituire le 5 assertion in `canonical_media_committer_derived_reads.go`** con
   un'unica asserzione sulla porta:
   ```go
   handle, ok := committer.(pgmedia.EngineHandle)
   if !ok { return nil }   // media plane chiuso: fail-closed, come oggi
   ```
   Il `nil` resta legittimo (media plane chiuso), ma il tipo è esplicito e testabile con un
   fake.
4. **`clips_adapter.go`**: rimuovere `pgDBGetter` e far dipendere `ClipsRegistry` da una porta
   di lettura iniettata (`MediaReader`), costruita in wiring. Se `ClipsRegistry` ha bisogno del
   DB per un solo caso, il caso è una lettura derivata e appartiene alla stessa porta.
5. **Percheck**: vietare `interface{ DB() *sql.DB }` e `DB() *sql.DB` in
   `internal/app/**` e `internal/capabilities/**` (allowlist vuota dopo la migrazione). Stesso
   stile di `percheck_platform_port_boundary`.
6. **Test**: fake `EngineHandle` che ritorna reader deterministici; test che il committer
   concreto soddisfa la porta (`var _ EngineHandle = (*MediaCommitter)(nil)`).

## 3. Check finali

```bash
cd refactored
# 1. nessun type assertion anonima residua
grep -rn 'interface{ DB() \*sql.DB }' --include=*.go internal/ | grep -v _test
# atteso: nessun output

# 2. la porta è soddisfatta dall'implementazione concreta
grep -rn "var _ .*EngineHandle" internal/platform/postgres/media/
# atteso: una assertion di compilazione presente

# 3. il media plane chiuso resta fail-closed
go test ./internal/app/wiring/ -run 'DerivedRead|MediaDetails|DriveFileList|Committer' -v
# atteso: PASS; con committer che non implementa la porta => reader nil + errore tipizzato all'uso

# 4. clips registry non tocca più il DB
grep -n "sql\." internal/capabilities/assets/artifacts/clips_adapter.go
# atteso: nessun uso di *sql.DB nel file

# 5. build/vet/gate
go build ./... && go vet ./...
go run ./cmd/archcheck --strict   # exit 0
go test ./internal/platform/postgres/media/... ./internal/capabilities/assets/artifacts/...
```

## 4. Rischi e rollback

- **Rischio**: la porta diventa un god interface se ogni lettura aggiunge un metodo.
  Mitigazione: metodi per **lettura derivata**, non per query; se diventano >5, dividere in
  `EngineHandle` + `MediaReader`.
- **Rischio**: il committer è costruito in più punti; la porta deve essere soddisfatta da
  tutti. Mitigazione: assertion di compilazione nei costruttori.
- **Rollback**: revert; il bypass torna funzionante.

## 5. Chiusura (2026-11-21)

**DONE.** La porta tipizzata esiste e i chiamanti non possono più estrarre il `*sql.DB`.

- `pgmedia.EngineHandle` espone le nove letture derivate; `*PostgresMediaCommitter` la
  implementa con assertion di compilazione. I tre file nuovi (`engine_handle.go`,
  `clips_media_reader.go`, `folder_path_writer.go`) sono stati consolidati in
  `internal/platform/postgres/media/media_engine_ports.go` per rispettare il cap di 65
  file del package (erano 67).
- `ClipsRegistry` riceve `artifacts.MediaReader` con `WithMediaReader`; il SQL e
  `pgDB()` sono spariti dalla capability.
- `percheck_capability_db_bypass` vieta `DB() *sql.DB` in `internal/app` e
  `internal/capabilities` (i platform adapter sono l'implementazione della porta).

Check eseguiti: `go build ./...` exit 0; `gofmt -l` vuoto; test PASS su
`cmd/archcheck/scan/boundaries`, `app/wiring`, `capabilities/assets/artifacts`,
`platform/postgres/media`; `go run ./cmd/archcheck --strict` exit 0, 0 violazioni,
67 warning, `pkg_size` di `platform/postgres/media` rientrato a 65/65.
