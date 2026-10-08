# Implementation plan: Pistos, a local-first code-quality tool

**Handoff date:** 8 October 2026. This plan consolidates the agreed direction and takes precedence over illustrative interfaces in earlier research notes.

## Instructions to the coding agent

Implement the open-source project described below. This document is the handoff specification; no previous conversation is required. The tool is named Pistos, and its executable is `pistos`.

Inspect the existing repository and its contributor/agent instructions first. Preserve unrelated changes. Use the existing module path and conventions where available; otherwise use a documented placeholder until the repository owner supplies the publishing identity. Do not publish packages, create external services, transmit real repository code to a paid model, or create PRs without authorization. Local fixtures, mock endpoints and in-scope implementation/testing are authorized by the implementation request.

Work through the milestones in order, keeping a functioning vertical slice. Before each milestone, record its intended scope and tests; after it, report delivered behavior, verification results and remaining limitations. Keep an implementation checklist in the repository. Do not mark placeholder adapters or mock-only tests as real language support.

Prefer straightforward Go code, explicit data structures and small interfaces at actual external boundaries. Avoid framework-style abstractions, clever generic dispatch, permanent daemons and speculative infrastructure. This plan defines the target release; milestone 1 alone is not completion.

## 1. Product objective and fixed decisions

Build a fast, lean CLI that reviews code changes using deterministic tools and optional BYOK AI. It must help maintain reliable, maintainable code under heavy agentic coding.

Fixed decisions:

- **Go core**, with bounded parallelism and a native binary.
- **JS/TS, Python, Go and C#** are the first supported ecosystems.
- Language packs are separately installed and activated only where needed.
- Stateful/custom plugins run out of process over a versioned stdio protocol; existing analyzer CLIs can be wrapped declaratively.
- Native local execution and Docker execution use the same contracts and normalized results.
- Compiler/type/lint evidence, complexity regressions and architecture rules are deterministic.
- AI discovery supports configurable independent passes, including a different model for every pass.
- Thorough AI review collects the **union** of three discovery passes, deduplicates and triages it. Do not require majority agreement to retain a supported discovery.
- Tagging an issue automatically updates **`.pistos/feedback.yaml`**. Decisions persist across runs, different diffs, commits and model changes.
- A simple action such as **`pistos tag 47 irrelevant`** is sufficient. Scope, matching evidence and persistence are handled automatically.
- Default blocking verdicts come from a fixed deterministic check plan. Fresh AI discoveries are advisory.
- No automatic code fixes, merges, hidden learning or silent analyzer upgrades.

Proposed license: Apache-2.0, unless an existing repository license or owner decision specifies otherwise. Keep third-party analyzer licenses and redistribution requirements documented separately.

Not part of the first release: hosted dashboard, mandatory GitHub App, multi-repository search, autonomous repair agents, embeddings database, WASM runtime, all-language mega-image, exhaustive vulnerability analysis, perfect semantic equivalence of arbitrary AI paraphrases, or a guarantee that three AI passes find every defect.

## 2. User-facing contract

### Commands

```sh
pistos init                         # detect project languages
pistos init --lang csharp           # select one ecosystem
pistos init --lang go --lang python # select several
pistos plugins install --locked    # explicit dependency acquisition
pistos doctor                      # tools, versions, capabilities, configuration
pistos review                      # local changes against HEAD
pistos review --base main          # working snapshot against merge-base(main, HEAD)
pistos review --base main --head HEAD
pistos review --all                # explicit full-project review
pistos review --profile thorough   # three AI discoveries plus configured triage
pistos review --refresh-ai         # explicit fresh discovery, not cached replay
pistos review --format json
pistos review --format sarif
pistos review --offline
pistos tag 47 irrelevant
pistos tag 47 false-positive
pistos tag 47 test-only
pistos reopen 47
pistos feedback                    # inspect/edit decisions interactively
pistos review --include-ignored
pistos reproduce <issue-key>
```

Language aliases: `js`/`javascript`, `ts`/`typescript`, `py`/`python`, `go`/`golang`, and `cs`/`csharp`/quoted `"c#"`. JS and TS share a pack but retain language-specific configuration. Use conventional `--lang`; document shell quoting for `c#`.

`init` is idempotent and preserves existing configuration, feedback and prompts. It must not overwrite user configuration or install/download tools implicitly. Normal reviews do not install packages. A no-change review reports its empty scope and makes no unnecessary AI calls; it must not claim the whole repository is defect-free.

Without `--head`, include eligible staged, unstaged and untracked files in the working snapshot. Respect ignores; show scope explicitly. With an explicit commit `--head`, analyze that commit independently of dirty working-tree changes. Never modify the user's checkout to obtain a baseline or target tree.

For the first release, `--head` requires `--base`. Resolve the comparison baseline as `merge-base(base, selected-head)`; the selected head is HEAD for a working-snapshot review. Record both the requested refs and resolved trees so the comparison is reproducible. Exact non-merge-base comparisons can be an explicit later option.

Resolve short issue numbers against the current published report. Missing or ambiguous references are errors, not guesses. Store canonical issue selectors in feedback, never report list positions. Support canonical keys when no current short reference is available.

### Verdict and completeness

Keep deterministic verdict and AI coverage separate. Report capability status as `complete`, `cached-complete`, `unsupported`, `failed`, `incomplete` or `not-requested`.

Exit codes:

- `0`: required deterministic checks completed and no active policy-blocking finding exists.
- `1`: completed deterministic policy checks found active blocking issues.
- `2`: invalid configuration, execution error or incomplete/unsupported required checks.
- `130`: user cancellation.

Incomplete required checks take precedence over an ordinary findings exit code; retain discovered issues in the error report. Optional AI failure produces an explicit degraded AI result, not a clean vote. Add an `ai.required` option for workflows that require completion of the requested AI plan; default false. It does not make AI issues blocking.

Reserved decision tags have documented effects: `irrelevant`, `false-positive` and `accepted-risk` suppress matching issues from normal comments and the default gate; `deferred` remains tracked and is not silently exempted. Ordinary labels such as `test-only` or `security` do not alter policy. All suppressed findings remain auditable.

## 3. Repository files and minimal configuration

Committed project files:

- `.pistos/config.toml`: projects, profiles, backend/model aliases, resource limits and policy.
- `.pistos/lock.json`: schema/core protocol, pack/tool versions, artifact checksums and effective toolchain constraints.
- `.pistos/feedback.yaml`: authoritative human tags, dispositions, scopes and matching guards.
- `.pistos/prompts/discover.md` and `triage.md`: optional reviewed prompt overrides; bundled defaults work without these files.

Ignored local state:

- `.pistos/cache/`: content-addressed results, evidence and AI artifacts.
- `.pistos/state/`: issue ledger, current-report references and local audit metadata.

Human decisions must remain effective after deleting cache/state. Store their matching information in `feedback.yaml` itself. Do not require a server/database for persistence.

Use one configuration vocabulary: `[ai]`, not parallel `[llm]` and `[ai]` settings. Validate schemas and reject unsupported versions and unknown policy keys. Commit sample schemas and migration tests.

Example, showing intent rather than the entire schema:

```toml
schema = 1
languages = ["go", "python"]
profile = "balanced"

[execution]
mode = "native"
jobs = 4

[ai]
enabled = false
required = false
aggregation = "union"
reuse_results = true

[[ai.discovery]]
id = "pass-1"
model = "reviewer-a"

[[ai.discovery]]
id = "pass-2"
model = "reviewer-b"

[[ai.discovery]]
id = "pass-3"
model = "reviewer-c"

[ai.triage]
mode = "model"
model = "triager"
keep_supported_singletons = true
```

Model aliases resolve to backend, endpoint, requested model identifier, secret environment-variable name, timeout and budget. Never write API keys into configuration, lockfiles, logs or feedback. Do not automatically load arbitrary repository `.env` files.

Profiles: `fast` runs declared file-local checks; `balanced` adds required project-aware deterministic checks; `thorough` adds three independent AI discoveries and configured triage. AI is disabled by default until explicitly configured. Report profile coverage; a fast profile cannot masquerade as a complete balanced gate.

Monorepo configuration records stable project IDs, roots, ecosystem and backend. Detect roots from existing project files without crawling vendor/generated directories. A project can share semantic workspace state with referenced projects; do not assume one language means one project.

## 4. Architecture and contracts

Organize code into small areas such as `cmd/pistos`, and internal packages for `config`, `snapshot`, `plan`, `runner`, `plugin`, `finding`, `policy`, `feedback`, `store`, `lsp`, `ai` and `report`. Add pack manifests/adapters, schemas, fixtures and documentation alongside them. Combine packages if boundaries prove artificial; do not create an interface for every helper.

Start with Go standard-library process, HTTP, JSON, hashing and filesystem facilities. Small candidate dependencies are [Cobra](https://github.com/spf13/cobra) for subcommands, [BurntSushi TOML](https://github.com/BurntSushi/toml), the [maintained Go YAML project](https://github.com/yaml/go-yaml), and `golang.org/x/sync` for coordination. Verify module paths, compatible releases and licenses before pinning. Avoid importing Viper or an agent framework just to parse a few files or call a model.

Keep the core buildable without CGo. Native parser bindings belong in selected pack workers. Keep checksumming and result storage simple first; a metadata database is not a prerequisite.

### Review flow

Capture snapshot/configuration → detect projects → select capabilities → build task graph → reuse valid caches → execute deterministic tools → normalize/deduplicate → compare baseline → optional independent AI discovery → candidate union/triage → apply feedback → calculate verdict → emit report and audit artifacts.

Use immutable input objects and typed results. All paths in canonical artifacts are repository-relative with normalized separators. Map temporary snapshot/worker URIs back to original repository locations; prevent path traversal and references outside authorized roots.

### Shared models

Define versioned types and serialized schemas before integrating tools:

- `Project`: stable ID, roots, ecosystem, backend and target/build configuration.
- `InputManifest`: snapshot identity, source/dependency/config hashes, toolchain and effective policy.
- `CheckPlan`: fixed tasks, capabilities, dependencies, scopes, resource weights and requiredness.
- `Finding`: canonical key, source aliases, rule/problem family, causal anchor, locations, severity, evidence references, evidence hash and reproducibility data.
- `Issue`: canonical finding lineage, current evidence, provenance, user tags/disposition and lifecycle.
- `Candidate`: AI pass/model provenance, anchors, hypothesis, assumptions, failing scenario and evidence references.
- `CapabilityResult`: completeness, findings/facts, covered scope and declared limitations.
- `Report`: stable canonical findings/verdict/coverage plus a separate telemetry/presentation envelope.

Identity must not depend on an AI explanation, model name, severity adjective, commit/diff ID or line number. Keep issue identity separate from current evidence hashes. Disambiguate two causes of the same family inside one function. Maintain explicit source-rule aliases and verified relocation/rename mappings.

Canonical output uses stable sorting and tie-breaking. Keep timestamps, timings, process IDs and local display-number assignments outside the canonical object. Provide a canonical JSON export suitable for byte-comparison; ordinary JSON can wrap it with telemetry. Do not compare volatile envelopes when testing repeatability.

### Plugins

A versioned pack manifest declares supported languages/project types, capabilities, tool versions, executable/artifact checksums, allowed configuration/environment inputs, result formats, scope requirements and resource weights.

Support:

1. Declarative wrappers around tools with supported structured output.
2. Executable workers for custom adapters and stateful syntax/semantic operations.

Do not use Go's native `plugin` package. Worker transport: JSON-RPC 2.0 objects framed as newline-delimited UTF-8 JSON, one object per line, bounded frame size; stderr is logs only. This is the project worker protocol, distinct from standard LSP framing.

Minimum worker methods: `initialize` (protocol/pack versions and capabilities), `analyze`, `facts`, `cancel` and `shutdown`. Responses include covered snapshot/project scope and completeness. Test unsupported versions, malformed output, excessive frame size, worker crashes and cancellation. Notifications/progress cannot silently replace a final result.

Reading manifests must not start workers. Installing a pack, activating it for a repository, and executing an uncached capability are different operations. A Go-only review must not launch Node, Python, .NET or unrelated language servers.

### Scheduler and process ownership

Use bounded worker pools, cancellation contexts and global CPU/memory budgets. Account for analyzer internal parallelism; pass supported limits rather than starting several unrestricted whole-project tools. Model solution/type checks as heavier tasks than file-local lint. Limit model calls per provider/local server separately.

Use argument arrays, not shell interpolation. Drain stdout/stderr without deadlocks, classify tool-specific exit codes, cap captured output, terminate owned process trees on cancellation and reap children. Implement/test Unix and Windows behavior explicitly; do not assume canceling a Go context kills grandchildren.

Never stop a required review early merely because the first N findings arrived. Stream progress, then deterministically rank the complete required result set. Timeouts are incomplete outcomes.

### Snapshot, baseline and cache correctness

Support immutable commit trees and consistent working snapshots. Analyze isolated materializations where practical, with compatible installed dependencies reused deliberately and build outputs directed to scratch/cache. At minimum, detect relevant input changes during analysis and reject mixed-snapshot results.

Baseline analysis is read-only and separate from head analysis. Analyze semantic changes at package/project boundaries required by the checker; do not substitute changed-file-only checking. Filter/report new or worsened issues after collecting the necessary context.

Cache keys include relevant sources/dependencies, effective config, rule/tool versions, compiler flags/target, environment/stubs and generated inputs. File-local checks can use narrower keys. Preserve findings when an incremental backend skips unchanged work. Cache hits must retain completeness and provenance.

Use atomic artifact writes and validate content/schema before reuse. Never write an empty successful result for a failed analyzer. Cache invalidation tests are required for changed imports, lockfiles, build configuration, stubs, generated code, deletions and renames.

`--offline` prevents core/provider network calls and undeclared acquisition, while allowing valid local cache reuse. Invoke tools with supported no-fetch/no-restore settings; missing dependencies produce an actionable incomplete result. Document that native mode cannot sandbox arbitrary subprocess networking. Use an isolated Docker profile when network enforcement is required.

## 5. Language packs, LSP and maintainability analysis

Before implementing each adapter, verify the CLI/API against primary documentation and the **pinned installed version**. Do not rely on historical command flags. Document required runtimes, exit-code mapping, structured output, supported scopes and unsupported features.

| Pack | Initial deterministic integration | Semantic/LSP integration | Initial support boundary |
|---|---|---|---|
| JS/TS | Repository-selected TypeScript compiler, Oxlint or existing ESLint/Biome configuration | Native TypeScript LSP where compatible; older `typescript-language-server` backend where needed | Respect TS config, JS typing settings and workspace/project references |
| Python | Ruff plus repository-selected Pyright/BasedPyright or ty | Corresponding semantic server; Ruff server for lint facts only | Correct interpreter environment, imports and stubs; dynamic behavior remains qualified |
| Go | Go compiler/type checks, `go vet`, selected Staticcheck; import existing golangci-lint configuration where practical | `gopls` | Package/module/workspace scope, build tags, target and CGO settings |
| C# | SDK compiler and Roslyn analyzers respecting `.editorconfig` and project settings | `csharp-ls` or a compatible Roslyn worker | Modern SDK-style cross-platform projects first; legacy/Windows workloads explicitly qualified |

Reuse repository policy. Do not silently replace Pyright with ty, an older TypeScript compiler with a new one, or ESLint with Oxlint just because another backend is faster. Record the selected backend. Deduplicate overlapping tools using a declared authority policy; disagreement remains visible.

C# must handle multiple projects/target frameworks, analyzer versions and generated sources. Use structured build/analyzer output or a typed Roslyn worker; prevent multiple projects overwriting one diagnostic artifact. A no-op incremental build with no new output must reuse previous findings or explicitly reanalyze. Failed semantic loading is incomplete, not a clean solution.

### LSP integration

Implement an on-demand client against the [LSP specification](https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/). Keep standard LSP headers/framing separate from the custom worker transport.

Support initialization/capability negotiation, workspace configuration, document versions, diagnostics, definitions, references, implementations when supported, cancellation and shutdown. Add server-specific readiness/completion handling. Use pull diagnostics when supported; never infer whole-project completeness from a sleep or the first notification. Batch compiler/typechecker results remain the authoritative gate fallback.

Cache facts by backend/project/snapshot. Start a server only for requested facts. A persistent watch mode can follow after one-shot correctness; it is not required to keep core operation functional.

### Metrics and architecture rules

Provide per-function length, parameter count, nesting and cyclomatic complexity for the four ecosystems, with a versioned definition and fixtures for language constructs. Prefer the parser already used by the active pack; Go AST and Roslyn are suitable in their ecosystems, while JS/TS/Python need an explicitly selected syntax backend. Keep any native grammar/interpreter dependency in the pack, not the core.

A Lizard adapter is acceptable for an explicitly optional prototype/compatibility mode, but not an undisclosed Python requirement for every language pack. Do not claim equivalence to Sonar cognitive complexity. Cognitive complexity and broad duplication detection can be subsequent capabilities with separate definitions.

Report base/head deltas. Existing over-threshold debt is not automatically new debt; identify genuinely new/worsened metrics and relocated symbols. Make thresholds configurable, initially advisory unless the user enables a deterministic gate rule.

Packs extract import/project edges with resolution provenance: resolved, syntax-only or unknown. The core applies dependency-cycle and configured layer-boundary rules. Start with explicit path/module layer mappings. A strict architecture gate requires sufficient coverage; unknown edges must not count as proof of no violations. Do not claim to resolve arbitrary dynamic imports or reflection.

## 6. Persistent tagging: first-class functionality

### Required UX

`pistos tag 47 irrelevant` immediately updates `.pistos/feedback.yaml` and refilters the current report. No required explanation, scope flag, lane flag or hashes. Explanations are optional and can be added interactively or in the file.

Default scope is the existing issue under its established assumptions, applying to both lanes for equivalent issues, with no time-based expiry. In `pistos feedback`, provide explicit **This issue** and **This rule/pattern** choices. Wider rule/pattern preferences require explicit human selection and a preview of their match scope; one ignored issue must not silently become a universal exception.

Use the same matching and mutation service for CLI and an interactive review/tag view. A minimal terminal list/detail/tag interaction is sufficient; a web UI is not required. Noninteractive commands remain available for agents/CI.

### Persisted record

Store a schema version, decision ID, canonical subject key, tags/disposition, optional reason text, scope type, project/path/symbol selectors, rule/problem family, causal anchor, source aliases, relevant assumptions/guards and decision version. Add original tree/commit references and verified rename lineage where needed for relocation without a local cache. Keep secrets and unnecessary source bodies out of committed feedback.

Users must not author generated selectors manually. YAML remains readable/editable; preserve unrelated entries and comments where feasible. Reject invalid edits with a useful diagnostic. Use atomic writes, serialized concurrent mutation and schema migration. Updating one entry must not erase manually authored preferences.

### Matching and invalidation

Load the file on **every** review, regardless of base/head, cache state or model. Apply strong matches after both lanes produce findings and supply relevant decisions as AI prompt context.

Match using stable identity and scoped causal/code evidence. Support harmless line shifts, formatting and verified file renames. AI paraphrase matching may propose a link, but similarity alone cannot authorize hiding an unrelated issue. Uncertain matches require revalidation rather than blanket suppression.

Do not remove a decision when its issue is outside the current diff. Preserve it for future diffs and cache deletion. A materially changed assumption can mark the decision stale; a new diff, model name, rewritten explanation or severity adjective alone cannot.

Example boundary: a deliberately vulnerable test fixture remains ignored across diffs. Making that code production-reachable invalidates the relevant test-only guard. A second defect in the same function remains reportable.

Only explicit user actions or approved policy changes create durable exceptions. AI can recommend feedback entries but cannot modify them. In untrusted PR analysis, use the approved base policy or report policy changes for review; do not let the analyzed change exempt itself.

## 7. AI discovery, triage and issue lifecycle

### Backend contract

Implement a small provider interface for structured discovery/triage requests, budgets, cancellation and usage/provenance. Start with one configurable compatible HTTP backend usable with BYOK cloud endpoints and local model servers. Verify provider-specific payloads against current official documentation; additional native provider adapters can follow without changing the engine.

Support endpoint/model aliases and secret environment variables. Use HTTPS for remote endpoints; allow explicitly configured local endpoints. Keep provider keys out of analyzer environments and mask secrets in diagnostics. Unit/integration tests use fake HTTP servers and canned responses; live paid-model tests are opt-in only.

### Independent discovery

Each pass receives the same captured code/evidence, project requirements and relevant approved feedback, in a fresh session. No shared discovery conversation and no visibility into other passes' candidates or the coding agent's justification. Model aliases may resolve to the same or different providers/models.

Validate structured candidates: anchors exist in the snapshot; evidence references resolve; problem family, hypothesis, assumptions, impact and failing scenario are present. Unsupported certainty claims or malformed outputs cannot become verified findings. Record raw artifacts locally and statuses for every requested pass.

Run independent passes concurrently where budget/rate/memory limits permit. Serial local execution must preserve independence. Apply bounded retries for transient failures only, with recorded attempts. Do not silently swap failed models or run unlimited rounds until nothing new appears.

### Union and triage

Union all candidates; preserve every raw candidate and its cluster/outcome. Merge equivalent causes, not merely nearby lines or similar prose. Keep well-supported single-model candidates. Votes/model agreement are provenance, not correctness probabilities.

Triage modes: evidence-only/manual-compatible and optional model-based. A separate model triager receives current code, assumptions and distinct hypotheses; ask it to check/refute claims, not endorse them. Hide model identity/vote totals during initial assessment where feasible, then attach provenance.

Outcomes: `verified`, `supported`, `needs-investigation`, `rejected`, `duplicate`, `ignored`. Only trusted deterministic evidence can justify `verified`; an AI-written test is not automatically a trusted specification oracle. Unresolved claims remain distinguishable from refuted claims. Keep rejected candidates auditable and normal reports concise.

Three discovery calls plus model triage is four logical passes, potentially more requests after chunking. Set explicit time/token/candidate budgets and record actual usage where available. Do not assume a weak cheap triager improves a stronger discoverer's result; evaluate true issues it rejects.

### Lifecycle and replay

Same snapshot: accepted issues persist across refreshed discoveries. Omission by a later pass cannot resolve them; removal requires explicit refutation, a user decision or verified changed-code outcome.

New snapshot: revalidate anchors/guards and retain pending issues where verification is incomplete. Do not accumulate irrelevant old issues forever, and do not label an AI issue fixed because it was not rediscovered. Deterministic closure requires a complete relevant check; AI closure requires explicit evidence/decision.

Separate caches for deterministic evidence, raw discovery, triage and publication. Discovery keys include evidence/snapshot, prompt/model/settings and relevant memory; triage keys also include the candidate set and verifiers; publication includes effective feedback/policy. Tag changes can refilter cached reports with zero model calls. Record original model inputs honestly; future fresh passes receive updated context.

Default reuse gives reproducible replay. `--refresh-ai` explicitly produces fresh discovery and can add new issues. AI omissions/errors must never alter which fixed checks determine the deterministic gate. Promotion of an AI discovery into a permanent check requires a reviewed policy/rule change.

## 8. Milestones and acceptance gates

### Milestone 1 — scaffold, contracts and fixture infrastructure

Deliver CLI skeleton, config/lock/feedback schemas, typed models, canonical JSON serialization, fixture repositories, fake workers and fake HTTP endpoints. Add build/test/race-check CI and a concise architecture document.

Acceptance: help/init work without Node/Python/.NET; init is idempotent; invalid config is rejected; no secret persistence; canonical serialization is stable; tests use no real API key.

### Milestone 2 — plugin runner and Go vertical slice

Deliver snapshot/baseline handling, capability planning, lazy manifests, process supervision, bounded scheduling, native execution, Go checks, cache/provenance, text/JSON output and verdicts.

Acceptance: a real Go fixture produces a known diagnostic; a clean fixture completes successfully; broken/missing tools yield incomplete outcomes; unrelated packs never start; changed dependencies invalidate caches; identical completed checks match across three repetitions and worker counts 1/2/4; cancellation leaves no owned child processes.

### Milestone 3 — persistent feedback and issue ledger

Deliver short tag/reopen commands, atomic feedback mutation, current-report references, ordinary/decision tags, strong matching, cross-diff persistence and a minimal interactive feedback view. Test AI-shaped candidates with fixtures before real provider integration.

Acceptance: tag once; change unrelated files and base/head; move lines; delete cache/state; rerun and still suppress the recognized issue. Test verified rename handling, manual YAML edits, explicit pattern scope, concurrent tags and reopen. A materially changed guard and a different nearby defect remain visible.

### Milestone 4 — JS/TS, Python and C# packs

Deliver real deterministic adapters, installer/lock handling and runtime/backend documentation for all three remaining ecosystems. Add clean/defective/multi-project fixtures and structured-output normalization tests.

Acceptance: each pack produces real expected findings and clean results; configured backend/rules are preserved; incompatible/missing tools are explicit; mixed-language roots activate only needed packs; C# generated-source/target-framework and no-op-build cases are covered. Missing local runtimes can skip live tests with a clear reason, but the release CI must execute the real supported matrix.

### Milestone 5 — LSP, metrics and architecture rules

Deliver on-demand LSP lifecycle/facts in the four ecosystems, language metrics with algorithm versions, baseline metric deltas, import/project graph facts and explicit cycle/layer rules.

Acceptance: server startup occurs only for needed facts; delayed diagnostics and failed initialization never appear complete; fixture definitions/references map to original paths; a complexity regression and a dependency-cycle/layer violation are reproducible; formatting/renames do not masquerade as new debt; unknown graph coverage remains qualified.

### Milestone 6 — BYOK discovery and union triage

Deliver configurable backend/model aliases, one-/three-pass profiles, structured independent discovery, union clustering, optional model triage, usage/budgets, feedback context, replay and refresh lifecycle.

Acceptance: fake passes return A, B and A+C, producing A/B/C with merged provenance; a supported singleton survives; a triager can reject a false claim without losing audit data; a model failure is not a clean vote; tagging refilters with zero HTTP calls; refreshed omission does not erase an accepted issue; fresh AI remains outside the blocking plan. Provide optional live-test instructions without claiming live results not actually obtained.

### Milestone 7 — Docker, portable release artifacts and reporting

Deliver Docker execution/image recipes, SARIF export, core/per-language artifacts, installation documentation and CI/release definitions. A native host core can run containerized workers over stdio. Language-specific complete images can also run the core and workers directly; do not require Docker-in-Docker or a mounted Docker socket.

Acceptance: selected-language images exclude unrelated runtimes; matched native/container fixtures produce equivalent canonical deterministic results; core builds on Linux/macOS/Windows; process cleanup and path normalization are tested on each supported OS; SARIF validates and maps to repository files; offline fixtures perform no core/provider network calls.

Document a trusted local profile and a restricted untrusted-analysis profile. Restricted analysis uses no provider secrets, controlled source/scratch mounts, non-root execution, no network and resource limits. If AI is used with untrusted code, run the credentialed provider client outside that sandbox against curated evidence. Do not claim ordinary native execution or a basic container is a complete security boundary.

### Milestone 8 — benchmark, reliability audit and handoff

Deliver reproducible benchmark commands/results, a limitations matrix, README tutorials, pack author guide, migration notes and release-readiness checklist. Audit all acceptance cases below.

Acceptance: publish measured startup/warm/cold latency and total child-process memory with machine/tool versions; compare three-run deterministic canonical output; compare AI single/same-model/mixed-model/triaged variants using the same labeled corpus and matched budgets. Explain missing measurements rather than inventing performance or reliability figures.

## 9. Mandatory regression suite

Automate these as scenario tests, with fakes for controlled failure/timing cases and real tools for language support:

1. No changes: no unnecessary analyzer/model starts, explicit review scope.
2. Three deterministic runs and changed worker counts: same canonical findings/verdict.
3. Required worker crashes, truncates output or times out: incomplete, never clean.
4. A linter's findings exit code is distinct from an execution failure.
5. Config, imports, dependencies, stubs, targets and generated inputs invalidate relevant caches.
6. Incremental no-op preserves existing diagnostics; baseline artifacts cannot contaminate head results.
7. Snapshot changes during analysis are isolated or rejected.
8. Unselected language packs do not initialize processes or load runtimes.
9. Cancellation cleans process trees on Unix and Windows.
10. Tag `irrelevant` once; new diff, changed base/head, AI refresh and cache/state deletion preserve the recognized dismissal.
11. Harmless formatting/line shifts and verified renames preserve matching.
12. Changed causal/contract assumptions request revalidation; unrelated nearby issues are not suppressed.
13. Explicit pattern preference suppresses only its declared scope; ordinary tags do not affect verdicts.
14. Reopen restores visibility; absent-from-diff decisions are not deleted.
15. Two concurrent tag actions preserve both entries and manual feedback content.
16. AI wording/model/line-number changes do not alone create a different canonical issue.
17. Union retains unique discoveries; each raw candidate has a recorded cluster and outcome.
18. Later model silence cannot close a known issue; failed passes are not negative votes.
19. Cached replay makes zero provider calls; tag-only edits need no rediscovery.
20. Prompt instructions attempting to alter feedback or run arbitrary commands are treated as untrusted data.
21. Keys are absent from analyzer environments, persisted config and exported logs; fake endpoint tests verify redaction.
22. Native/container matched environments agree; unsupported target-specific builds are clearly qualified.

## 10. Performance and release definition of done

Measure installation/download separately from review. Track core startup/no-op overhead, cold semantic analysis, warm function edit, public API/dependency/configuration changes, analyzer cache hit rates, model usage and total peak RSS across processes. Record p50/p95 and representative project sizes.

Set numerical performance budgets after the first measured vertical slice, then add a regression threshold in CI. Do not promise a universal two-second whole-solution review or compare a warm diff scan against a competitor's cold full scan.

For AI, measure defect recall, precision, accepted singleton yield, false triage rejection, marginal useful issues per extra pass, fresh-run overlap, ignored-issue recurrence and cost. Model agreement alone is not the success metric.

The target release is complete only when:

- The short init/review/tag/reopen flow works locally and through supported Docker execution.
- All four ecosystems have real tested deterministic adapters; LSP/metrics coverage and limitations are documented.
- Packs are lazy and versions are locked; normal review does not install or upgrade tools.
- Feedback survives different diffs and cache deletion, with safe matching and explicit broader preferences.
- Configurable BYOK discovery and union triage work with fakes and documented opt-in live endpoints.
- Reproducible deterministic gates, canonical replay and incomplete-result handling pass regression tests.
- No fresh AI vote selects the blocking check plan or silently creates an exception.
- Every reported issue has source/evidence/provenance and an actionable reproduction or investigation path.
- CI/build/schema/SARIF checks, portability tests and measured benchmarks are recorded.
- README, contribution guide, pack protocol, security/trust boundaries and known limitations are sufficient for another maintainer.

At handoff, report implemented milestones, tests actually run, measured performance, skipped/unverified coverage and the next safe development step. Do not declare the full release complete while any mandatory capability is still a placeholder.

## Primary integration references

Use these as entry points; recheck details for the versions selected during implementation:

- [Go subprocess management](https://pkg.go.dev/os/exec), [errgroup](https://pkg.go.dev/golang.org/x/sync/errgroup), [weighted semaphores](https://pkg.go.dev/golang.org/x/sync/semaphore).
- [LSP specification](https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/).
- [TypeScript native compiler project](https://github.com/microsoft/typescript-go), [older-toolchain LSP adapter](https://github.com/typescript-language-server/typescript-language-server), [Oxlint documentation](https://oxc.rs/docs/guide/usage/linter/).
- [Ruff](https://docs.astral.sh/ruff/), [Pyright](https://github.com/microsoft/pyright), [ty](https://docs.astral.sh/ty/).
- [gopls diagnostics](https://go.dev/gopls/features/diagnostics), [Staticcheck](https://staticcheck.dev/docs/).
- [Roslyn analyzer configuration](https://learn.microsoft.com/en-us/dotnet/fundamentals/code-analysis/overview), [Roslyn workspace APIs](https://github.com/dotnet/roslyn), [csharp-ls](https://github.com/razzmatazz/csharp-language-server).
