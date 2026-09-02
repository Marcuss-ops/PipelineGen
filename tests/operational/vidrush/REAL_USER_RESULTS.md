# PipelineGen — real-user acceptance results

This is the operator log for the live, user-visible checks. It records
observed artifacts and failures; a green HTTP response alone is not an
acceptance result.

Date: 2026-09-01  
Checkout: `main` at `121ed61af`  
Qdrant: `1.19.0`  
Runtime used for the isolated checks: `127.0.0.1:8012`

## Reproduction

Start an authenticated isolated server:

```bash
scripts/with-velox-auth bash -c '
  set -a; source /etc/pipelinegen/pipelinegen.env; set +a
  export VELOX_ALLOW_INSECURE_DEV=true VELOX_PORT=8012
  export VELOX_HOST=127.0.0.1 VELOX_SPLIT_DB_ENABLED=true
  export VELOX_JOBS_DB_PATH=/tmp/pipelinegen-user-e2e-jobs.sqlite
  setsid /tmp/pipelinegen-user-e2e --mode all \
    >/tmp/pipelinegen-user-e2e-8012.log 2>&1 < /dev/null &
'
```

Build the server from the checkout before starting it:

```bash
go build -o /tmp/pipelinegen-user-e2e ./cmd/server
```

The VidRush scenario runner uses the authenticated API surface:

```bash
scripts/with-velox-auth bash -c '
  export API_BASE=127.0.0.1:8012 VELOX_PORT=8012
  export SMOKE_TOKEN="$VELOX_ADMIN_TOKEN"
  bash tests/operational/vidrush/run_scenario.sh \
    tests/operational/vidrush/scenarios/06_local_stock.json
'
```

## Observed results

### Entity image retrieval — PASS with concrete file

```bash
curl --get http://127.0.0.1:8012/api/images/search \
  -H "Authorization: Bearer $VELOX_ADMIN_TOKEN" \
  --data-urlencode territory=retrieved \
  --data-urlencode q="Gerard Butler" \
  --data-urlencode limit=3
```

Observed result: HTTP 200, provider `wikipedia`, persisted asset ID
`23178b6b26451299e2e1d3916471de12a734a3e8a3b601b51f0ff2165490ece6`.
The downloaded file was a real JPEG, `710x946`, and visual inspection showed
Gerard Butler.

The same check for London produced a real `2048x1152` JPEG after provider
fallback, but upstream Wikimedia/SearXNG requests intermittently returned
429/timeout responses. This remains a provider-availability caveat.

A fresh live retrieval was repeated after the filename/path cleanup:

```text
query: London skyline landmark path verify 20260901 unique
asset: 93db1deeb3b13fe710549411faadbbde9e88331438beea7f8b63b2327d2e0b42
path:  data/media/images/duckduckgo/london-skyline-landmark-path-verify-20260901-unique.jpg
file:  JPEG, 2560x1440
HTTP:  200
```

Visual inspection showed a recognisable London City skyline, including The
Shard and the City cluster. The new path contains no URL/query-string
component. This is a PASS for real retrieval, local persistence, and visual
relevance; Drive delivery and Qdrant projection remain separate gates for
this retrieved-image route.

### Artlist existing-asset reuse — PASS

```bash
scripts/with-velox-auth env VELOX_PORT=8012 make artlist \
  TERM="barista making latte art live 20260901" \
  LIMIT=1 STRATEGY=default
```

Observed run: `job_1788274643056280716_0ded168a`, terminal `SUCCEEDED`,
`found=1`, `skipped=1`, `processed=0`. The item had a concrete Drive file ID
and a non-empty content hash. Logs explicitly reported
`verified_drive_and_hash` and canonical existing-asset reuse.

This proves reuse and persistence metadata. It does not by itself prove a
fresh download, because the selected asset already existed.

### Artlist fresh acquisition — BLOCKED by external provider

```bash
scripts/with-velox-auth env VELOX_PORT=8012 make artlist \
  TERM="antique 1920s mechanical orange sorting machine" \
  LIMIT=1 STRATEGY=default
```

Observed run: `job_1788274653174862579_f83f43a9`, terminal `FAILED`,
`found=1`, `processed=0`, `failed=1`.

The failure chain was concrete and fail-closed:

```text
Artlist daily download limit exceeded
→ yt-dlp fallback
→ Artlist Cloudflare HTTP 403
→ no media_assets success row
→ job FAILED
```

No fabricated local file, Drive ID, or indexed asset was reported.

### Local Stock — application path reaches real validation, fixture incomplete

Scenario `06_local_stock` initially exposed an invalid Qdrant filter shape;
that was fixed in `wire_script_sources.go`. After the fix, Qdrant returned
60 results and SQLite hydrated all 60, then the pipeline correctly rejected
the set because the available clips had no ready transcript text tracks:

```text
coverage 0.00 below required minimum 1.00
missing transcript text track
```

This is not accepted as a Local Stock PASS. The catalog needs real Mike Tyson
clip fixtures with ready transcripts before reuse/fallback can be certified.

### AI image generation — BLOCKED by external authentication

The generation endpoint reached the real Playwright/Chrome worker after the
system-browser fallback was added. Warmup then failed with the authoritative
reason:

```text
login required: user is logged out
```

Run `scripts/bridges/login.py` with an authenticated operator profile before
repeating the generation and text-to-video checks.

## Code/data hygiene verified during these checks

- No active `images.example.com` or synthetic Mediterranean candidate helper
  remains in the production VidRush adapter.
- `make verify-vidrush-local` passes, including all 17 scenario manifest
  dry-runs, the contract suite, and the Mediterranean payload identity check.
- The Mediterranean image scenario now declares canonical segment positions
  `0..4`, matching the identity contract checked by the runner.
- Retrieved image paths use provider directories instead of source URLs.
- Query strings are removed from downloaded image filenames.
- Qdrant remains a projection: missing SQLite truth is discarded.
- The isolated test server was stopped after each live run.

## Final acceptance status

`REAL_USER_ACCEPTANCE = INCOMPLETE`

## Rust migration certification

The migration certification was executed after the live checks. These gates
passed: Rust workspace test/check/clippy, release builds, worker health,
VisualNER grounding and 100-run determinism, MediaSampler semantic selection
and 100-run determinism, invalid-operation rejection, cancellation/zombie
scan, Media Intelligence certification, boundary scan, and legacy Go compute
scan.

`make verify-full` reached the repository Go tests but failed on existing
non-Rust regressions in stock naming, YouTube folder slug normalization, and
VidRush timing/error propagation tests. The Node checks themselves pass when
the host uses Node 22; the certification output also includes intentional
negative fixture checks. Crash recovery, RSS soak, and old-vs-Rust benchmark
remain `NOT_RUN` in the current certification script.

Remaining required evidence:

1. one fresh Artlist item downloaded, probed, persisted, uploaded to Drive,
   and indexed in Qdrant;
2. Local Stock reuse with transcript-ready catalog fixtures and zero remote
   calls on the warm request;
3. AI generation after Google authentication;
4. complete text → entity → image → overlay/timeline → rendered-video visual
   inspection.
