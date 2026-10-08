# Implementation checklist

Status legend: `[x]` delivered and tested, `[~]` partial (see notes), `[ ]` not started.
Placeholder adapters and mock-only tests are never marked as real language support.

Environment used for verification: Linux arm64, Go 1.27.2 (installed to `~/sdk/go`).

## Milestone 1 — scaffold, contracts and fixture infrastructure

Scope: CLI skeleton, config/lock/feedback schemas, typed models, canonical JSON, fixtures, fake
workers, fake HTTP endpoints, CI, architecture document.
Planned tests: config validation/rejection, init idempotency, canonical stability, identity
independence from line/message/tool, worker protocol failures, process tree cleanup.

- [x] CLI skeleton (`init`, `doctor`, `--help`) — works without Node/Python/.NET
- [x] `config.toml` schema with strict unknown-key rejection, schema version check, secret-key hint
- [x] `lock.json` model (schema/protocol versions, per-platform checksums)
- [x] `feedback.yaml` skeleton written by init (full schema in M3)
- [x] Typed models: Project, InputManifest, CheckPlan/Task, Finding, Issue, Candidate, CapabilityResult, Report/Envelope
- [x] Canonical JSON with stable ordering; volatile data in a separate envelope
- [x] Fixture repositories (`testdata/fixtures`) and git repo test helper
- [x] Fake worker (re-executed test binary) and fake OpenAI-compatible HTTP endpoint
- [x] Worker protocol client: NDJSON JSON-RPC, bounded frames, version check, crash/malformed/cancel
- [x] Process supervision: process groups, output caps, timeout, tree kill, descendant reaping (Unix tested)
- [x] CI workflow (build without CGo, vet, test, race on Linux, Linux/macOS/Windows matrix) — defined, not run here
- [x] Architecture document

Limitations: Windows process-tree handling uses `taskkill /T` and is compile-checked only; it has not
been executed on Windows in this environment.

## Milestone 2 — plugin runner and Go vertical slice

Scope: snapshot/baseline handling, capability planning, lazy manifests, process supervision,
bounded scheduling, native execution, Go checks, cache/provenance, text/JSON output, verdicts.
Planned tests: known diagnostic, clean change, pre-existing vs new, no-change scope, commit head vs
dirty tree, determinism across runs and job counts, cache reuse/invalidation, unaffected projects,
broken/upgraded/missing tools, cancellation cleanup, worker-backed checks, parser fixtures.

- [x] Working snapshot via temporary index (staged + unstaged + untracked, ignores respected), user index untouched
- [x] `--base` uses merge-base; `--head` requires `--base`; commit review ignores dirty files; refs and trees recorded
- [x] Isolated materialization of each tree outside the repository (`$PISTOS_CACHE_DIR` or user cache)
- [x] Embedded pack manifests (strict schema); reading starts no process
- [x] Tool lookup (repo/managed/path), version probing, identity = file hash + version, lock verification
- [x] `plugins install [--locked] [--offline]` with `go install` acquisition; `doctor` tool status
- [x] Declarative checks: argument arrays, option templates, allowlisted environment (no secrets)
- [x] Worker-backed checks with scope echo verification
- [x] Weighted job budget (`golang.org/x/sync/semaphore`); `{jobs}` passed to tools; never stops early
- [x] Go pack: `go vet` (both Go 1.26/1.27 output layouts), Staticcheck JSON, alias-based dedupe with authority
- [x] Result cache keyed by project subtree, shared inputs, references, tool identity, args, env, options
- [x] Only completed results are cached; cache hits report `cached-complete`
- [x] Baseline comparison: new / existing / unknown (baseline incomplete) / in-scope (`--all`)
- [x] Verdict and exit codes 0/1/2/130; incomplete required checks take precedence
- [x] Text, JSON and canonical JSON output; `Reproduce` command per finding
- [x] Live tests with real Go (1.27.2); Staticcheck verified live with Go 1.26.9 (merge) and Go 1.27.2 (explicit incompatibility)

Limitations:
- Cache granularity is the project subtree, not per package; dependencies outside the project root
  are covered only through declared `references` and pack shared inputs.
- Windows process-tree cleanup is implemented with `taskkill /T` but not executed here.
- Go modules missing from the module cache yield an incomplete result; pistos never downloads them.

## Milestone 3 — persistent feedback and issue ledger

Scope: short tag/reopen commands, atomic feedback mutation, current-report references, ordinary and
decision tags, strong matching, cross-diff persistence, minimal interactive feedback view.
Planned tests: persistence across unrelated changes, line shifts, reformatting, cache/state deletion,
different base/head and `--all`; verified renames; changed cause and nearby defect; reopen; labels and
deferred; test-only guard; pattern scope preview/confirmation; invalid manual edits; base-policy mode;
strict references; interactive view; concurrent mutation; AI-shaped findings.

- [x] `feedback.yaml` schema 1 with strict validation, unversioned-file migration, committed sample
- [x] Locked, atomic, comment-preserving mutation (YAML node patching); invalid files never overwritten
- [x] Issue ledger with stable local numbers; current report stored for short references
- [x] `pistos tag N TAG...` (`--reason`, `--scope pattern --paths --yes` with preview), canonical keys accepted
- [x] `pistos reopen`, `pistos reproduce`, `pistos feedback` (list/show/tag/untag/reason/pattern/delete)
- [x] Matching: identity, verified rename (blob or git rename from origin tree/commit), pattern scope
- [x] Guards (`test-only` → test-path guard) and revalidation for changed anchors; nothing hidden on weak evidence
- [x] Tag/reopen refilter the stored report without running tools
- [x] `--feedback-from-base` for untrusted changes
- [x] AI-shaped findings: rewording/model/line changes keep decisions; other defects stay visible
- [x] Fixed a real racy-git bug: the temporary index now keeps the original mtime (regression test)

Limitations:
- Symbols are derived for Go (AST) and Python (indentation); other languages fall back to
  path + anchor line + ordinal identity.
- Two identical anchor lines in one symbol are distinguished only by order (ordinal).
- Rename verification needs either an unchanged file blob or a locally available origin tree/commit.
