# Parallelization Architecture

> **Last updated**: 2026-06-01

PipelineGen uses a two-tier parallelization strategy for media generation:

- **Tier 1 (Go side):** `concurrent.ParallelMap` — generic concurrency helper for parallel task execution with semaphore limits
- **Tier 2 (Python side):** `SessionPool` — pre-warmed Playwright browser contexts for Google Vids automation

---

## Tier 1: `concurrent.ParallelMap`

### Package

`internal/pkg/concurrent/pool.go` — generic Go helper (since Go 1.18+ generics).

### Signature

```go
func ParallelMap[T, U any](items []T, concurrency int, fn func(int, T) U) []U
```

### How it works

1. Creates a **semaphore channel** (`make(chan struct{}, concurrency)`) limiting concurrent executions
2. Spawns a **goroutine per item**, acquiring a semaphore token before executing
3. Each goroutine calls `fn(idx, item)` and sends the result to a **buffered channel** with its original index
4. After all goroutines complete (`sync.WaitGroup`), results are **collected in original order** by index
5. Returns a `[]U` slice ordered identically to the input `[]T` slice

### Consumers

| Caller | File | Concurrency | Input → Output | Purpose |
|--------|------|-------------|----------------|---------|
| `Visualize()` | `handler_flow.go:244` | **9** | `[]string` → `[]VisualizeSegment` | Generates or reuses images for each script sentence |
| `HandleSourceScriptGenerateJob()` | `job_handler.go:138` | **6** | `[]string` → `[]GeneratedScene` | Generates images for each scene in a full script job |
| `HandleSourceScriptGenerateJob()` (translations) | `job_handler.go:307` | **5** | `[]GeneratedScene` → `[]GeneratedScene` | Translates scene texts to target languages |

### When to use

- You have a **uniform slice** of items to process independently
- Each item's processing is **I/O-bound** (API calls, file I/O, browser automation)
- Results must be returned **in the same order** as the input
- Errors should be handled **per-item** (embedded in the result type)

### When NOT to use

- Tasks depend on each other (sequential dependency)
- CPU-bound tasks (use a worker pool with `runtime.NumCPU()` instead)
- Dynamic/unknown work items (use a channel-based worker pool)

---

## Tier 2: Playwright Session Pool

### Service

`google-accounting/session_pool.py` — FastAPI sidecar service running on port 8000.

### Architecture

```
┌──────────────────────────────┐
│     Go Server (:8081)        │
│  ┌────────────────────────┐  │
│  │  concurrent.ParallelMap│  │  ← up to 9 concurrent goroutines
│  │  (concurrency = 9)     │  │
│  └────────┬───────────────┘  │
│           │ HTTP proxy       │
│           ▼                  │
│  POST /generate-vids-images  │
└──────────┬───────────────────┘
           │
           ▼
┌──────────────────────────────┐
│  Python FastAPI (:8000)      │
│  ┌────────────────────────┐  │
│  │    SessionPool          │  │  ← up to MAX_WARM_CONTEXTS browsers
│  │  ┌──────┐ ┌──────┐    │  │
│  │  │Chrome│ │Chrome│ ...│  │  │  pre-warmed at startup
│  │  │ ctx1 │ │ ctx2 │    │  │  │
│  │  └──────┘ └──────┘    │  │  │
│  └────────────────────────┘  │
└──────────────────────────────┘
```

### Configuration

#### Via environment variable (Python service)

```bash
# Set before starting the Python service
export MAX_WARM_CONTEXTS=9

# Or via .env file
echo "MAX_WARM_CONTEXTS=9" >> google-accounting/.env
```

#### Via YAML (Go config, for documentation)

```yaml
google_accounting:
  max_warm_contexts: 9   # Overridable via MAX_WARM_CONTEXTS env var
```

#### Default value

`9` — increased from the original `6` to support the `concurrent.ParallelMap` concurrency of 9 in `Visualize()`.

### How it works

1. **Startup**: `SessionPool.start()` initializes Playwright
2. **Warm-up**: `warmup_account("favamassimo")` creates up to `MAX_WARM_CONTEXTS` Chromium browser contexts, each navigating to Google Vids and verifying readiness
3. **Acquire**: When a generation request arrives, `acquire(account)` returns a warm, non-expired session
   - If all sessions are in use, creates a temporary ad-hoc session
   - Expired sessions (>30 minutes) are recycled
4. **Release**: `release(session)` returns the session to the pool for reuse
5. **Page pool**: For image generation, `acquire_page()` / `release_page()` manages pre-loaded Google Vids editor tabs

### Performance

| Concurrency | Warm Pool | Cold Start | Steady State |
|-------------|-----------|------------|--------------|
| 1 image | 1 context | ~40s | ~40s |
| 4 images | 6 contexts (old) | ~2-3 min | ~1 min |
| 9 images | 9 contexts | ~5-9 min | ~40-60s |

- **Cold start**: First request creates all warm contexts one-by-one (~5-10s each for Chromium + Google Vids navigation)
- **Steady state**: After pool is warm, all 9 images generate in parallel in approximately the same time as a single image (~40s)

### Tuning guidelines

| Use case | Recommended `MAX_WARM_CONTEXTS` | Notes |
|----------|-------------------------------|-------|
| Local dev / testing | `3-4` | Saves memory and CPU |
| Production (standard) | `9` | Matches `Visualize()` concurrency |
| Production (heavy) | `12-16` | If increasing Go-side `ParallelMap` concurrency |
| Resource-constrained | `6` | 2GB RAM per Chromium process ≈ 12GB+ total |

> **Memory note**: Each Chromium context uses ~1-2GB RAM. With `MAX_WARM_CONTEXTS=9`, expect ~9-18GB RAM usage for the Python service.

---

## End-to-End Flow: Visualize + Image Generation

```
1. POST /api/script/visualize
   ├── Sentences: ["A person tosses...", "The clock shows 3AM...", ...]
   │
   ├── concurrent.ParallelMap(sentences, 9, ...)
   │   │
   │   ├── goroutine 1: realtimeSvc.Match() → miss → GenerateSmartImage()
   │   ├── goroutine 2: realtimeSvc.Match() → HIT → reuse
   │   ├── ...
   │   └── goroutine 9: realtimeSvc.Match() → miss → GenerateSmartImage()
   │       │
   │       └── HTTP proxy → Python FastAPI
   │           │
   │           └── SessionPool.acquire("favamassimo")
   │               │
   │               └── Warm Chromium context → generate image → return
   │
   └── Results collected in original order → JSON response
```

## Related Files

| File | Role |
|------|------|
| `internal/pkg/concurrent/pool.go` | `ParallelMap` generic helper |
| `google-accounting/session_pool.py` | Playwright warm session pool |
| `google-accounting/config.py` | `MAX_WARM_CONTEXTS` env var definition |
| `internal/config/types.go` | `GoogleAccountingConfig.MaxWarmContexts` |
| `config.yaml` | `google_accounting.max_warm_contexts` |
| `internal/api/handlers/script/handlers/handler_flow.go` | `Visualize()` — consumer with concurrency 9 |
| `internal/api/handlers/script/handlers/job_handler.go` | Scene generation + translation consumers |
