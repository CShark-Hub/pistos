# Pistos

A fast, local-first CLI that reviews code changes with deterministic tools (compilers, type checkers,
linters) and optional bring-your-own-key AI discovery. Deterministic checks decide the gate; AI
findings are advisory. Human decisions (`pistos tag 47 irrelevant`) persist in a committed
`.pistos/feedback.yaml` across diffs, commits, cache deletion and model changes.

> Status: under active development. See [docs/CHECKLIST.md](docs/CHECKLIST.md) for what is implemented
> and verified, and [docs/PLAN.md](docs/PLAN.md) for the full specification.

## Quick start

```sh
go build -o pistos ./cmd/pistos
pistos init                  # detect projects, write .pistos/config.toml (never installs anything)
pistos plugins install       # explicit tool acquisition; records versions/checksums in .pistos/lock.json
pistos review                # working tree (staged, unstaged, untracked) against HEAD
pistos review --base main    # against merge-base(main, HEAD)
pistos tag 3 irrelevant      # persist a decision; the issue stays suppressed in future reviews
```

Exit codes: `0` clean gate, `1` blocking findings, `2` configuration/execution error or incomplete
required checks, `130` cancelled.

Language selection accepts aliases: `go`/`golang`, `py`/`python`, `js`/`ts`/`javascript`/`typescript`,
`cs`/`csharp`/`"c#"` (quote `c#`, otherwise most shells treat `#` as a comment).

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
