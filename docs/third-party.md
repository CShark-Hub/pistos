# Third-party components

Pistos does not redistribute analyzers. Packs locate tools that the repository already uses or that
`pistos plugins install` acquires explicitly from their upstream distribution channel.

## Go module dependencies (compiled into the core)

| Module | Version | License |
|---|---|---|
| github.com/spf13/cobra | v1.10.2 | Apache-2.0 |
| github.com/spf13/pflag | v1.0.9 | BSD-3-Clause |
| github.com/inconshreveable/mousetrap | v1.1.0 | Apache-2.0 (Windows only) |
| github.com/BurntSushi/toml | v1.6.0 | MIT |
| go.yaml.in/yaml/v3 | v3.0.5 | MIT and Apache-2.0 |
| golang.org/x/sync | v0.23.0 | BSD-3-Clause |

## Analyzers invoked by packs (not bundled)

| Tool | Pack | License | Acquisition |
|---|---|---|---|
| Go toolchain (`go vet`, compiler) | go | BSD-3-Clause | user-installed toolchain |
| Staticcheck | go | MIT | `go install` by `pistos plugins install` |
