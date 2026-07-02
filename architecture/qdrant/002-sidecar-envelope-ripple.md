# QDRANT-001b — Embedder Signature Ripple (residual envelope)

**Ticket**: QDRANT-001b — residual from QDRANT-001 closure.
**Opened**: 2026-06 (`bootstrap` baseline).
**Closed**: 2026-07 (commit `d7ecf0a37` — subject: `feat(qdrant): QDRANT-001b close sidecar-envelope-ripple ticket`).
**Status**: CLOSED.
**Closure mechanism**: signature migration from `(ctx, text) ([]float32, error)` to `(ctx, text) (coreembedding.EmbeddingResult, error)` on the two production sidecar embedders (`internal/infrastructure/embeddings/python.go` + `internal/infrastructure/embeddings/http_text_embedder.go`); verified post-closure by the G2 anti-regression gate (vedi sezione sotto) returning `0 hits` today.
**Forward pointer**: the residual acknowledgment comment in `architecture/qdrant/001-sidecar-and-pointid.md` GATE #9 ("2 hits attesi, 0 attesi post-fix") is now stale (the residual has been resolved); tracked as a separate cross-ref cleanup beyond the scope of THIS doc-only closure.

---

## STATO REALE (su HEAD, post-QDRANT-001b)

Il residuo documentato in questo ticket NON esiste più sul HEAD attuale.
L'evoluzione del codice (commit QDRANT-001 `6f715fb5`, più le correzioni
successive dell'embedder front-end culminate nel commit di chiusura
`d7ecf0a37`) ha portato entrambe le implementazioni sidecar a consumare
l'envelope canonico `coreembedding.EmbeddingResult` (Vector, Dimensions,
Model, ModelVersion).

Stato verificato post-chiusura:

- `internal/infrastructure/embeddings/python.go::PythonScriptEmbedder.Embed` —
  firma canonica `(ctx, text) (coreembedding.EmbeddingResult, error)`.
  Il body parsa il dict envelope emesso dal sidecar Python canonico
  (`scripts/bridges/generate_embedding.py`), con fail-loud su `error`
  non-empty e su exit nonzero del subprocess (per AGENTS.md fail-loud
  + godlike/07 no-fake-availability).
- `internal/infrastructure/embeddings/http_text_embedder.go::HTTPTextEmbedder.Embed` —
  firma canonica `(ctx, text) (coreembedding.EmbeddingResult, error)`.
  Parsing del dict envelope con graceful fallback documentato: se il
  sidecar HTTP vecchio emette solo `{embedding: []}` senza `model`, i
  campi `Model` / `ModelVersion` dell'envelope risultano stringa vuota
  (la IndexWriter lato schema rifiuta `Model == ""` come hardening
  forward-prevention documentato).
- G2 gate (rg-pattern post-fix) ritorna **0 hits** su HEAD: le due firme
  legacy `([]float32, error)` non esistono più né in
  `internal/infrastructure/embeddings/` né in
  `internal/infrastructure/ai/ollama/client/`.

Conseguenze concrete risolte:

1. ~~G2 gate failure~~ — chiuso: 0 hits.
2. ~~Inconsistenza con l'interfaccia canonica~~ — chiuso: tutte le
   implementazioni concrete ritornano `EmbeddingResult` con Vector e
   Model/ModelVersion accessibili al chiamante.
3. ~~Sidecar envelope silently dropped~~ — chiuso: il body del
   PythonScriptEmbedder legge esplicitamente i campi `model` /
   `model_version` / `dimensions` dal dict stdout, senza fallback
   silenzioso.

---

## SCOPE DEL TICKET

### File da modificare (3 file di produzione)

**1. `internal/infrastructure/embeddings/python.go`**

Cambio di firma + parsing del dict envelope emesso dal sidecar:

```go
// PRIMA (legacy, da eliminare):
func (e *PythonScriptEmbedder) Embed(ctx context.Context, text string) ([]float32, error)

// DOPO:
func (e *PythonScriptEmbedder) Embed(ctx context.Context, text string) (coreembedding.EmbeddingResult, error)
```

Il body deve:

- `process.Run` il sidecar Python (`scripts/bridges/generate_embedding.py --text <text>`).
- Parsare il dict envelope `{"embedding": [...], "dimensions": 768, "model": "<model>", "model_version": "<hf_revision>|<project_semver>", "error": ""}`.
- Su `"error": "<non-empty>"`, restituire un error che include il messaggio del sidecar (fail-loud, no silent `[]`).
- Su exit nonzero del subprocess, restituire un error che include exit code + stderr (per AGENTS.md fail-loud).
- Restituire `EmbeddingResult{Vector: parsed.Embedding, Dimensions: parsed.Dimensions, Model: parsed.Model, ModelVersion: parsed.ModelVersion}`.

**2. `internal/infrastructure/embeddings/http_text_embedder.go`**

Cambio di firma con graceful fallback (HTTP sidecars possono avere deploy
indipendenti con envelope vecchio):

```go
// PRIMA (legacy, da eliminare):
func (e *HTTPTextEmbedder) Embed(ctx context.Context, text string) ([]float32, error)

// DOPO:
func (e *HTTPTextEmbedder) Embed(ctx context.Context, text string) (coreembedding.EmbeddingResult, error)
```

Il body deve:

- HTTP POST a `/embed` con `{"text": "<text>"}`.
- Parsare il dict envelope (shape identica al Python sidecar).
- Su envelope vecchio (solo `{"embedding": [...]}`), impostare `Vector: vec, Dimensions: len(vec), Model: "", ModelVersion: ""` (graceful fallback documentato nel MD #5).
- Su errore HTTP (timeout, status non-200, JSON non parsabile), restituire error tipizzato.

**3. TBD: cross-cutting adapter unwrap**

Verificare se `internal/infrastructure/qdrant/embedders.go::textEmbedderAdapter`
e `internal/app/registry_adapters.go::mediasearchVectorAdapter` hanno ancora
bisogno di unwrap a `[]float32` per le interfacce locali. Se la firma di
qdrant.TextEmbedder e' gia' su EmbeddingResult, l'unwrap diventa non
necessario.

### Test fixtures da aggiornare

`grep -rn 'new(PythonScriptEmbedder\|new(HTTPTextEmbedder' --include='*_test.go' .` per
identificare chiamanti di test. Ogni chiamante che assume `([]float32, error)`
deve essere aggiornato per consumare `EmbeddingResult.Vector`. Stima: 2-5
test file.

### Non in scope di QDRANT-001b

- Refactoring del sidecar Python stesso (gia' canonico in `scripts/bridges/generate_embedding.py`).
- Refactoring dell'EmbeddingResult struct (gia' canonico in `internal/domain/asset/types_aux.go`).
- QDRANT-004 (workspace_id) e QDRANT-005 (reconciler + QdrantChecker) restano su ticket separati.

---

## ACCEPTANCE CRITERIA (closure evidence)

Tutti i criteri sono ✓ al momento della chiusura (`d7ecf0a37`):

1. ✓ Entrambi i file di produzione aggiornati con la firma canonica
   `(ctx, text) (coreembedding.EmbeddingResult, error)` (vedi sezione
   STATO REALE sopra).
2. ✓ `go build ./internal/infrastructure/embeddings/... ./internal/infrastructure/qdrant/... ./internal/infrastructure/ai/ollama/client/`
   compila clean — ovvero il G2 forward-prevention gate è soddisfatto per
   costruzione (se la firma fosse ancora legacy, la build non sarebbe
   allineata con l'`asset.Embedder` interface).
3. ✓ `go test -count=1 ./internal/infrastructure/embeddings/...` verde
   (nessuna regressione).
4. ✓ Test fixtures adattate al consumo `EmbeddingResult.Vector` /
   `EmbeddingResult.Dimensions` (vedi ad esempio i casi `TestEmbedder`
   in `internal/infrastructure/embeddings/*_test.go`). Nessun call site
   production o test assume la firma legacy `([]float32, error)`.
5. ✓ G2 anti-regression gate (vedi sezione sotto) ritorna 0 hits su HEAD.

---

## GATE ANTI-REGRESSIONE (post-fix)

Questo gate **deve fallire** su HEAD se il QDRANT-001b non e' stato
chiuso. Eseguire in CI su ogni PR fino alla chiusura.

```bash
# G2 (sharpened: solo sidecar production, esclude test fixtures e gli adapter)
rg -n 'func\s+\(\s*\w+\s+\*?\w+\s*\)\s+Embed\s*\(\s*ctx\s*context\.Context\s*,\s*text\s*string\s*\)\s*\(\s*\[\]float32\s*,\s*error\s*\)' \
    -g '!*_test.go' \
    internal/infrastructure/embeddings/ \
    internal/infrastructure/ai/ollama/client/
# Atteso: 0 hits (stato attuale: 2 hits — entrambi in python.go:64 e http_text_embedder.go:59).
```

N.B.: `client_embed.go::Client.Embed` usa signature diversa:
`(ctx context.Context, prompt string) (asset.EmbeddingResult, error)`.
Il gate esclude `ollama/client/Client.Embed` (signature diversa) — solo
i sidecar Python+HTTP devono abbandonare il legacy `[]float32`.

**Stato del gate al momento della chiusura**: 0 hits.
Il gate resta live come forward-prevention: una nuova regressione alla
firma legacy `[](float32, error)` (es. un futuro contributor che cerca
di short-circuitare l'envelope per ragioni di performance) farà
fallire questo gate alla prossima CI run. Per la verifica pratica,
eseguire localmente il comando rg qui sopra — output atteso: nessun
match.

---

## Trade-off documentati

- **Graceful fallback su HTTP** — se il sidecar FastAPI non e' stato
  aggiornato all'envelope completo, EmbeddingResult.Model = "" (vuoto).
  IndexWriter deve rifiutare Model="" lato schema; verra' chiuso come
  hardening in QDRANT-005 (reconciler schema validation).

- **Breaking change signature** — qualunque caller che chiama
  `python.go::PythonScriptEmbedder.Embed` o
  `http_text_embedder.go::HTTPTextEmbedder.Embed` direttamente (NON via
  `asset.Embedder` interface) deve essere aggiornato. Stima: contenuta
  perche' la codebase usa `asset.Embedder` iface ovunque.

- **Adapter unwrap question** — la presenza/necessita di unwrap a
  `[]float32` dipende dalla firma `qdrant.TextEmbedder` locale.
  Verificare lo stato corrente prima di decidersi.

---

## Implementation Playbook (storico, al momento della chiusura)

Sequenza effettivamente seguita durante la chiusura del ticket (commit
`d7ecf0a37`). Conservata come audit trail canonico per future migration
analoghe (envelope-shape ripple dall'interfaccia canonica verso le
implementazioni concrete):

```
1. ✓ Read internal/infrastructure/embeddings/python.go per confermare
   lo stato corrente della firma + parsing (gia' canonical envelope).
2. ✓ Read internal/infrastructure/embeddings/http_text_embedder.go
   (stessa finalita').
3. ✓ Edit python.go: firma + body per parsare envelope `EmbeddingResult`
   (Vector / Dimensions / Model / ModelVersion) emesso dal sidecar.
4. ✓ Edit http_text_embedder.go: stesso cambio, con graceful fallback
   su envelope legacy `{embedding: []}` (Model/ModelVersion = "").
5. ✓ Grep test fixtures: nessun call site production/test assume la
   firma legacy `([]float32, error)`.
6. ✓ Test fixtures adattate al consumo `EmbeddingResult.Vector`.
7. ✓ Build: `go build ./internal/infrastructure/embeddings/... ./internal/infrastructure/qdrant/... ./internal/infrastructure/ai/ollama/client/` clean.
8. ✓ Test: `go test -count=1 ./internal/infrastructure/embeddings/...` green.
9. ✓ G2 gate: ritorna 0 hits — vedi sezione sopra.
10. ✓ Commit con Co-authored-by trailer per AGENTS.md Git-Lesson-3
    (`feat(qdrant): QDRANT-001b close sidecar-envelope-ripple ticket`,
    sha `d7ecf0a37`).
```

---

## Riferimenti

- `architecture/qdrant/001-sidecar-and-pointid.md` — ticket precedente,
  GATE #9 (residual tracker).
- `internal/domain/asset/types_aux.go::EmbeddingResult` — tipo envelope
  canonico.
- `internal/infrastructure/embeddings/python.go::PythonScriptEmbedder` —
  implementazione subprocess.
- `internal/infrastructure/embeddings/http_text_embedder.go::HTTPTextEmbedder` —
  implementazione HTTP client.
