# BrainRust ICU runtime ABI

`BrainRust/ICU_MAJOR` is the single ABI pin for the Rust executables consumed by
PipelineGen. `make build-server` reads that pin; it does not infer the release
ABI from `pkg-config`, which reports the development headers and can differ from
the installed runtime libraries. Before installing release binaries,
`scripts/ci/rust-fetch.sh` checks that the dynamic loader can resolve both ICU
sonames for the pinned major.

The pinned BrainRust release uses ICU 72. Ubuntu 22.04 ships ICU 70 by default.
The versioned ICU 72 runtime can coexist with ICU 70, so Go builds and Rust
release binaries can use their intended ABIs without replacing Jammy's system
ICU or its development headers. Provision it once on an amd64 Jammy host with:

```sh
scripts/deploy/install_icu72_runtime.sh
```

The installer downloads the Debian Bookworm `libicu72` runtime package over
HTTPS, verifies its pinned SHA-256, installs only that versioned runtime
package, and checks both loader entries. Its runtime dependencies are satisfied
on this host (`libc6` 2.35 and `libstdc++6` 12.3). It does not replace
`libicu70` or `libicu-dev`.

After provisioning, normal `make build-server` and `make build` use the
canonical BrainRust release bundle and pass its ABI check. `pkg-config` may
still report ICU 70; that is expected for local Rust source builds using
Jammy's development headers. BrainRust release artifacts remain built and
published against ICU 72 as declared in its `ICU_MAJOR` file.

## Current release pin: v0.3.8

`v0.1.0` is retired for PipelineGen: its `semantic-engine` predates the
`semantic_manifest.v2` fields (`contextual_image_queries`,
`composed_image_queries`, `important_segments`) and `rust-fetch` now rejects
it. `v0.2.0` was the first mediaexec.v2 pin. The current pin is `v0.3.8`, built
from BrainRust HEAD (`a4ab4cb65af0fffd93966f27738ae58626fce2a9`) in Debian
Bookworm with ICU 72. It adds the standalone-CI fixture guard, the
CAP_SYS_ADMIN no-network gate, the mediaexec.v2 validation and the
`safe.directory` needed by the release build; the render parity test skips
only when ffmpeg is absent. The runtime wire contract is unchanged from
`v0.3.0`.

## Building the worktree binary (local verification)

`make build-server` deliberately consumes the checksum-verified release, so a
change in the BrainRust worktree never reaches `bin/semantic-engine` through the
deploy path. Use one command for local verification:

```sh
make build-rust-local      # cargo build --release -p semantic-engine + install + stamp
make verify-rust-binary    # fail if the manifest, HEAD and the binary disagree
```

`build-rust-local` rewrites `bin/BRAINRUST_RELEASE.json` and
`bin/.rust-release` with the real `git_sha`, the SHA-256 of the installed
binary, the dirty flag and the build time. `verify-rust-binary` is read-only
(no cargo) and fails when a Rust source is newer than the binary.
`pipelinegen_deploy.sh` runs the same check before the build: it fails closed
when a BrainRust checkout is present, and can be skipped explicitly with
`DEPLOY_SKIP_RUST_FRESHNESS=1` for a release-only host.

The locally built staging bundle is `BrainRust/dist-icu72/`; install it with:

```sh
make rust-fetch RUST_ARTIFACTS_DIR="$PWD/../BrainRust/dist-icu72"
```

The same bundle is published as the GitHub `v0.3.5` release before a clean
`make build` or Docker build can fetch it remotely. Verify the installed stamp
and linkage with:

```sh
cat bin/.rust-release
ldd bin/semantic-engine | grep 'libicuuc.so.72'
```
