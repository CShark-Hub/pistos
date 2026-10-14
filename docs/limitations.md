# Limitations matrix

| Area | Go | Python | JS/TS | C# |
|---|---|---|---|---|
| Deterministic checks | go vet, Staticcheck | Ruff, Pyright/basedpyright; ty, mypy **unsupported** | repository tsc, ESLint, Oxlint; Biome **unsupported**; TS project references per pistos project only | dotnet build + Roslyn analyzers (SARIF); SDK-style projects, .NET 8+ SDK |
| Known tool incompatibility | Staticcheck 0.8.1 cannot read Go 1.27 export data → reported incomplete | snap-packaged Ruff cannot read outside its sandbox → incomplete; PATH not searched for Ruff | TypeScript < 7 has no built-in LSP | legacy/Windows-only project types unverified |
| Dependencies | module cache only, never downloaded | project `.venv`/`venv` (native mode only) | repository `node_modules` | local NuGet package folder only |
| Identity symbol | function/method | def/class | path + anchor + ordinal (no symbol) | path + anchor + ordinal (no symbol) |
| Metrics backend | go/ast | Python `ast` | oxc-parser 0.153.0 | Roslyn from the SDK |
| Import graph | module-internal packages; build constraints ignored | file layout (root, `src/`); `sys.path` changes ignored | relative specifiers; aliases/exports unknown | `using` → namespace directories; FQNs/reflection not traced |
| Language server | gopls 0.23 (push diagnostics → incomplete) | pyright-langserver (push → incomplete) | TypeScript 7 native LSP (pull) | csharp-ls 0.28 (pull) |
| Docker image | debian-slim + Go; cgo off | Python + Node (for Pyright) | Node + Oxlint/oxc | .NET SDK |

| Cross-cutting | Limitation |
|---|---|
| Platforms | Verified on Linux arm64 only; macOS/Windows CI defined but not run; Windows process-tree cleanup uses `taskkill /T` (compile-checked) |
| Project include/exclude | Filters scope, findings and AI evidence; analyzers still read the whole project root (no per-tool exclude flags) |
| Cache | Granularity is the project subtree (not per package/file); out-of-root dependencies only via `references`/shared inputs |
| Lock | One toolchain per platform; docker image IDs differ per machine because apt/npm/pip layers are not bit-reproducible |
| Feedback | Rename verification needs an identical blob or a locally available origin tree/commit; two identical anchor lines in one symbol are distinguished by order |
| AI | One OpenAI-compatible backend; per-review spend cap only (no daily/monthly cap; USD is estimated from configured prices); whole changed files as evidence (no chunking); same-model passes at temperature 0 add little diversity (see benchmarks) |
| Metrics | No cognitive complexity, no duplication detection; not equivalent to Sonar metrics |
| Secrets | gitleaks 8.30.1 directory scan of the snapshot only (no git history); `.gitleaksignore` fingerprints contain line numbers; reported columns are one too high after the first line; flagged lines are withheld from HTML excerpts and AI evidence, but the identity anchor is still a hash of the flagged line |
| Security | Native mode is not a sandbox; restricted docker profile is not a complete security boundary (kernel/runtime/base-image trust) |
| Facts | `pistos facts` native only; one request per server start (no persistent watch mode) |
| Performance | Numbers measured on one 20-core arm64 machine with tiny fixtures and one medium Go codebase; no large-monorepo or p95 over many edits |
