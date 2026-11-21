# RA-20 — `regexp.MustCompile` compilato a runtime dentro le funzioni

**Data:** 2026-11-21
**Priorità:** P2 (CPU e allocazioni su percorsi per-risultato)
**Stato:** DONE
**Owner:** `capabilities/images`, `capabilities/youtube`, provider DuckDuckGo
**Origine:** report esterno 2026-11-21 §3.1; parzialmente verificato
**Verifica sul codice:** **DA MISURARE** — in `internal/` non-test ci sono **102**
occorrenze di `regexp.MustCompile`; il report ne cita 13 "dentro funzioni". La distinzione
package-level vs function-body è il primo passo del ticket.

## 1. Problema

Una `regexp.MustCompile` dentro il corpo di una funzione ricompila (e rialloca) a ogni
chiamata; se la funzione sta in un loop per risultato, il costo è per-hit. I siti citati dal
report:

- `provider_duckduckgo.go:247,248,290` — `<[^>]+>` compilata **due volte per risultato**
  dentro `parseDDGHTML`;
- `page_fetcher.go:102,105,106`;
- `internal/capabilities/images/search_scoring.go:16`;
- `tagutil_extract.go:35,70`;
- `internal/capabilities/youtube/metadata/metadata_extraction.go:103`;
- `internal/capabilities/images/ingest_utils.go:42-43`.

## 2. Soluzione

1. **Misurare** quali occorrenze sono davvero dentro un corpo di funzione:
   ```bash
   cd refactored
   python3 - <<'PY'
   import re, pathlib
   for p in pathlib.Path('internal').rglob('*.go'):
       if p.name.endswith('_test.go'): continue
       src = p.read_text(errors='ignore').splitlines()
       for i, line in enumerate(src, 1):
           if 'regexp.MustCompile' not in line: continue
           indent = len(line) - len(line.lstrip())
           # package-level: indentazione 0 (o dentro un var ( ) block con tab singolo)
           if indent > 0:
               print(f"{p}:{i}: {line.strip()}")
   PY
   ```
   Il risultato è la lista di lavoro; incollarla nella scheda.
2. **Promuovere a variabile di package** ogni pattern costante:
   ```go
   var ddgTagRE = regexp.MustCompile(`<[^>]+>`)
   ```
   Nominate per scopo, non per contenuto (`tagStripper`, non `re1`).
3. **Deduplicare i pattern identici** nello stesso package: `parseDDGHTML` non deve compilare
   due volte lo stesso `<[^>]+>`.
4. **Pattern dinamici**: se il pattern dipende da input, `regexp.Compile` resta, ma va spostato
   fuori dal loop e cachato per chiave (con cap, vedi RA-22), oppure sostituito da
   `strings`/`text/scanner` quando basta.
5. **Guard statico**: aggiungere un test `go/ast` nel package (o un percheck) che vieti
   `regexp.MustCompile` con indentazione > 0 in `internal/**`: deve fallire se qualcuno
   reintroduce il pattern. Allowlist solo per i casi dinamici documentati.
6. **Misura**: un benchmark del percorso caldo (`parseDDGHTML` con N risultati, o la funzione
   che contiene il sito) **prima e dopo**, con `-benchmem`. Senza numero non si dichiara
   miglioramento: si dichiara solo "una compilazione in meno per chiamata".

## 3. Check finali

```bash
cd refactored
# 1. i siti sono stati promossi
grep -rn "regexp.MustCompile" --include=*.go internal/capabilities/images/ internal/capabilities/youtube/ \
  | grep -v _test
# atteso: solo dichiarazioni `var … = regexp.MustCompile(...)`

# 2. il guard fallisce se il pattern rientra in una funzione
go test ./internal/... -run 'RegexpCompilePlacement|StaticRegexp'
# atteso: PASS (e il test deve fallire con un MustCompile indentato introdotto ad arte)

# 3. benchmark before/after (incollare entrambi)
go test ./internal/capabilities/assets/providers/... -run '^$' -bench 'DDG|Parse' -benchmem
# atteso: ns/op e allocs/op non peggiori; una compilazione in meno per chiamata

# 4. comportamento invariato
go test ./internal/capabilities/images/... ./internal/capabilities/youtube/... ./internal/capabilities/assets/providers/...
# atteso: PASS

# 5. build/vet/gate
go build ./... && go vet ./...
go run ./cmd/archcheck --strict   # exit 0
```

## 4. Rischi e rollback

- **Rischio**: promuovere a package-level cambia il momento in cui un pattern invalido fa
  panic (all'init invece che alla prima chiamata). Mitigazione: i pattern sono costanti e già
  validati dai test; un panic all'init è preferibile e più visibile.
- **Rischio**: un pattern dipende da configurazione. Mitigazione: resta `Compile` con cache,
  documentato nell'allowlist del guard.
- **Rollback**: revert per file.

## 6. Esito della verifica finale (2026-11-21)

Siti promossi a variabili di package (11 occorrenze in-function):

- `images/search_scoring.go` → `nonAlnumRE`
- `research/provider_duckduckgo.go` → `ddgResultLinkRE`, `ddgResultSnippetRE`, `htmlTagRE`
- `research/page_fetcher.go` → `htmlTitleRE`, `htmlNoiseRE`, `htmlAnyTagRE`
- `youtube/metadata/metadata_extraction.go` → `clipTimestampRE`
- `youtube/dto/tagutil_extract.go` → `capitalizedPhraseRE`, `conceptWordRE`
- `platform/sqlite/twinddl/shape.go` → `whereClauseRE`

Le occorrenze residue con indentazione sono dentro blocchi `var (...)` di package, non in
funzioni. Test dei package toccati: verdi.
