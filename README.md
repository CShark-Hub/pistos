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

## Module path

The Go module path `example.com/pistos` is a placeholder until the publishing identity is decided.

## License

Apache-2.0. Third-party analyzers keep their own licenses; see [docs/third-party.md](docs/third-party.md).
