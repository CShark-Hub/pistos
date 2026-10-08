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
