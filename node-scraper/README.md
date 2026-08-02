# Node-Scraper — Artlist Operator Contract

The node-scraper is the source-of-truth for fetching Artlist clip
metadata. It exports two operator-visible contracts that downstream
consumers (the Go pipeline, the `tests/operational/artlist/*` gates,
and the future `tests/operational/lib/artlist.sh::artlist_detail`
implementation) depend on. Any change to these contracts is a breaking
change and MUST be reflected here on the same commit.

Source files (audit anchors):
- `src/driver/cookies.js`        — cookie parser (env var, file format, fallback)
- `src/scrape/detail-page.js`    — `/detail` extractor (happy + STREAM_NOT_FOUND paths)
- `src/scrape/url.js`            — clip-id extractor (URL canonicalisation)
- `test/detail-page.test.js`     — unit-test fixtures for the above
- `tests/operational/artlist/03_detail_stream.sh`  — Gate 1 hard gate (this README's contract MUST be enforced there)

---

## 1. `ARTLIST_COOKIE_FILE` env var

`ARTLIST_COOKIE_FILE` is an OPTIONAL operator-supplied path to a cookie
file that authenticates the scraper against Artlist's CMS. The scraper
reads the file **on every invocation of `fetchClipDetails()`** (no
in-memory cache, no SIGHUP reload). Operators can swap the file at
any time, but they MUST do so **atomically** (`mv` rather than direct
overwrite) to avoid a partial-read race.

### 1.1 Default

If `ARTLIST_COOKIE_FILE` is unset or empty, no cookie file is loaded. The
scraper uses anonymous Chrome by default. Set `ARTLIST_COOKIE_FILE` explicitly
when an authenticated session is required; the legacy
`DEFAULT_COOKIE_FILE_PATH = '/tmp/artlist_cookies.txt'` remains available to
callers that opt in to that path.

### 1.2 Accepted formats

The parser (`src/driver/cookies.js::importCookies`) auto-detects which
of two formats the file contains. The decision is purely lexical:
a file whose first non-whitespace character is `[` or `{` is treated
as JSON; anything else is treated as Netscape.

#### Format A — JSON (bare Puppeteer-style array)

```json
[
  {
    "name": "session",
    "value": "eyJhbGciOi...",
    "domain": ".artlist.io",
    "path": "/",
    "expires": 1798765432,
    "httpOnly": true,
    "secure": true
  }
]
```

Each object is normalised into the shape `page.setCookie` expects:

| Field    | Required | Notes |
|----------|----------|-------|
| `name`   | YES      | The cookie name (e.g. `session`, `xsrf-token`). |
| `value`  | YES      | The cookie value. Tabs in the value are preserved. |
| `domain` | YES      | Cookie domain. Leading `.` is preserved (subdomain inclusion). |
| `path`   | no       | Defaults to `/`. |
| `expires`| no       | Unix seconds; values ≤ 0 or non-numeric are dropped (Puppeteer handles session cookies separately). |
| `httpOnly`| no      | Boolean. |
| `secure` | no       | Boolean. |
| `sameSite`| no      | One of `Strict`, `Lax`, `None`; any other value is dropped (Puppeteer refuses unknown same-site tokens). |

> ⚠️ **Playwright `storageState()` outputs ARE NOT accepted as raw input.**
> `storageState()` writes `{ "cookies": [...], "origins": [...] }`; the
> scraper does NOT unwrap the outer object. Extract the bare array first:
> `jq '.cookies' /path/to/storage_state.json > /tmp/artlist_cookies.json`
> Operators WILL silently degrade to anonymous scraping if they pass a
> raw `storageState` file (the parser hits the JSON branch, fails to
> find array entries on the root object, and returns 0).

#### Format B — Netscape cookie-jar

```
# Netscape HTTP Cookie File
# https://curl.haxx.se/rfc/cookie_spec.html
.artlist.io   TRUE    /   FALSE   1798765432  session  eyJhbGciOi...
.artlist.io   TRUE    /   FALSE   1798765432  xsrf-token  abc123
```

Fields are TAB-delimited (NOT space-delimited), exactly seven per row:

```
<domain> <includeSubdomains> <path> <secure> <expires> <name> <value>
```

- `<includeSubdomains>` should be `TRUE` / `FALSE`.
- `<secure>` should be `TRUE` / `FALSE`.
- `<expires>` is unix seconds or `0` (session cookie).
- `<value>` may itself contain tabs (everything after the 6th tab is the value).
- Lines starting with `#` and blank lines are ignored.

`yt-dlp --cookies /path/to/net.txt` outputs are an accepted form of
this format.

### 1.3 Refresh cadence

- Sessions issued by Artlist expire when the upstream access token
  does; typical lifetime is **7–14 days**.
- A 401 from Artlist is the canonical "your cookie is dead" signal;
  refresh on the FIRST 401 to avoid burning upstream request quota
  (every failed request still counts against the per-IP limit).
- Atomic swap: `cat /tmp/new-cookies.txt > /tmp/artlist_cookies.tmp && \
  mv /tmp/artlist_cookies.tmp /tmp/artlist_cookies.txt`. Direct
  overwrite is unsafe — a concurrent scrape can read the file mid-write.

### 1.4 Behavior when unset / invalid / empty

The parser is **fail-closed on parse but fail-OPEN on outcome**:

| Input                       | Behavior |
|-----------------------------|----------|
| Env var unset AND default path doesn't exist | `[artlist] cookie file not found: /tmp/artlist_cookies.txt` logged; `importCookies` returns 0; scraper proceeds anonymously. |
| File is empty / whitespace-only            | Silent no-op; `importCookies` returns 0; scraper proceeds anonymously. |
| File is malformed JSON                     | `[artlist] cookie file ... looks like JSON but failed to parse` logged; `importCookies` returns 0; scraper proceeds anonymously. |
| File is JSON but contains no valid cookies | `[artlist] cookie file ... contained no valid cookies` logged; `importCookies` returns 0. |
| File is valid JSON OR Netscape with ≥1 valid entry | Sets cookies via `page.setCookie(...cookies)`; logs `[artlist] imported N cookies from /path/to/file`. |

**Silent degradation to anonymous scraping.** Requests continue
without authentication. The downstream consequence is that
premium-tier, region-locked, or login-only clips will return
`STREAM_NOT_FOUND` (see §2). The pipeline MUST treat
`STREAM_NOT_FOUND` as a hard failure rather than a phantom-clip
acceptance — see Gate 1 cross-reference below.

The parser never throws. Operators NEVER see a scraper crash because
of a bad cookie file.

### 1.5 Cross-references

- **Source-of-truth:** `src/driver/cookies.js::importCookies` and
  `::parseNetscapeCookies`.
- **Consumer side:** `src/scrape/detail-page.js::fetchClipDetails` —
  the first thing it does is `importCookies(detailPage, cookiePath)`.
- **Tests:** Operator-coverage of the graceful-degradation path lives in
  `tests/operational/artlist/03_detail_stream.sh` Gate 1 (the test runs
  with no `ARTLIST_COOKIE_FILE` set, exercising the anonymous-scraping
  branch and observing the `STREAM_NOT_FOUND` failure envelope). No
  dedicated `test/cookies.test.js` exists yet — surface that as a
  followup if cookie-format coverage is needed at the unit level.

---

## 2. `/detail` STREAM_NOT_FOUND JSON contract

When the scraper cannot resolve a clip's stream URL, `/detail` returns:

```
HTTP/1.1 200 OK
Content-Type: application/json

{
  "ok": false,
  "error": "STREAM_NOT_FOUND",
  "provider": "artlist",
  "clip_id": "123456",
  "page_url": "https://artlist.io/stock-footage/clip/skyline-at-sundown/123456",
  "clip_page_url": "https://artlist.io/stock-footage/clip/skyline-at-sundown/123456",
  "stream_urls": [],
  "raw_metadata": { ... }
}
```

> ⚠️ **HTTP 200 carries the failure.** Operators MUST NOT rely on HTTP
> status codes alone to gate pipeline success — the failure is encoded
> in the JSON body via `ok:false` + `error:"STREAM_NOT_FOUND"`. This is
> deliberate (the upstream network probes do not always surface 4xx
> reliably for paywalled content; HTTP 200 keeps the response a single
> round-trip) but it means consumers MUST parse the body.

### 2.1 Trigger conditions

The scraper emits this exact shape when ANY of the following hold
after the page-load + HLS-listen window completes (`fetchClipDetails`,
end of function):

- `stream_urls` is empty (no `.m3u8` / `.mp4` / `/manifest` / `/playlist`
  candidate surfaced across network interception, `performance.getEntries`,
  and HTML scraping).
- `primary_url` is empty.
- `primary_url` equals `clipPageUrl` itself (degenerate: just the clip
  page URL, no actual stream).

If any of the happy conditions are met, the scraper emits the
HAPPY-PATH shape (see §2.3) instead.

### 2.2 Field-by-field schema (STREAM_NOT_FOUND path)

| Field           | Type           | Description |
|-----------------|----------------|-------------|
| `ok`            | bool           | ALWAYS `false`. Clients MUST assert `ok === false` (or `== false` in shell jq) before treating as failure; this is the discriminator between happy / failure envelopes. |
| `error`         | string         | ALWAYS literal `"STREAM_NOT_FOUND"` for this error path. Future error paths use distinct literals (`AUTH_EXPIRED`, `RATE_LIMITED`, `CLOUDFLARE_BLOCK` — see §2.5). |
| `provider`      | string         | ALWAYS `"artlist"` for this scraper. Multi-tenancy tagging. |
| `clip_id`       | string\|null   | The clip slug extracted from the input URL, or `null` if extraction yielded no digits. |
| `page_url`      | string         | The exact clip page URL echoed from the request input. |
| `clip_page_url` | string         | Alias of `page_url` today; reserved for future divergence (e.g. signed-URL forms). |
| `stream_urls`   | array<string>  | ALWAYS `[]` on this path. Empty-array sentinel; check length, not truthiness, against `null`. |
| `raw_metadata`  | object         | Operator-debug payload (intercepted API responses, `__NEXT_DATA__`, JSON-LD scripts, DOM snapshot). NOT for client-side branching; safe to log. |

### 2.3 HAPPY-PATH response shape (for context, NOT the same as §2.2)

When at least one stream URL is resolved, the scraper returns:

```json
{
  "ok": true,
  "provider": "artlist",
  "clip_id": "123456",
  "title": "Skyline at Sundown",
  "description": "...",
  "creator": "John Richter",
  "country": "Spain",
  "location": "Barcelona, Spain",
  "tags": ["Skyline", "Evening", "Clouds", "Sundown"],
  "categories": ["Cities", "Travel"],
  "page_url": "https://artlist.io/stock-footage/clip/skyline-at-sundown/123456",
  "clip_page_url": "...same as page_url...",
  "thumbnail_url": "https://cdn.artlist.io/thumb/123456.jpg",
  "preview_url":   "https://cdn.artlist.io/preview/123456.mp4",
  "primary_url":   "https://cdn.artlist.io/hls/123456/master.m3u8",
  "stream_urls":   ["https://cdn.artlist.io/hls/123456/master.m3u8"],
  "raw_metadata":  { ... }
}
```

All fields are flat (no nested `clip` envelope). Operators that wrap
this response in another envelope (e.g. from the Go server) MUST
preserve the field names by either promoting them to top-level OR
nesting them under a `clip` object — clients that consume the wire
format use the `(.clip.X // .X // default)` jq alternative operator so
both shapes are forward-compatible (see Cross-references below).

### 2.4 Single-source-of-truth, no drift guarantees

`src/scrape/detail-page.js::buildResult` constructs the happy-path
shape; the STREAM_NOT_FOUND branch in the same file constructs the
failure shape. Any future field additions MUST land in BOTH branches
(today: `provider`, `clip_id`, `page_url`, `clip_page_url`,
`stream_urls`, `raw_metadata` appear in both — keep this list in sync
on PRs).

### 2.5 Forward-compat: error-literal namespace

Future scraper error envelopes MUST use distinct string literals.
Today only `STREAM_NOT_FOUND` is emitted. Reserved literals (do NOT
add until the scraper actually emits them):

- `AUTH_EXPIRED`       — session/cookie rejected upstream; refresh cookie.
- `RATE_LIMITED`       — Artlist returned 429; back off + retry.
- `CLOUDFLARE_BLOCK`   — page title contained `Just a moment`; today returned as `null`, not as an envelope.

### 2.6 Cross-references

- **Source-of-truth:** `src/scrape/detail-page.js::buildResult` +
  the `STREAM_NOT_FOUND` branch at the end of `fetchClipDetails`.
- **Enforced in:** `tests/operational/artlist/03_detail_stream.sh`
  Gate 1 (`gate_detail_stream`). The jq assertions there are the
  authoritative field-level contract — operators and reviewers MUST
  reconcile any drift against the test FIRST, then update this doc
  and the scraper to match.
- **Consumed by:** future `tests/operational/lib/artlist.sh::artlist_detail`
  (currently a stub at commit `5f21bfa15`, full implementation in a
  followup commit). The stub will be replaced with a curl call that:
  1. POSTs `{clip_page_url}` to `$SCRAPER_URL/detail`,
  2. Reads `.ok`; on `false` parses `.error`,
  3. Asserts `error == "STREAM_NOT_FOUND"` AND `stream_urls` empty,
  4. Maps the failure to the Artlist orchestrator's
     `transport_dispatch_failed_classification` outbox event.
- **Negative test fixture:** `tests/operational/artlist/03_detail_stream.sh`
  uses `https://artlist.io/stock-footage/clip/00000000` as a known-bad
  clip (deterministic ID 8 zeros) so the STREAM_NOT_FOUND branch is
  always exercised, regardless of live state.

---

## 3. Open Architectural Question — Wire-format envelope

There is a discrepancy between the scraper's NATIVE flat-JSON shape
(documented in §2.2 / §2.3) and what `tests/operational/artlist/03_detail_stream.sh`
asserts at the consumer side:

- The scraper returns `{ ok, provider, clip_id, ... , stream_urls }` flat.
- The 03_detail_stream.sh test reads `.clip.ok == true` (nested under
  `clip`) for the happy path, and falls back to flat `.clip_id` /
  `.stream_urls` for `STREAM_NOT_FOUND`.

The test uses jq's `//` alternative operator throughout (e.g.
`(.clip.page_url // .page_url // "")`), so the discrepancy resolves
as: the wrapped form MAY arrive with a nested `clip` envelope, and
MUST arrive flat on the `STREAM_NOT_FOUND` path. The wrapper that
adds the `clip` envelope (likely in the Go server's `/api/artlist/detail`
forwarding layer or in `src/server/routes.js`) is NOT documented here
because it is out of scope for the node-scraper README.

**Followup:** audit `src/server/routes.js` (and any Go server routes
that proxy `$SCRAPER_URL/detail`) to determine the exact wire-format
contract. Document the wrapper in a followup commit either way
(this README if the wrapper lives in node-scraper, or the Go server's
contract doc otherwise).

---

## 4. Future operator contracts to add

- `ARTLIST_REQUEST_TIMEOUT_MS` (currently `DEFAULT_NAV_TIMEOUT=60000`,
  inline constant in detail-page.js) — should be promoted to an env
  var.
- `ARTLIST_USER_AGENT` (currently hard-coded Chrome/124 string) —
  likewise, some operators rotate user-agents when scraping at scale.
- `ARTLIST_BROWSER_PROFILE_DIR` (today: Puppeteer launches with a
  fresh profile every time) — operators running multi-tenant scrapes
  may want to share a profile + cookie store across processes.

Until these are promoted to env vars, audit the
`src/scrape/detail-page.js::fetchClipDetails` function for the
inline constant that's currently in force.
