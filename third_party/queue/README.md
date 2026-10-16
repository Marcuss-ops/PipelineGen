# Vendored `github.com/Marcuss-ops/RenderingGen/queue`

Provenance: byte-for-byte copy of `RenderingGen/queue` (the RenderginGen
repository, `queue/` Go module) at commit `aebda34` ("Sync overlay contract,
entity sequence camera and queue changes"), including `Client.Retry`.

## Why this copy exists (BUILD-01)

The upstream module path `github.com/Marcuss-ops/RenderingGen/queue` is **not
resolvable**: GitHub serves the repository as `Marcuss-ops/RenderginGen`
(different spelling), so `go mod download` for the declared module path can
never succeed (`https://github.com/Marcuss-ops/RenderingGen` returns 404 and
the Go module proxy has no cached copy). The former
`replace … => ../RenderingGen/queue` therefore forced every clean PipelineGen
checkout, Docker build and CI run to materialise a second repository at a
hard-coded sibling path before anything could compile.

Vendoring the module inside PipelineGen makes `go mod download`,
`go build ./...`, `go vet ./...` and `make verify-main` work in an isolated
checkout with no cross-repo layout assumptions.

## Sync procedure

When the queue contract changes upstream:

```sh
# from the PipelineGen repository root:
rsync -a --delete --exclude README.md ../RenderingGen/queue/ third_party/queue/
go mod tidy
```

`--exclude README.md` keeps this file in place. After syncing, run the package
tests of `internal/platform/renderinggen/`.

## Exit path

Once the queue module is published under a resolvable module path (repository
rename alignment or a dedicated module repository) with a versioned reference
that contains `Client.Retry`, delete this directory, drop the `replace`
directive in `go.mod`, and pin the published version in `require`.
