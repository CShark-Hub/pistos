# Pistos

A fast, local-first CLI that reviews code changes with deterministic tools (compilers, type checkers,
linters) and optional bring-your-own-key AI discovery. Deterministic checks decide the gate; AI
findings are advisory. Human decisions (`pistos tag 47 irrelevant`) persist in a committed
`.pistos/feedback.yaml` across diffs, commits, cache deletion and model changes.

> Status: milestones 1–8 implemented and verified on Linux arm64; CI on macOS/Windows not yet run.
> See [docs/CHECKLIST.md](docs/CHECKLIST.md), [docs/release-readiness.md](docs/release-readiness.md),
> [docs/limitations.md](docs/limitations.md) and the [tutorial](docs/tutorial.md).

## Quick start

```sh
go install ./cmd/pistos       # installs to $(go env GOPATH)/bin; make sure that directory is on PATH
pistos init                  # detect projects, write .pistos/config.toml (never installs anything)
pistos plugins install       # explicit tool acquisition; records versions/checksums in .pistos/lock.json
pistos review                # working tree (staged, unstaged, untracked) against HEAD
pistos review --base main    # against merge-base(main, HEAD)
pistos tag 3 irrelevant      # persist a decision; the issue stays suppressed in future reviews
pistos report                # browse the report with code excerpts; select issues to ignore and save
pistos ai setup              # optional: connect an OpenAI-compatible endpoint or OpenRouter for AI review
```

Every review also writes `.pistos/state/report.html` and prints its link with the command that opens
it on this machine (`--no-html` skips it).

Alternatively, `go build -o pistos ./cmd/pistos` builds into the current directory; then invoke it as `./pistos`.

Exit codes: `0` clean gate, `1` blocking findings, `2` configuration/execution error or incomplete
required checks, `130` cancelled.

Language selection accepts aliases: `go`/`golang`, `py`/`python`, `js`/`ts`/`javascript`/`typescript`,
`cs`/`csharp`/`"c#"` (quote `c#`, otherwise most shells treat `#` as a comment).

To limit a project to part of its root, add globs relative to `root` in its `[[projects]]` entry:

```toml
[[projects]]
id = "svc"
root = "services/svc"
ecosystem = "go"
checks = ["go-vet", "staticcheck"]
include = ["cmd", "internal/**"]          # optional; only these paths belong to the project
exclude = ["internal/gen", "**/*_mock.go"] # optional; applied after include
```

`*` stays within a path segment, `**` spans segments, and a glob naming a directory covers everything
below it. Changes only in left-out paths do not trigger the project's checks, findings there are
dropped (the coverage line says how many) and they are not sent to AI. Tools still analyze the whole
project, so excluded code can still affect type checking of included code.

## Language packs

| Pack | Checks | Notes |
|---|---|---|
| Go | `go vet`, Staticcheck | [docs/packs/go.md](docs/packs/go.md) |
| Python | Ruff, Pyright / basedpyright | [docs/packs/python.md](docs/packs/python.md) |
| JS/TS | tsc (repository version), ESLint, Oxlint | [docs/packs/jsts.md](docs/packs/jsts.md) |
| C# | `dotnet build` + Roslyn analyzers (SARIF) | [docs/packs/csharp.md](docs/packs/csharp.md) |

Packs activate only for projects affected by a change; tools are locked in `.pistos/lock.json` and
never installed or upgraded by `review`. Decisions are documented in [docs/feedback.md](docs/feedback.md).

Every pack also offers `metrics` (function complexity with baseline deltas, [docs/metrics.md](docs/metrics.md))
and `imports` (dependency cycles and layer rules, [docs/rules.md](docs/rules.md)) checks, and on-demand
semantic queries through language servers: `pistos facts references svc/main.go:12:9`
([docs/lsp.md](docs/lsp.md)).

Optional BYOK AI review (`--profile thorough`) runs independent discovery passes against any
OpenAI-compatible endpoint, unions and triages their candidates and publishes advisory issues with
provenance; see [docs/ai.md](docs/ai.md).

Output formats: text, JSON, canonical JSON and SARIF 2.1.0. Analyzers can run natively or in pinned
per-pack containers (`execution.mode = "docker"`, restricted profile for untrusted code); see
[docs/install.md](docs/install.md) and [docs/security.md](docs/security.md).

## Module path

The Go module path `example.com/pistos` is a placeholder until the publishing identity is decided.

## License

Apache-2.0. Third-party analyzers keep their own licenses; see [docs/third-party.md](docs/third-party.md).
