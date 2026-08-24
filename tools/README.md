# Repository tools

These programs are developer and verification tools, not deployed PipelineGen
processes:

- `golden06/` generates the RenderingGen GOLDEN 06 fixture.
- `overlaytimings/` measures overlay compilation timing.
- `researchlive/` runs the live multi-provider research diagnostic.

Run them with `go run ./tools/<name>`. Runtime entrypoints remain under
`cmd/server`, `cmd/worker`, and `cmd/admin`.
