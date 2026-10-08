# Go pack (`go`, version 0.1.0)

Verified with Go 1.26.9 and 1.27.2 (linux/arm64) and Staticcheck 0.8.1.

## Checks

| Check | Command (in the project root of the snapshot) | Scope | Output | Exit codes |
|---|---|---|---|---|
| `go-vet` | `go vet -p=N -json [-tags=…] ./...` | project | `go-vet` | 0 and 1 are recognized; other codes fail |
| `staticcheck` | `staticcheck -f json [-tags=…] ./...` | project | `staticcheck-json` | 0 clean, 1 findings; other codes fail |

`go vet -json` interleaves `# pkg` headers, `vet: file:line:col: msg` type errors, package-load errors
and per-package JSON objects. Go 1.26 prints the JSON on stdout and Go 1.27 on stderr; the parser reads
both. Type errors become `go/typecheck` findings (severity error); load errors become `go/load` findings
and mark the check **incomplete** because vet stops loading.

## Environment

Analyzers run with `GOFLAGS=-mod=readonly`, `GOPROXY=off` and `GOTOOLCHAIN=local`: a review never
downloads modules or toolchains. Missing modules surface as `go/load` findings with an incomplete
status; run `go mod download` yourself. Options: `tags`, `goos`, `goarch`, `cgo_enabled`
(`[[projects]] options = { tags = "integration" }`).

## Tools and acquisition

- `go`: found on `PATH`; never acquired.
- `staticcheck`: managed copy (`pistos plugins install` runs `go install honnef.co/go/tools/cmd/staticcheck@v0.8.1`
  into the pistos cache) or `PATH`.

Staticcheck 0.8.1 cannot read Go 1.27 export data ("export data version 5 is greater than maximum
supported version 4"). The pack detects this and reports the `staticcheck` check as **incomplete**
instead of clean. Use Go ≤ 1.26 with Staticcheck 0.8.1 or drop `staticcheck` from the project checks.

## Deduplication

`staticcheck/SA5009` is an alias of `go-vet/printf`; `staticcheck/compile` and `go-vet/typecheck` map
to `go/typecheck`. Authority order: `go-vet`, then `staticcheck`. Merged findings keep every source.

## Identity

The enclosing symbol is the function or `Receiver.Method` from the Go AST.

## Not supported yet

golangci-lint configuration import, gopls facts (milestone 5), per-package incremental caching
(the cache key covers the whole project subtree).
