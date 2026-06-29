# PipelineGen — Archived Wave History (June 2026)

Frozen audit-history archive for `architecture/current.yaml`, established by
`chore(arch): action P0-5 of cleanup plan slice 1/4` (June 2026).

## Layout

- `INDEX.md` — this file.
- `current-snapshot-2026-06-29.yaml` — verbatim frozen snapshot of the
  pre-refactor `architecture/current.yaml` (158,887 bytes; 2,401 lines;
  multi-document YAML stream with 3 docs: wave sequence + post_cascade
  followups + legacy_fallback_cleanup + audit summary). This is the
  canonical pre-P0-5 byte-equivalent; anything not visible here was added
  after 2026-06-29.

## Future audit extractions

Following slices (P0-5 slice 3/4 = trim-current.yaml) splits the snapshot
into per-wave files under `wave-<N>.yaml`, preserving the exact
historical text per wave. Until that lands, the snapshot is the
single source of truth for "what did pre-refactor current.yaml say".

## Restoration

The snapshot is byte-identical to `architecture/current.yaml` as it
existed on `main` HEAD at commit `17ad323b` (ci: invoke fail-closed
verify-main as blocking gate). To restore the legacy schema verbatim:

```
cp architecture/archive/2026-06-29/current-snapshot-2026-06-29.yaml \
   architecture/current.yaml
```

Per AGENTS.md Git-Lesson-1/2/3 audit discipline, this commit ships:
  - Subject verbatim: `chore(arch): action P0-5 of cleanup plan slice 1/4`
  - Co-authored-by trailer (PipelineGen Agent)
  - Direct-to-main (NO BRANCH) per Git-Lesson-2
  - Fast-forward push (NO --force, NO --force-with-lease)
