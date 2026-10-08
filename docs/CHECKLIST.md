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

## Milestone 4 — JS/TS, Python and C# packs

Scope: real deterministic adapters, installer/lock handling and runtime/backend documentation for
Python, JS/TS and C#; clean/defective/multi-project fixtures; structured-output normalization tests.
Planned tests: live findings and clean results per pack, configured backend/rule preservation,
missing/unsupported tools, mixed-language activation, C# multi-project, multi-target, generated
sources and no-op rebuild; parser tests from captured outputs.

Verified live (Linux arm64): Python 3.12.3 + Ruff 0.16.10 + Pyright 1.1.414 on Node 24.21.0;
TypeScript 7.0.2, ESLint 10.12.0, Oxlint 1.87.0; .NET SDK 10.0.401 (net10.0 and net8.0 targets);
Go 1.26.9 + Staticcheck 0.8.1. Command: `PISTOS_REQUIRE_LIVE=1 go test -race -count=1 ./...` — all pass, no skips.

- [x] Python pack: ruff (repo venv or managed pip), pyright/basedpyright (repo or managed npm, run via node), interpreter detection, site-packages fingerprint
- [x] Python: ty/mypy selection preserved by init and reported `unsupported` (not substituted)
- [x] JS/TS pack: repository-selected tsc (TypeScript 7 native verified), ESLint, Oxlint (repo or managed); Biome reported unsupported
- [x] JS/TS: repository `node_modules` linked into snapshots (race-safe); lockfile/listing fingerprint in cache key
- [x] C# pack: `dotnet build` with per-project/per-TFM SARIF via injected props, per-task artifacts path, `--no-incremental`, local-only restore
- [x] Generic filters: other-project diagnostics, generated/untracked files, duplicate multi-TFM diagnostics
- [x] Acquisition methods `pip` and `npm` (`--ignore-scripts`); node-package lookup via package `bin` entries
- [x] Unsupported checks skipped by install/doctor with explanation, reported as `unsupported` in review
- [x] Live tests: findings + clean results (Python), config preservation (ruff extend-select, tsconfig strict, .editorconfig CA1822), ESLint+Oxlint merge, missing repo TypeScript, C# multi-project attribution, multi-target, no-op rebuild, mixed-repo activation
- [x] Parser tests from captured outputs, incl. sandboxed-ruff silent clean, tsc project errors, SARIF suppressions/unanchored/missing logs
- [x] Release CI job with `PISTOS_REQUIRE_LIVE=1` (defined; not executed here)

Limitations:
- Symbols for identity: Go and Python only; JS/TS and C# use path + anchor line + ordinal.
- basedpyright installed only as a Python package (no npm package) is not located.
- tsc project references, Biome, ty and mypy are not integrated.
- C# restore uses only the local package cache; legacy/Windows-only project types are unverified.
- Windows and macOS execution of the packs has not been run.

## Milestone 5 — LSP, metrics and architecture rules

Scope: on-demand LSP lifecycle/facts in the four ecosystems, versioned function metrics, baseline
metric deltas, import graph facts, explicit cycle/layer rules.
Planned tests: fake-server failure semantics (pull vs push, late push, failed init, crash, endless
loading), live definitions/references/diagnostics per ecosystem, hand-computed metric fixtures per
language, Go regression/worsened/formatting/rename/relocation/gate scenarios, layer and cycle
violations, unknown-coverage qualification and strict mode, live import graphs for Python/JS/TS/C#.

Verified live (Linux arm64): gopls 0.23.0, pyright-langserver 1.1.414, TypeScript 7.0.2 native LSP,
csharp-ls 0.28.0; oxc-parser 0.153.0; Roslyn from .NET SDK 10.0.401; Python 3.12 `ast`.
Command: `PISTOS_REQUIRE_LIVE=1 go test -race -count=1 ./...` — all pass, no skips (44 CLI scenarios).

- [x] LSP client (Content-Length framing, separate from the worker protocol): initialize/initialized, capability negotiation, position-encoding conversion, configuration answers, didOpen versions, definition/references/implementation, pull diagnostics, progress-based readiness, `$/cancelRequest`, shutdown/exit
- [x] Push-only diagnostics and timeouts are never complete; failed init/crash/unsupported methods are explicit
- [x] `pistos facts …` starts only the owning project's server in the snapshot; results mapped to original paths; cached by backend/project/snapshot
- [x] `plugins install --lsp` (gopls via `go install`, csharp-ls via `dotnet tool install`, pyright-langserver via npm, repository TypeScript)
- [x] `review` never starts a language server
- [x] Metrics v1 (statements, params, nesting, cyclomatic) for Go (`go/ast`), Python (`ast` worker), JS/TS (oxc-parser worker), C# (Roslyn worker built offline against the SDK)
- [x] Embedded workers installed by `plugins install`; stale workers detected after pistos upgrades
- [x] Baseline deltas: new / worsened (with base value) / existing; rename re-keying and symbol relocation; advisory unless `[metrics] gate = true`
- [x] Import graph facts with provenance (resolved / syntax-only / unknown) for all four packs
- [x] Cycle (Tarjan SCC) and explicit layer rules; unknown edges qualify results; `require_complete` strict mode
- [x] Pre-existing issues whose decision guard fails (needs-revalidation) now gate

Limitations:
- TypeScript < 7 language server (typescript-language-server) is not integrated.
- Go/pyright diagnostics through LSP are push-only and therefore always incomplete; batch checks
  remain the authoritative gate.
- Import graphs ignore Go build constraints, Python `sys.path` changes, tsconfig path aliases, package
  `exports`, C# fully qualified names and reflection.
- Cognitive complexity and duplication detection are not implemented.

## Milestone 6 — BYOK discovery and union triage

Scope: backend/model aliases, one-/three-pass configurations, structured independent discovery, union
clustering, optional model triage, usage/budgets, feedback context, replay and refresh lifecycle.
Planned tests (fake HTTP server): A, B, A+C union with merged provenance and a surviving singleton;
model triage rejection with audit; failed pass is not a clean vote (bounded retries, `ai.required`);
tag refilter and replay with zero calls; refreshed omission and rewording keep identity; no-change
makes no calls; prompt-injection/output cannot change decisions or run commands; keys never persist
or leak; offline replay vs. offline miss; opt-in live test.

- [x] OpenAI-compatible provider: per-endpoint concurrency, timeouts, bounded retries, usage, key redaction, HTTPS-or-local endpoints
- [x] Bundled prompts (`discover.md`, `triage.md`) with `.pistos/prompts/` overrides; repository content marked untrusted
- [x] Independent concurrent passes over one captured evidence snapshot (budgeted), feedback context included
- [x] Candidate validation (anchors, evidence refs, required fields, budget) with rejected reasons
- [x] Union by issue identity; every raw candidate keeps cluster and outcome; duplicates recorded
- [x] Evidence and model triage (neutral ids, no identities/votes), refuted → rejected, unresolved → needs-investigation, deterministic corroboration → verified
- [x] Advisory AI issues with provenance; never blocking; `ai.required` only affects completeness
- [x] Raw call artifacts with exact messages; replay with zero calls (also offline); `--refresh-ai`
- [x] Lifecycle ledger: omission keeps accepted issues; carried across snapshots while the anchor exists
- [x] AI issues accept feedback decisions; candidates marked ignored on publication
- [x] Live run recorded against a local model (gemma-4-31B-it via SGLang); no hosted model exercised

Limitations:
- One backend shape (OpenAI-compatible); native provider adapters are not implemented.
- Token budgets are enforced per request (`max_tokens`) and on evidence size, not as a cumulative spend limit.
- Evidence is whole changed files; there is no chunking for very large changes (excess files are listed as omitted).
- The AI quality metrics of section 10 (recall, precision, singleton yield…) are not measured yet (milestone 8).
