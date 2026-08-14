# Qdrant projection-lag + ENOMEM recovery runbook

Recovery for the two failure modes that can stop PipelineGen boot or a
projection rebuild:

1. **Projection lag** — the active alias points to a collection whose
   `projection_seq` is behind the SQLite registry, so the startup
   `qdrant-collection` step fails closed:

   ```text
   required step "qdrant-collection" failed:
   reconcile projection "media_assets_v3_nomic_768_siglip_768_...":
   mediaregistry: projection sequence lags registry:
   projection_seq=283 registry_seq=291
   ```

2. **ENOMEM on reindex** — `reindex-qdrant --apply` aborts mid-populate
   with `os error 12` even though `free -h` shows RAM available:

   ```text
   reindex batch upsert: qdrant: HTTP 500:
   {"status":{"error":"Service internal error: Out of memory,
   free: 25631436800, IO Error: Cannot allocate memory (os error 12)"}...}
   ```

Invariants that make recovery safe:

- **SQLite is the canonical state store; Qdrant is a derived projection**
  and is always rebuildable from SQLite. No recovery step below mutates
  ground truth.
- **Reindex is blue-green (PR 13)**: `--apply` never writes into the
  active collection; it builds a new timestamped collection, runs the
  strict PR 12 verifier, and only switches the alias on `Ready=true`.
  The previous collection is retained as a rollback target.
- **Retention is registry-aware**: it never deletes the active alias
  target, never lets a `FAILED`/`BUILDING` partial crowd out a
  known-good rollback target, and fails closed if the projection
  registry cannot be hydrated.

## §0 — When to use this runbook

- PipelineGen is in a crash-loop with the `qdrant-collection` step
  failing and a `projection sequence lags registry` fatal in the journal.
- `reindex-qdrant --apply` fails with `os error 12` / `Cannot allocate
  memory` while the host still has free RAM.
- Qdrant has accumulated dozens of `media_assets_v3_*` collections and
  is slow to reach `ready` after a restart.

## §1 — Confirm the failure mode (read-only)

```bash
# 1. Live kernel limit vs the mmap pressure Qdrant is under.
sysctl -n vm.max_map_count
QDPID=$(pgrep -f qdrant | head -1)
[ -n "$QDPID" ] && wc -l /proc/$QDPID/maps

# 2. Registry vs projection sequence (the boot fatal source).
#    Compare the active alias target's projection_seq against the
#    SQLite mediaregistry registry_seq.

# 3. Qdrant reachability + collection/alias inventory (no writes).
curl -s --max-time 8 http://127.0.0.1:6333/healthz
curl -s --max-time 8 http://127.0.0.1:6333/aliases
curl -s --max-time 8 http://127.0.0.1:6333/collections
```

Build a table before touching anything: which collection is the active
alias target (`media_assets_current`), which is the previous known-good
rollback, which are `RETIRED`, and which are `FAILED`/partial.

## §2 — Host fix for ENOMEM (mmap exhaustion)

`os error 12` with free RAM is the classic symptom of hitting
`vm.max_map_count` (Qdrant memory-maps on-disk vector segments; many
accumulated collections exhaust the mapping limit).

```bash
# Apply now:
sudo sysctl -w vm.max_map_count=655300

# Persist across reboots:
printf 'vm.max_map_count=655300\n' \
  | sudo tee /etc/sysctl.d/99-pipelinegen-qdrant.conf
sudo sysctl --system

# Confirm:
sysctl -n vm.max_map_count          # -> 655300
```

`655300` is an operational margin, not a Qdrant-mandated value. Wait for
Qdrant to finish loading collections before proceeding:

```bash
for i in $(seq 1 30); do
  code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 http://127.0.0.1:6333/healthz)
  [ "$code" = "200" ] && break
  sleep 5
done
```

## §3 — Retention sweep (registry-aware, never blind `DELETE`)

Do **not** issue `DELETE /collections/<name>` by hand. Use the
registry-aware sweep, which only drops non-active collections matching a
schema prefix and always protects the active alias target.

```bash
# Dry-run first — it must report the exact drop set and keep set.
go run ./cmd/admin dr-qdrant apply-retention \
  --retention-days=1 \
  --keep-last-n=1 \
  --protect=<previous_known_good_collection> \
  --retired-prefix=media_assets_v3_e5_768_siglip_768 \
  --dry-run --json

# Apply only after the dry-run set is verified.
go run ./cmd/admin dr-qdrant apply-retention \
  --retention-days=1 \
  --keep-last-n=1 \
  --protect=<previous_known_good_collection> \
  --retired-prefix=media_assets_v3_e5_768_siglip_768 \
  --json
```

Notes:

- `--retired-prefix` is repeatable; one entry per retired schema. The
  current schema (`media_assets_v3_e5_768_siglip_768`) is matched
  automatically via `schema.CanonicalName()`. Pre-migration the active
  schema was `media_assets_v3_nomic_768_siglip_768`; pass it as a
  `--retired-prefix` when sweeping leftovers from the nomic→e5
  migration (§6).
- `--keep-last-n` has a hard floor of `2` (active + one rollback).
- `FAILED`/`BUILDING` partials are **never** protected: they are drop
  candidates, while the known-good rollback target is kept.
- `UNKNOWN` collections (no registry state) are left untouched.

## §4 — Rebuild the projection (blue-green)

```bash
# Dry-run: enumerate what --apply would reindex (no writes).
go run ./cmd/admin reindex-qdrant --dry-run --json

# Apply: new timestamped collection -> populate -> PR 12 verify ->
# atomic alias switch (previous collection retained for rollback).
go run ./cmd/admin reindex-qdrant --apply --json
```

`--apply` only switches `media_assets_current` when the PR 12 verifier
reports `Ready=true`. On failure the alias is unchanged and the partial
collection is left for inspection/cleanup.

## §5 — Verify

```bash
systemctl is-active pipelinegen          # -> active
curl -s -o /dev/null -w '%{http_code}\n' --max-time 8 http://127.0.0.1:6333/healthz   # -> 200
# Re-run the §3 dry-run; it must report collections_dropped: 0 (idempotent).
```

## §6 — Embedding model migration (nomic → e5) and query-embedder drift

The text embedding contract is a four-leg SSOT
(`internal/kernel/embedding.CanonicalText`):

1. **canonical** — `intfloat/multilingual-e5-base`, rev `2026-06-26-v1`,
   768 dims, L2-normalized, Cosine, `query: `/`passage: ` prefixes,
   semantic-document v3.
2. **sidecar runtime** — `GET /contract` on the Python embedding sidecar
   (default `http://127.0.0.1:8001`).
3. **Qdrant active collection** — the `text` vector of the
   `media_assets_current` alias target (compared partial: dim + distance).
4. **query embedder** — configured by `ollama_embed_model`
   (`OLLAMA_EMBED_MODEL`); must equal the canonical model id.

Any divergence fails closed at boot.

### Failure signatures

```text
# composition-time gate
QDRANT_EMBEDDING_CONTRACT_MISMATCH: collection_model=intfloat/multilingual-e5-base runtime_model=nomic-embed-text

# readiness-barrier handshake
lifecycle readiness barrier failed: embedding contract handshake:
  embedding contract probe: sidecar "http://127.0.0.1:8001" does not expose /contract (HTTP 404)
```

The first means `config.yaml` `ollama_embed_model` (or `OLLAMA_EMBED_MODEL`)
still points at the legacy nomic model while the schema SSOT is e5. The
second means the sidecar predates the `/contract` endpoint and must be
upgraded/restarted.

### The drift (root cause)

Document vectors were always produced by the **E5 sidecar** (`POST /index`),
so SQLite `embedding_json` is e5. The **query embedder** was the legacy
Ollama client configured with `nomic-embed-text`, emitting queries in a
*different* vector space. Both are 768d/Cosine, so the old dimension-only
check passed while semantic search silently returned wrong neighbors — the
documents and queries were incompatible without any error.

### The fix

1. **Route the query embedder through the E5 sidecar, not Ollama.** The
   two composition sites now use `embeddings.NewHTTPTextEmbedder(
   cfg.ClipIndexer.ServerURL)`:
   - `internal/app/wire_script_resolvers.go` (SourceSearch +
     AssetSearchPort)
   - `internal/app/wire_services_orchestration.go` (Qdrant readiness
     canary)
   Ollama remains the chat/legacy embedder and must never silently create
   a second vector space.
2. **Point the query-embedder config leg at e5.**
   `config.yaml` `ollama_embed_model: intfloat/multilingual-e5-base` (or
   `OLLAMA_EMBED_MODEL`). This is the handshake's query leg, not the
   Ollama model used to produce embeddings.
3. **Reindex to e5.** The stored vectors are already e5, but the schema
   name/signature must reflect it:

   ```bash
   go run ./cmd/admin reindex-qdrant --dry-run --json   # expect media_assets_v3_e5_768_siglip_768
   go run ./cmd/admin reindex-qdrant --apply --json
   ```

### Verify the migration

```bash
# sidecar contract (model_id + revision)
curl -s --max-time 8 http://127.0.0.1:8001/contract
# active alias must point at an e5 collection
curl -s --max-time 8 http://127.0.0.1:6333/aliases
# readiness: handshake + E5 canary search
curl -s --max-time 8 http://127.0.0.1:8000/qdrant/ready
```

A live semantic canary is the strongest check: embed a query via the
sidecar and confirm the top hits match the query's domain (e.g. `actor
interview` → actor-interview clips, not random assets).

### Retire the nomic rollbacks (post-migration)

After the alias swap the superseded nomic collections are `RETIRED` in
the projection registry. Sweep them with §3, but do **not** keep one as
the retention rollback:

- A nomic collection is **not a valid rollback** for the e5 active: its
  document vectors live in the nomic space, so an alias rollback to it
  would re-introduce the exact query/documents drift this migration fixes
  (the e5 query embedder would immediately mismatch the nomic documents).
- The retention `--keep-last-n` floor of `2` protects "active + 1
  rollback" by picking the newest `RETIRED` name — which, right after the
  migration, is a nomic collection. That protection is wrong for a
  cross-schema rollback, so the last nomic must be dropped explicitly.

Procedure:

```bash
# 1. Sweep the older nomic collections (registry-aware; the
#    keep-last-n floor leaves one nomic as the "rollback").
go run ./cmd/admin dr-qdrant apply-retention \
  --retention-days=1 \
  --keep-last-n=2 \
  --retired-prefix=media_assets_v3_nomic_768_siglip_768 \
  --json

# 2. Once the e5 collection is proven stable (canary + semantic search
#    correct), delete the last nomic rollback explicitly. Verify the
#    preconditions first: registry status is RETIRED and the alias does
#    NOT point at it.
NAME="media_assets_v3_nomic_768_siglip_768_<timestamp>"
curl -s --max-time 30 -X DELETE "http://127.0.0.1:6333/collections/$NAME"
```

Leaving zero rollback is acceptable here because SQLite is the canonical
store: any lost projection is rebuilt with `reindex-qdrant --apply` (§4).
The registry rows stay `RETIRED` for audit — only the physical collection
is dropped.
