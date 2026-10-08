# Architecture

Pistos is a single Go binary (no CGo) that orchestrates external analyzers. It keeps a strict split
between the **deterministic lane** (compilers, type checkers, linters — these decide the gate) and the
**AI lane** (advisory discoveries, never blocking by default).

## Review flow

```
snapshot (git trees) → projects → check plan → cache lookup → run tools (bounded)
  → normalize + identity → merge/dedupe → baseline diff → [AI discovery/triage]
  → feedback matching → verdict → report + state
```

| Stage | Package | Notes |
|---|---|---|
| CLI | `internal/cli` | Cobra commands; exit codes 0/1/2/130 |
| Configuration | `internal/config` | `config.toml` (strict, unknown keys rejected), `lock.json`, init |
| Project detection | `internal/detect` | Marker files only; never crawls vendor/build/dependency dirs |
| Snapshots | `internal/snapshot` | Git trees for base and head; working snapshot via a temporary index |
| Packs and tools | `internal/plugin`, `internal/packs` | Embedded manifests, tool resolution against the lock, declarative checks, worker protocol |
| Processes | `internal/runner` | Process groups, output caps, timeouts, tree kill, weighted scheduling |
| Models | `internal/finding` | Finding/Issue/Candidate/Report, identity keys, canonical JSON |
| Feedback | `internal/feedback` | `feedback.yaml` load/validate/mutate/match |
| Local state | `internal/store` | Content-addressed result cache, issue ledger, current report |
| Orchestration | `internal/review` | Glues the stages; computes the verdict |
| Output | `internal/report` | Text and JSON renderers |

## Snapshots and isolation

Every review compares two git **trees**. A commit head uses its tree directly. A working-snapshot head is
built by copying the index to a temporary file, running `git add -A` against that temporary index and
`git write-tree`: staged, unstaged and untracked (non-ignored) files are captured without touching the
user's index or working tree (git only gains unreferenced objects, which `git gc` prunes). Each tree is
materialized with `git archive` into `.pistos/cache/work/<tree>/`, so analyzers never see edits made
while they run, and baseline and head outputs never share a directory.

## Identity

A finding's key is `k1-` + hash of: project ID, problem family (tool rule mapped through declared
aliases), repository-relative path, enclosing symbol, hash of the whitespace-normalized anchor line,
and an ordinal among identical anchors. Line numbers, messages, severities, model names and commit IDs
are deliberately excluded. Current evidence is tracked separately in `evidence_hash`.

## Packs

A pack is a versioned manifest (`internal/packs/<name>/manifest.json`) declaring tools, checks, output
formats, exit-code meanings, allowed options/environment and authority order. Manifests are data:
reading them starts nothing. Checks are either declarative wrappers (args template + structured output
parser) or out-of-process workers speaking newline-delimited JSON-RPC 2.0 (`docs/worker-protocol.md`).

Analyzer processes receive an explicit environment built from an allowlist; the parent environment
(including any provider API keys) is never inherited.

## Verdict

Only completed deterministic capabilities determine the gate. A required capability that failed, timed
out or was unsupported makes the review exit 2 even if findings were also produced (they are still
reported). AI coverage is reported separately and never changes the deterministic outcome.
