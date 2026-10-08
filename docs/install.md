# Installation

## Core binary

Pistos is one static binary (no CGo). Build from source with Go 1.26+:

```sh
go build -trimpath -o pistos ./cmd/pistos
```

or produce release archives for Linux, macOS and Windows (amd64/arm64) with checksums:

```sh
./scripts/release.sh v0.1.0      # → dist/pistos-v0.1.0-<os>-<arch>.tar.gz and dist/SHA256SUMS
```

Nothing is published automatically; `.github/workflows/release.yml` only uploads workflow artifacts.

## Per-repository setup

```sh
pistos init                     # writes .pistos/config.toml; never downloads anything
pistos plugins install          # locate or acquire pinned tools and write .pistos/lock.json
pistos plugins install --lsp    # also language servers for `pistos facts`
pistos doctor                   # verify tools against the lock
```

Commit `.pistos/config.toml`, `.pistos/lock.json` and `.pistos/feedback.yaml`. In CI use
`pistos plugins install --locked`, which fails instead of changing the lock. The lock records one
toolchain per platform: a repository that reviews natively on Linux and inside images on Linux needs
to pick one environment for that platform.

Runtimes per pack (native mode): Go toolchain (Go pack); Python 3 and Node.js (Python pack, Node for
Pyright); Node.js and the repository's installed `node_modules` (JS/TS pack); .NET SDK 8+ (C# pack).
Only packs of affected projects are started.

## Docker execution

```toml
[execution]
mode = "docker"
memory_mb = 4096

[execution.docker]
profile = "restricted"   # or "trusted"
```

`pistos plugins install` then builds one image per active pack (`pistos-pack-<pack>:<pack version>`)
from Dockerfiles embedded in pistos, with base images pinned by digest, and locks the image ID. Build
a single image explicitly with `pistos plugins image go`. Each image contains only its pack's runtimes
(verified by tests: the Go image has no Node, Python or .NET, and so on). A rebuilt image has a new ID
and is reported as a changed tool until `plugins install` is re-run.

The host core runs every analyzer and facts worker as `docker run --rm -i` over stdio. The snapshot is
mounted read-only and scratch read-write at their host paths, so results need no path translation.
Installed dependencies are mounted read-only: `node_modules`, the Go module cache, the NuGet package
folder. In docker mode the host Python virtualenv is not used; imports resolve against the image's
Python. Language-server facts (`pistos facts`) run natively only.

## Complete images (CI without Docker-in-Docker)

`docker/full.Dockerfile` adds the pistos binary and git to a pack image, so CI can run the core and the
tools directly in one container — no Docker socket and no Docker-in-Docker:

```sh
pistos plugins image go
CGO_ENABLED=0 GOOS=linux go build -o ctx/pistos ./cmd/pistos
docker build -f docker/full.Dockerfile --build-arg PACK=go -t pistos-go:dev ctx
docker run --rm --network none -u "$(id -u):$(id -g)" -e HOME=/tmp -v "$PWD:$PWD" -w "$PWD" pistos-go:dev review --base origin/main
```

Inside the image (`PISTOS_IN_IMAGE=1`) tools resolve at the paths the pack image installed. The test
suite builds this image and checks that a review inside a `--network none` container matches the
native result.
