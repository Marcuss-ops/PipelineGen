# Vendored `github.com/Marcuss-ops/RenderingGen/queue`

Provenance: byte-for-byte copy of `RenderingGen/queue` at commit `cd9749a`
("fix(media): pin avcC profiles and certify segment output facts"). `README.md`
is the upstream documentation (the tests read it to pin the live event and job
state vocabulary); this file is the vendoring note and is the only local
addition.

## Why this copy exists (BUILD-01)

The upstream module path `github.com/Marcuss-ops/RenderingGen/queue` is **not
resolvable**: GitHub serves the repository under a differently spelled name,
so `go mod download` for the declared module path can
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
rsync -a --delete --exclude VENDOR.md ../RenderingGen/queue/ third_party/queue/

# Host fixtures the upstream queue tests read from the repository that owns
# the module (queueRoot/.. in the upstream layout).
mkdir -p third_party/contracts third_party/infra/e2e
cp ../RenderingGen/contracts/renderinggen.job.v1.schema.json third_party/contracts/
cp ../RenderingGen/infra/e2e/golden-overlay-verify.sh third_party/infra/e2e/
cp ../RenderingGen/README.md third_party/README.md

go mod tidy
```

`--exclude VENDOR.md` keeps this note. The three host fixtures are required by
`client/contract_test.go` and `internal/repository/postgres/events_vocabulary_test.go`,
which resolve their pinned documents from the parent of the module root. Verify
the copy with:

```sh
python3 ops/tools/check_queue_drift.py --tests
```

The checker compares every file byte-for-byte, compares the three fixtures, and
optionally runs `go test ./...` in both modules. A drift is a build failure of
the checker, not a surprise at claim time.

## Exit path

Once the queue module is published under a resolvable module path (repository
rename alignment or a dedicated module repository) with a versioned reference
that contains `Client.Retry`, delete this directory, drop the `replace`
directive in `go.mod`, and pin the published version in `require`.
