# Semantic facts through language servers

`pistos facts <definition|references|implementation|diagnostics> <path[:line:column]>` starts the
language server of the project that owns the file — and only that one — inside the working snapshot,
then maps every location back to the repository path. `review` never starts a language server.
Servers are acquired and locked explicitly with `pistos plugins install --lsp`.

| Pack | Server | Verified | Diagnostics |
|---|---|---|---|
| Go | gopls v0.23.0 (managed `go install`) | Go 1.26.9 | push only → reported **incomplete** |
| Python | pyright-langserver 1.1.414 (repository or managed npm) | Node 24.21.0 | push only → incomplete |
| JS/TS | the repository's TypeScript ≥ 7 (`tsc --lsp --stdio`) | TypeScript 7.0.2 | pull → complete |
| C# | csharp-ls 0.28.0 (managed `dotnet tool install`) | .NET SDK 10.0.401 | pull → complete |

TypeScript < 7 has no built-in server; the older `typescript-language-server` backend is not
integrated yet, so such repositories get a failed result rather than a substitute.

Lifecycle: `initialize` (UTF-8 or UTF-16 position encoding negotiated and converted to byte
columns), `initialized`, `workspace/configuration` answered from the pack's settings (e.g. pyright's
`python.pythonPath` from the project virtualenv), `didOpen`, then the request. Servers that load the
workspace asynchronously announce it with work-done progress; pistos waits until all progress has
ended (bounded by a timeout, after which the answer is **incomplete**). Diagnostics are complete only
through pull diagnostics; a push-only server gives no completion signal and is never treated as
complete, regardless of timing. Failed initialization, crashes, unsupported methods and timeouts are
failed/unsupported/incomplete results, never empty successes.

Completed answers are cached by server identity, project subtree, dependency fingerprint, method and
position under `.pistos/cache/facts/`.
