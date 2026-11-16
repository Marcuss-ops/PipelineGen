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

## Current release pin: v0.3.2

`v0.1.0` is retired for PipelineGen: its `semantic-engine` predates the
`semantic_manifest.v2` fields (`contextual_image_queries`,
`composed_image_queries`, `important_segments`) and `rust-fetch` now rejects
it. `v0.2.0` was the first mediaexec.v2 pin. The current pin is `v0.3.2`, built
from BrainRust HEAD (`ba2f86ad7fac75e10957d49843913bd6e64f9332`) in Debian
Bookworm with ICU 72. It adds the standalone-CI fixture guard, the ffmpeg
install for the render-segment parity gate, and the phase-2 demolition
tickets; the runtime wire contract is unchanged from `v0.3.0`.

The locally built staging bundle is `BrainRust/dist-icu72/`; install it with:

```sh
make rust-fetch RUST_ARTIFACTS_DIR="$PWD/../BrainRust/dist-icu72"
```

The same bundle is published as the GitHub `v0.3.2` release before a clean
`make build` or Docker build can fetch it remotely. Verify the installed stamp
and linkage with:

```sh
cat bin/.rust-release
ldd bin/semantic-engine | grep 'libicuuc.so.72'
```
