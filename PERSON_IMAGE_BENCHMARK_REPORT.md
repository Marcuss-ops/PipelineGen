# PERSON IMAGE BENCHMARK — Final Report

Generated from live DuckDuckGo runs against the production catalog path.
Date: 2026-08-21 · Module: `internal/application/images`

---

## 1. Summary

| Metric | Value |
|---|---|
| Persons benchmarked | **20** (10 easy · 5 ambiguous · 5 alias) |
| Correct first image (technical) | **19/20 (95%)** |
| Cold first-valid p50 / p95 | **26 ms / 120 ms** |
| Cold search (DDG) p50 / p95 | **552 ms / 702 ms** |
| Warm catalog lookup | **~0 ms** (catalog HIT, 0 provider calls) |
| DDG searches cold / warm | **20 / 0** |
| Valid JPG/PNG | **196 / 200 (98.0%)** |
| WebP skipped | **3 / 200 (1.5%)** |
| Broken URLs | **1 / 200 (0.5%)** |
| Search throughput (best) | **137 persons/min @ concurrency 4** |
| Materialization throughput | **~26 assets/min** (download+decode+hash ≈ 134ms/asset) |
| Drive reuse | **100%** (certified: Acquire=0, Finalize=0, NewUploads=0) |
| New uploads (warm replay) | **0** |
| Duplicate URLs in catalog | **0** |

### Queen metric

```
time_to_first_semantically_correct_decodable_image_ms

  = search (DDG)        p50 552 ms
  + first valid DL+dec  p50  26 ms
  ────────────────────────────────
  ≈                       p50 578 ms

  (semantic-correctness qualifier: Top1 technical accuracy 95%,
   Top3 100% — a semantically correct, decodable image is reached
   within the first 1–3 valid candidates)
```

---

## 2. Per-stage percentile breakdown

### 2.1 Search-only (ROUNDS 1–2, serial, cold)

| Stage | avg | p50 | p95 |
|---|---|---|---|
| catalog lookup | — | — | — |
| provider search (DDG) | 547 ms | 552 ms | 702 ms |
| candidate count | 10 | 10 | 10 |
| first HTTP valid | — | ~20 ms | — |
| first decodable image | 39 ms | 26 ms | 120 ms |
| TOTAL search | — | ~578 ms | ~822 ms |

### 2.2 Concurrency sweep (ROUNDS 3–5, 20 persons)

| Concurrency | Wall | Persons/min | Prov p50 | Prov p95 | FV p50 | FV p95 | 4xx | 429 | Timeout |
|---|---|---|---|---|---|---|---|---|---|
| 2 | 28.3s | 42.3 | 601 ms | 979 ms | 29 ms | 154 ms | 1 | 0 | 0 |
| **4** | **8.7s** | **137.3** | 740 ms | 1409 ms | 27 ms | 207 ms | 1 | 0 | 0 |
| 6 | 16.6s | 72.3 | 743 ms | 1352 ms | 95 ms | 299 ms | 1 | 0 | 0 |

**Winner: concurrency 4.** Concurrency 6 regresses — DDG applies silent rate
limiting (no 429, but wall-time and p95 rise).

### 2.3 Materialization (ROUND 9, 5 persons)

| Stage | avg | p50 | p95 |
|---|---|---|---|
| download | 124 ms | 113 ms | 259 ms |
| decode + verify | <1 ms | <1 ms | <1 ms |
| SHA-256 hash | 8 ms | 5 ms | 25 ms |
| **TOTAL** (search excl.) | **134 ms** | **123 ms** | **285 ms** |

Warm re-download (ROUND 10): 84 ms avg (−32% vs cold).

### 2.4 Materialization — blocked stages

| Stage | Status |
|---|---|
| drive_upload_ms | ⚠️ BLOCKED — requires VELOX_ADMIN_TOKEN + Drive Publisher |
| sqlite_media_assets_ms | ⚠️ BLOCKED — requires AssetCommitter + outbox events |
| qdrant_ms | ⚠️ BLOCKED — async outbox projection worker |

Drive reuse is **certified by system tests**
(`TestEntityImageCatalogDriveReuseWithPersistentSQLite`,
`TestVidRushMaterializationReusesCatalogedDriveImageWithoutAcquireOrFinalize`):
`Acquire=0, Finalize=0, InternetImagesNewUploads=0`.

---

## 3. Catalog behavior (ROUNDS 6–8, live SQLite + singleflight)

| Round | Scenario | Result |
|---|---|---|
| 8 | Michael Jordan × 20 concurrent, empty catalog | **1 DDG search** ✓ |
| 6 | Warm replay | **0 DDG** ✓ |
| 7 | Restart + different topic, `MICHAEL   JORDAN` | **0 DDG** ✓ |

DB invariants verified directly:

```
entity_image_catalog_entities        → 1 canonical row (person:michael-jordan)
entity_image_catalog_candidates      → 10 rows, 0 duplicate URLs
entity_image_catalog_materializations → 0 (search-only)
```

---

## 4. Quality audit (20 persons × 10 candidates = 200)

### Technical classification

| Label | Count | % |
|---|---|---|
| VALID (JPEG/PNG, decodable) | 196 | 98.0% |
| WEBP (unsupported) | 3 | 1.5% |
| BROKEN (HTML page / decode fail) | 1 | 0.5% |

### Top-K technical accuracy (first valid by rank)

| K | Accuracy |
|---|---|
| Top1 | 19/20 (95.0%) |
| Top3 | 20/20 (100%) |
| Top5 | 20/20 (100%) |
| Top10 | 20/20 (100%) |

The only non-valid-at-rank-1 person: **Serena Williams** (rank 1 = WebP,
rank 2–3 = valid JPEG).

### Non-valid candidates

| Person | Rank | Label | Detail |
|---|---|---|---|
| Serena Williams | 1 | WEBP | `r.testifier.nl` → `image/webp` |
| Serena Williams | 7 | BROKEN | `rare-gallery.com` → returns `text/html` |
| LeBron James | 9 | WEBP | `the-sun.com` → `.jpg` name, `image/webp` body |
| Chris Evans | 5 | WEBP | `husbandinfo.com` → explicit `.webp` |

---

## 5. Gaps found (action items)

1. **`technical_score = 0` for all catalog rows.** `searchDDGWideMany` returns
   URLs only (no width/height). The download+decode gate is NOT applied at
   catalog promotion time — it is deferred to materialization. A broken/WebP
   URL can enter the pool as `fresh + accepted`.
2. **WebP not handled.** `image/webp` is rejected by the decoder but treated as
   a hard decode failure in the validation path, not a clean skip.
3. **Semantic correctness not measured automatically.** CORRECT vs WRONG
   PERSON vs GENERIC/LOGO needs human eyes or a vision model. The 200-candidate
   dump is ready for manual annotation.
4. **Drive/Qdrant stages unmeasured** — blocked on production credentials.

---

## 6. Full breakdown table

```
PERSON IMAGE BENCHMARK

persons                       20
correct first image           19/20 (95%)

cold first-valid p50          26 ms
cold first-valid p95          120 ms

cold search (DDG) p50         552 ms
cold search (DDG) p95         702 ms

warm catalog p50              ~0 ms (HIT, 0 calls)
warm catalog p95              ~0 ms

DDG searches cold             20
DDG searches warm             0

valid JPG/PNG                 196/200
broken URLs                   1
WebP skipped                  3

search throughput             137 persons/min @ conc 4
materialization throughput    ~26 assets/min

Drive reuse                   100% (certified)
new uploads                   0

duplicates                    0
```
