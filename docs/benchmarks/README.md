# Benchmarks

Reproduce (from the repository root, with the runtimes of the measured packs on PATH):

```sh
go build -trimpath -o /tmp/pistos ./cmd/pistos
go run ./tools/bench -pistos /tmp/pistos -out docs/benchmarks          # latency, memory, determinism
go run ./tools/aibench -pistos /tmp/pistos -endpoint http://localhost:8000/v1 \
  -model <model-id> [-key-env NAME] -out docs/benchmarks               # AI variants on the labeled corpus
```

`tools/bench` creates throwaway repositories from fixtures (and from a copy of pistos itself as a
medium Go codebase) and measures:

- **Install** (`plugins install`, including downloads): reported separately from review time.
- **No-op review**: nothing changed. This is mostly core overhead plus git snapshotting.
- **Cold review**: `--all` with the result cache and materialized trees deleted. This is real analysis
  of every check; Go/.NET build caches outside pistos stay warm.
- **Warm function edit**: one new function per run; the baseline comes from cache.
- **Peak RSS**: the sum of resident memory over the pistos process and all descendants, sampled from
  `/proc` every 10 ms (Linux only, so short-lived peaks can be missed).
- **Determinism**: three cold `--all` runs compared byte-for-byte as canonical JSON.

p95 over 3–10 runs is only indicative. Results for the recorded machine are in `results.md` /
`results.json`, and the AI comparison is in `ai-results.md` / `ai-results.json`.

## Not measured (and why)

- **macOS/Windows**: there was no machine available here.
- **Large monorepos and many-project workspaces**: the largest case is pistos itself (15.6k lines of Go).
- **Cache hit rates over a real edit history**: only synthetic edits were replayed.
- **Mixed-model AI**: only one general-purpose model was available locally. The comparison covers
  single pass, three same-model passes and three passes with same-model triage.
- **Hosted/paid models**: not exercised; the plan forbids sending repository code to a paid model
  without authorization.
