# Delivery certification — 2026-11-12

This file is the single authoritative record for whether the two referenced
commesse can be called delivered. A job state of `SUCCEEDED` is not a delivery
certificate; the artifact, its probe facts, its frame checks and the code/test
identities must all be coherent.

The machine gate is `RenderingGen/tools/verify_delivery_manifest.py`. It requires
a real artifact URI and SHA-256, positive duration/geometry/FPS/frame count, the
`first`/`middle`/`last` frame hashes, the PipelineGen and RenderingGen SHAs, a
non-empty test list with no `FAIL`, and an explicit approval. A manifest with a
zero frame count, a malformed hash or a missing frame check cannot pass.

## Garlasco — r151

Evidence present in the repository:

- `ops/jobs/garlasco_editorial_e2e_20261030/r151-overlay-assets/r151-overlay-assets-manifest.json`
  records 24 rendered overlay assets, each with a SHA-256, byte count, duration
  and frame count.
- The same manifest records the remote master final job
  `job_a1104e88eb05a8de` with status `SUCCEEDED`, attempt 1, SHA-256
  `e877d90423c0f5a6bf7fdd80964e18165386b5a7bb2d16173e5eeef52adbdfed` and an
  artifact download URI.
- The local r151 directory contains the 24 overlay MP4s, not the assembled
  master MP4.

Verdict: **NOT LOCALLY CERTIFIED.** The remote final artifact is recorded but
was not fetched or frame-checked on this host. A canonical delivery manifest can
be issued only after downloading that exact SHA, probing it, extracting the
first/middle/last frames, recording the two repository SHAs and the test
results, and obtaining an explicit approval. Do not change the manifest status
to `SUCCEEDED` from the job row alone.

## Francis Kaufmann — r12

Evidence present in the repository:

- `refactored/ops/jobs/francis_kaufmann_20261111/r12-submit-poll.log` records
  the submitted job `job_1794423905238121539_d1806353` in `QUEUED`.
- `job-full.latest.json` records the latest observed state as `CANCELLED` with
  stage `job_cancelled`.
- The job directory contains no final MP4.

Verdict: **NOT DELIVERED.** There is no artifact to certify. r12 must not be
described as a successful delivery, and a new render identity must not be
created merely to mask the cancelled state. Reconcile the job first; only an
explicit new-generation decision may create a new logical job.

## How to certify a future delivery

```bash
# 1. Probe and hash the exact artifact.
ffprobe -v error -show_streams -show_format -of json ARTIFACT.mp4
sha256sum ARTIFACT.mp4

# 2. Extract first, middle and last frames; hash each PNG.
ffmpeg -v error -i ARTIFACT.mp4 -vf "select='eq(n,0)'" -frames:v 1 first.png
ffmpeg -v error -i ARTIFACT.mp4 -vf "select='eq(n\,MID)'" -frames:v 1 middle.png
ffmpeg -v error -i ARTIFACT.mp4 -vf "select='eq(n\,LAST)'" -frames:v 1 last.png
sha256sum first.png middle.png last.png

# 3. Fill the canonical manifest fields listed in
#    RenderingGen/tools/verify_delivery_manifest.py, then run the gate.
python3 RenderingGen/tools/verify_delivery_manifest.py MANIFEST.json
```

The verifier must exit 0 before any status is reported as `SUCCEEDED`.
