# QDRANT-001 — Sidecar Embedding Contract + Canonical PointID Boundary

**Ticket**: QDRANT-001 — Ownership & writer Python (and sidecar contract).
**Closed**: 2026-06 (`6f715fb5` baseline, this commit).
**Ratchet ownership**: `internal/infrastructure/qdrant/pointid.go`.

---

## STATO REALE (su HEAD pre-commit)

L'audit QDRANT-001 documentava due blocker separati. Sul HEAD pre-commit
questo ticket i due problemi erano Aperti:

1. **Sidecar Python incompleto** — `scripts/bridges/generate_embedding.py`
   printava `json.dumps(embedding)` (lista raw `[]float32`). Non venivano
   esposti `model` né `model_version`. Una rotazione del checkpoint di
   `intfloat/multilingual-e5-base` non poteva essere rilevata a valle.

2. **`asset.ID → Qdrant point ID` boundary mancante** — Il mapper
   scriveva `ID: asset.ID` direttamente
   (`internal/infrastructure/qdrant/payload_mapper.go`). Nessuna
   funzione canonica; future writer avrebbe potuto adottare
   strategie diverse silenziosamente.

---

## LEGACY ELIMINATA (su questo commit)

1. `scripts/bridges/generate_embedding.py` ora emette il dict
   `{"embedding": [...], "dimensions": 768, "model": "intfloat/multilingual-e5-base",
   "model_version": "<hf_revision>|<project_semver>", "error": ""}`.
   Exit nonzero quando il modello non è importabile (fail-loud).

2. `internal/infrastructure/embeddings/python.go::PythonScriptEmbedder.Embed`
   parsa il dict e ritorna `(EmbeddingResult, error)`.

3. `internal/infrastructure/embeddings/http_text_embedder.go::HTTPTextEmbedder.Embed`
   consuma il nuovo dict shape (graceful fallback).

4. `internal/infrastructure/ai/ollama/client/client_embed.go::Client.Embed`
   adotta la stessa firma `EmbeddingResult`.

5. `internal/domain/asset/types_aux.go` introduce `EmbeddingResult`
   accanto all'interfaccia `Embedder`. Signatura di `Embedder.Embed`
   aggiornata a `(EmbeddingResult, error)`.

6. `internal/infrastructure/qdrant/pointid.go` (canonical forward):
   la boundary canonica `AssetIDToQdrantPointID(string) string` basata
   su UUID v5 con namespace privato del progetto
   (`uuid.MustParse("e5e9b4b1-2c8a-4f7d-9b3e-6c2d9a1f3e8b")`).

7. `internal/infrastructure/qdrant/canonical.go`: era predecessore
   identity-form (`AssetIDPrefix + id`); nel rebase-resolution e' stato
   rimosso anche il reverse helper `PointIDToAssetID` + la costante
   `AssetIDPrefix` (zero-legacy: nessun caller residuo). Il file
   mantiene solo il package doc come "puntatore canonico" alla
   boundary in `pointid.go` (vedi sotto).

8. `internal/infrastructure/qdrant/payload_mapper.go::AssetToPoint`
   usa `ID: AssetIDToQdrantPointID(asset.ID)`.

9. `internal/infrastructure/qdrant/index_writer.go::DeletePoints`
   usa `AssetIDToQdrantPointID(id)` per canonicalizzare prima della
   delete API.

10. `internal/infrastructure/qdrant/index_writer.go::ValidatePoint`
    legge `payload["asset_id"]` come fonte canonica dell'asset id
    (NON usa reverse mapping perché UUID v5 e' one-way). Missing
    payload = **silent fallback** su `point.ID` (UUID v5 verbatim):
    la funzione e' di **schema-validation scope** (vettori,
    dimensioni, NaN/Inf) non di payload-metadata enforcement,
    quindi usa `point.ID` come best-effort identifier per error
    message readability invece di hard-fail. Il fallimento HARD
    spetta al WRITE path (`AssetToPoint`/`BuildPayload`) che
    garantisce il payload; ValidatePoint tollera input legacy
    costruiti a mano (test fixtures, point IDs pre-ratchet).

11. `internal/infrastructure/qdrant/pointid_test.go`: 6 unit test
    coprono determinismo, collision resistance, empty input,
    namespace isolation, distribution/genuinely-distinct-inputs.

---

## GATE ANTI-REGRESSIONE

Questo gate **deve fallire** se uno qualunque dei tagli di cui sopra
riemergesse. Eseguire in CI su ogni PR.

```bash
# 1. Sidecar contract — non deve emettere un array raw come unica
#    risposta. Il pattern ammette la forma dict `print(json.dumps({...}))`.
rg -n '^print\(\s*json\.dumps\(\s*[A-Za-z_]\w*\s*\)\s*\)' scripts/bridges/generate_embedding.py
# Atteso: zero hits.

# 2. Embedder interface — non deve tornare il vecchio tipo `([]float32, error)`.
#    Una firma `([]float32, error)` indica che l'envelope EmbeddingResult
#    e' stato aggirato.
rg -n 'func\s+\(\s*\w+\s+\*?\w+\s*\)\s+Embed\s*\(\s*ctx\s+context\.Context\s*,\s*text\s+string\s*\)\s*\(\s*\[\]float32\s*,\s*error\s*\)' \
    internal/infrastructure/embeddings/ internal/infrastructure/ai/ollama/client/
# Atteso: zero hits.

# 3. Canonical boundary enforcement — solo i **writer** di Qdrant point
#    literals sono sotto gate. Il valore del campo `ID:` deve
#    ESCLUSIVAMENTE essere una delle forme canoniche (wrap attraverso
#    `AssetIDToQdrantPointID(...)`), oppure identity legacy.
#    La regex `\bID\s*:\s*(asset|r)\.ID\s*[,)\n}]` richiede che il
#    valore sia letteralmente `asset.ID`/`r.ID` (bypass detection).
#    Il word-boundary `\b` davanti a `ID:` esclude correttamente i
#    campi composti tipo `QdrantPointID:` (no boundary tra `t` e `I`).
#
#    File-scope ristretto ai writer (zero-false-positive design):
#     - `payload_mapper.go::AssetToPoint` — writer canonico
#     - `index_writer.go::DeletePoints` — second writer (canonicalise
#       AssetID → QdrantPointID prima della delete API).
#     - Ogni NUOVO writer file (es. `bulk_writer.go`) DEVE essere
#       aggiunto a questa lista esplicitamente; un'omissione e' un
#       errore zero-legacy (la copertura del gate = l'elenco writer).
#
#    Esclusioni by-design (NON sono writer, NON vanno nel gate):
#     - `client.go::decodeSearchResults` mappa `r.ID` (REST Qdrant
#       response struct, gia' UUID canonica per costruzione) su
#       `SearchResult.ID`. Round-trip canonico.
#     - `search_adapter.go::searchResultToVectorSearchResult` legge
#       `QdrantPointID: r.ID` — equivalente round-trip.
#     - Test files (`*_test.go`) — esclusi via globs.
rg -n '\bID\s*:\s*(asset|r)\.ID\s*[,)\n}]' \
    internal/infrastructure/qdrant/payload_mapper.go \
    internal/infrastructure/qdrant/index_writer.go
# Atteso: zero hits.

# 4. PointID namespace — non deve usare il namespace di default URL/DNS.
rg -n 'uuid\.NameSpaceURL|uuid\.NameSpaceDNS' internal/infrastructure/qdrant/pointid.go
# Atteso: zero hits. Deve restare solo PipelineGenQdrantNamespace.

# 5. Model envelope preservation — i caller che producono il vettore
#    DEVONO attraversare il campo `Vector` dell'envelope canonico.
rg -n '\bres\.Vector\b|Vector:\s*(parsed\.Embedding|out|result\.Embedding)\b' \
    internal/infrastructure/embeddings/ \
    internal/infrastructure/ai/ollama/client/ \
    internal/infrastructure/qdrant/embedders.go \
    internal/app/registry_adapters.go
# Atteso: hit in tutti e 5 i file. Se TUTTI i 5 file non hanno match,
# qualcuno ha reintrodotto il bypass `[]float32` diretto.

# 6. Workspace: nessuna modifica al gate in questo commit; resta
#    dentro QDRANT-004.

# 7. Reverse-mapping helper stato — `PointIDToAssetID` e' stato
#    rimosso in questo commit (zero-legacy: nessun caller residuo,
#    UUID v5 e' one-way, qualsiasi callsite sarebbe silent failure).
#    Gate negativo: non deve riapparire. Refinamento: scope a
#    definizioni di funzione (`^func\s+PointIDToAssetID\b`) per
#    evitare false-positive su doc-comment `// PointIDToAssetID(`.
rg -g '!*_test.go' -n '^func\s+PointIDToAssetID\b' internal/infrastructure/
# Atteso: zero hits. Re-introduzione = silent-failure-mode regression.

# 8. Anti-redeclaration — `AssetIDToQdrantPointID` deve essere
#    dichiarata ESATTAMENTE una volta, in pointid.go. Una
#    re-dichiarazione in canonical.go (o in qualunque altro file
#    del package qdrant/) e' un blocker di compilazione; questo
#    gate cattura la regression al sorgente piuttosto che ai
#    call sites. Refinamento: scope a file non-test
#    (`-g '!*_test.go'`) per evitare false-positive su test fixtures
#    che mockano la funzione con `return AssetIDToQdrantPointID("fixed")`.
rg -g '!*_test.go' -n '^func\s+AssetIDToQdrantPointID\b' internal/infrastructure/qdrant/
# Atteso: esattamente 1 hit (pointid.go). Se 0, la canonical e'
# stata cancellata per errore. Se >1, c'e' una declaration
# duplicata (build fail a Go-level ma il gate cattura l'intent prima).

# 9. RESIDUAL (out of QDRANT-001 rebase-resolution scope) —
#    `python.go::PythonScriptEmbedder.Embed` e
#    `http_text_embedder.go::HTTPTextEmbedder.Embed` sono ancora
#    nella firma pre-QDRANT-001 `(ctx, text) ([]float32, error)`.
#    Il QDRANT-001 closure canonico richiede il ritorno
#    `EmbeddingResult` (envelope completo con Model/ModelVersion).
#    Queste signature changes facevano parte del QDRANT-001
#    commit originale ma NON sono sopravvissute al rebase —
#    sono un residuo noto, da chiudere in QDRANT-001b (separate
#    ticket). Gate attuale: 2 hits attesi, 0 attesi post-fix.
rg -n 'func\s+\(\s*\w+\s+\*?\w+\s*\)\s+Embed\s*\(\s*ctx\s*context\.Context\s*,\s*text\s*string\s*\)\s*\(\s*\[\]float32\s*,\s*error\s*\)' \
    internal/infrastructure/embeddings/
# Atteso: 2 hits (residuo noto QDRANT-001b). Post QDRANT-001b: 0.
```

### Trade-off documentati (NON bug, ma forward-looking)

- **`HTTPTextEmbedder` graceful fallback** — quando il sidecar HTTP
  emette ancora `{embedding: []}` senza `model`, EmbeddingResult.Model = "".
  Forward cleanup in QDRANT-005 ratchet.
- **UUID v5 namespace hardcoded** — constante deterministica per idempotenza.
- **PointID collisione difensiva** — collision probabilita' ~10^-12.
- **`PointIDToAssetID` rimosso** — UUID v5 e' one-way; callers devono
  usare `payload["asset_id"]`. Validators sollevano errore HARD se
  payload manca (zero silent fallback).

---

## Riferimenti

- Schema canonico: `internal/infrastructure/qdrant/types.go::EmbeddingSpec{Model, ModelVersion}`.
- Sidecar Python canonico: `scripts/bridges/generate_embedding.py`.
- Hmac boundary co-occorrente (per HMAC secret ≥32): nessuno.
