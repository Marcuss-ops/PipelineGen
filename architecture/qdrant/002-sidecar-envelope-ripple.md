# QDRANT-001b — Embedder Signature Ripple (residual envelope)

**Ticket**: QDRANT-001b — residual from QDRANT-001 closure.
**Opened**: 2026-06 (`bootstrap` baseline).
**Status**: OPEN.
**Reference**: `architecture/qdrant/001-sidecar-and-pointid.md` GATE #9 records
this as a known residual ("2 hits attesi, 0 attesi post-fix"). This ticket is
the ratchet for closing it.

---

## STATO REALE (su HEAD)

Il QDRANT-001 closure ha introdotto l'envelope canonico `asset.EmbeddingResult`
(Vector, Dimensions, Model, ModelVersion) e l'ha propagato attraverso
`internal/domain/asset/types_aux.go` (signatura dell'interfaccia `Embedder`)
e attraverso due implementazioni (`internal/infrastructure/embeddings/python.go`,
`internal/infrastructure/embeddings/http_text_embedder.go`).

Sul HEAD attuale il rebase-state ha **perso parzialmente** la chiusura di
QDRANT-001: l'`asset.Embedder` interface e la `EmbeddingResult` struct sono
state propagate, ma le due implementazioni sidecar sono tornate alla firma
legacy:

- `internal/infrastructure/embeddings/python.go:64` — `func (e *PythonScriptEmbedder) Embed(ctx context.Context, text string) ([]float32, error)`
- `internal/infrastructure/embeddings/http_text_embedder.go:59` — `func (e *HTTPTextEmbedder) Embed(ctx context.Context, text string) ([]float32, error)`

Conseguenze concrete:

1. **G2 gate failure** — `rg ... 'func(...) Embed(ctx, text) ([]float32, error)'`
   matcha queste 2 funzioni. Gate #2 atteso: 0 hits. Stato attuale: 2 hits.
2. **Inconsistenza con l'interfaccia canonica** — `asset.Embedder.Embed`
   ritorna `EmbeddingResult` ma le implementazioni concrete ritornano
   `[]float32`. Chi cerca il Model o ModelVersion non li trova.
3. **Sidecar envelope silently dropped** — il sidecar Python canonico
   (post-QDRANT-001 base) emette `model`/`model_version`/`dimensions` su
   stdout, ma la funzione Go li ignora (legge solo `embedding` come list).

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

## ACCEPTANCE CRITERIA

Il ticket si considera CHIUSO quando:

1. Entrambi i file di produzione aggiornati con la firma `EmbeddingResult`.
2. `go build ./internal/infrastructure/embeddings/... ./internal/infrastructure/qdrant/... ./internal/infrastructure/ai/ollama/client/` compila clean.
3. `go test -count=1 ./internal/infrastructure/embeddings/...` verde (nessuna regressione).
4. Test fixtures aggiornati: `go test -count=1 -run TestEmbedder ./...` verde.
5. G2 gate da `001-sidecar-and-pointid.md` ritorna 0 hits (vedi sotto).

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

## Implementation Playbook (per l'agente che chiude il ticket)

```
1. Read internal/infrastructure/embeddings/python.go per confermare
   lo stato corrente della firma + parsing (gia' CanonicalContract).
2. Read internal/infrastructure/embeddings/http_text_embedder.go
   (stessa finalita').
3. Edit python.go: cambiare firma + body per parsare envelope dict.
4. Edit http_text_embedder.go: stesso cambio, con graceful fallback.
5. Grep i test fixtures: `grep -rn 'new(PythonScriptEmbedder\|new(HTTPTextEmbedder' --include='*_test.go' .`
6. Per ogni test fixtures hit, adattare al consumo di EmbeddingResult.Vector.
7. Verificare build: `go build ./internal/infrastructure/embeddings/... ./internal/infrastructure/qdrant/... ./internal/infrastructure/ai/ollama/client/`
8. Verificare test: `go test -count=1 ./internal/infrastructure/embeddings/...`
9. Verificare G2 gate: deve ritornare 0 hits.
10. Commit con Co-authored-by trailer per AGENTS.md Git-Lesson-3.
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
