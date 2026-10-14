# Third-party components

Pistos does not redistribute analyzers. Packs locate tools that the repository already uses or that
`pistos plugins install` acquires explicitly from their upstream distribution channel.

## Go module dependencies (compiled into the core)

| Module | Version | License |
|---|---|---|
| github.com/spf13/cobra | v1.10.2 | Apache-2.0 |
| github.com/spf13/pflag | v1.0.9 | BSD-3-Clause |
| github.com/inconshreveable/mousetrap | v1.1.0 | Apache-2.0 (Windows only) |
| github.com/BurntSushi/toml | v1.6.0 | MIT |
| go.yaml.in/yaml/v3 | v3.0.5 | MIT and Apache-2.0 |
| golang.org/x/sync | v0.23.0 | BSD-3-Clause |
| golang.org/x/term | v0.45.0 | BSD-3-Clause |
| golang.org/x/sys | v0.47.0 | BSD-3-Clause |

## Analyzers invoked by packs (not bundled)

| Tool | Pack | License | Acquisition |
|---|---|---|---|
| Go toolchain (`go vet`, compiler) | go | BSD-3-Clause | user-installed toolchain |
| Staticcheck 0.8.1 | go | MIT | `go install` by `pistos plugins install` |
| Ruff 0.16.10 | python | MIT | repository venv, or `pip install` into a managed venv |
| Pyright 1.1.414 / basedpyright 1.40.2 | python | MIT | repository `node_modules`, or managed `npm install --ignore-scripts` |
| Node.js | python, jsts | MIT | user-installed runtime |
| TypeScript | jsts | Apache-2.0 | repository `node_modules` only |
| ESLint | jsts | MIT | repository `node_modules` only |
| Oxlint 1.87.0 | jsts | MIT | repository `node_modules`, or managed `npm install --ignore-scripts` |
| .NET SDK (Roslyn, analyzers) | csharp | MIT | user-installed SDK; the C# facts worker is compiled against its bundled Roslyn |
| gitleaks 8.30.1 | secrets | MIT | `go install` by `pistos plugins install` |
| gopls 0.23.0 | go | BSD-3-Clause | `go install` by `plugins install --lsp` |
| pyright-langserver 1.1.414 | python | MIT | repository or managed npm |
| csharp-ls 0.28.0 | csharp | MIT | `dotnet tool install` by `plugins install --lsp` |
| oxc-parser 0.153.0 | jsts | MIT | npm (`--ignore-scripts`) next to the JS/TS facts worker |
