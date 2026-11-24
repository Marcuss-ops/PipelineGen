# CLEANUP-03 — Cinque helper duplicati riga per riga

**Data:** 2026-11-24
**Priorità:** P1 (SSOT, rischio basso)
**Stato:** DONE (commit `c6126a4ef`)
**Owner:** `pkg/*`, `platform/sqlite`
**Origine:** radiografia 2026-11-24, §2C
**Base:** `refactored` @ `2a3ea0b2c` (working tree pulito)

## 1. Problema

Cinque helper sono copiati in due package diversi. Le copie sono oggi
byte-identiche (verificato leggendo entrambi i corpi), quindi il rischio non è
un bug già presente: è la deriva futura. Una correzione applicata a una copia
non raggiunge l'altra, e il prossimo bug di parsing/whitespace si presenterà su
un solo percorso.

| # | Copia A | Copia B | Destinazione |
| --- | --- | --- | --- |
| 1 | `app/wiring/media_content.go:468` `parseAssetRange` | `capabilities/assets/clips/clip_action.go:210` `parseRangeHeader` | `pkg/httpjson.ParseByteRange` |
| 2 | `cmd/velox/download_final.go:211` `findMapByKey` | `capabilities/jobs/m2m_artifact.go:216` `findMapByKey` | `pkg/jsonutil.FindMapByKey` |
| 3 | `platform/sqlite/set.go:285` `resolveMigrationsDir` | `platform/sqlite/sqltest/testdb.go:158` `testMigrationsDir` (+ copia in `platform/sqlite/testdb_test.go:78`) | `sqlite.MigrationsDir()` esportata |
| 4 | `platform/instaeditpublishing/video.go:85` `cleanTags` | `capabilities/youtube/jobs/upload_job_handler.go:246` `cleanUploadTags` | `pkg/textutil.CleanTags` |
| 5 | `capabilities/mediamemory/normalizer.go:161` `canonicalizeWhitespace` | `capabilities/brain/normalizer/normalizer.go:109` `collapseWhitespace` | `pkg/textutil.CollapseWhitespace` |

## 2. Soluzione

Estrarre la logica nei leaf package già esistenti, senza cambiare il
comportamento:

1. **Range HTTP** — `ParseByteRange(raw string, size int64) (start, end int64, ok bool)`
   in `pkg/httpjson/range.go`. Resta single-range: `bytes=start-`,
   `bytes=start-end`, `bytes=-suffix`; multi-range, unità diverse e size ≤ 0
   falliscono. Aggiornare i due call-site e il test
   `clips/clip_action_range_test.go` (che oggi invoca `parseRangeHeader`).
2. **Ricerca ricorsiva in JSON** — `FindMapByKey(v any, key string) (map[string]any, bool)`
   in `pkg/jsonutil/find.go`. Mantiene il first-match depth-first su map e
   slice.
3. **Migrations dir** — esportare `sqlite.MigrationsDir() string` (l'attuale
   `resolveMigrationsDir`, che parte da `set.go` e risale fino a
   `migrations/sqlite`). `sqltest.NewMigratedTestDB` e i test di `sqlite`
   usano quella; si eliminano le due copie `testMigrationsDir`. Non toccare
   `resolveJobsMigrationsDir`: risolve `migrations/sqlite_jobs`, è un'altra
   directory.
4. **Tag** — `pkg/textutil.CleanTags([]string) []string`: trim, scarta vuoti,
   deduplica case-insensitive conservando la prima grafia.
5. **Whitespace** — `pkg/textutil.CollapseWhitespace(string) string`: runs di
   `unicode.IsSpace` diventano un singolo spazio ASCII; nessun trim (il
   comportamento attuale non trimma).

I package chiamanti mantengono le proprie API pubbliche; spariscono solo le
funzioni unexported duplicate. `pkg/httpjson`, `pkg/jsonutil` e `pkg/textutil`
sono leaf package già usati dal repo, quindi non si introducono dipendenze
architetturali nuove.

## 3. Check finali

```bash
cd refactored
# Una sola definizione per comportamento.
rg -n 'func (parseAssetRange|parseRangeHeader|findMapByKey|cleanTags|cleanUploadTags|canonicalizeWhitespace|collapseWhitespace|resolveMigrationsDir|testMigrationsDir)\b' --glob '*.go'
# atteso: nessun risultato (resta solo sqlite.MigrationsDir)
rg -n 'func (ParseByteRange|FindMapByKey|CleanTags|CollapseWhitespace|MigrationsDir)\b' --glob '*.go'
go build ./...
go test ./pkg/httpjson/... ./pkg/jsonutil/... ./pkg/textutil/... \
  ./internal/app/wiring/... ./internal/capabilities/assets/clips/... \
  ./internal/capabilities/jobs/... ./cmd/velox/... \
  ./internal/platform/sqlite/... ./internal/platform/instaeditpublishing/... \
  ./internal/capabilities/youtube/jobs/... ./internal/capabilities/mediamemory/... \
  ./internal/capabilities/brain/normalizer/... -count=1
gofmt -l <file toccati>
```

## 4. Esito (2026-11-24)

- Un solo owner per comportamento: `httpjson.ParseByteRange`,
  `jsonutil.FindMapByKey`, `textutil.CleanTags`,
  `textutil.CollapseWhitespace`, `sqlite.MigrationsDir`.
- `clips.TestParseByteRange` esercita il nuovo parser; il package `clips` passa.
- PASS: `pkg/httpjson`, `pkg/textutil`, `platform/sqlite/...` (105 s),
  `jobs`, `velox`, `youtube/jobs`, `mediamemory`, `brain/normalizer`,
  `instaeditpublishing`.
- `go vet` superficie stabile → rc 0; `gofmt -l` → vuoto.
- `sqlite.MigrationsDir()` è l'unico resolver di `migrations/sqlite`;
  `resolveJobsMigrationsDir` resta separato perché risolve `sqlite_jobs`.
- Commit: `c6126a4ef` — `refactor: share range, JSON, tag and whitespace
  helpers`.

## 5. Cosa resta

Niente di bloccante. Il ticket è DONE e committato; i cinque comportamenti
hanno un solo owner nei leaf package. Restano fuori perimetro push e suite
completa (`TestA1_ScaleBacklogInteractiveFirstClaimUnderBudget` flaky sotto
carico).

