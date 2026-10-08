# Pack author guide

A pack is data plus optional worker sources, embedded in the pistos binary under `internal/packs/<name>/`.

## Manifest (`manifest.json`, schema 1)

```json
{
  "schema": 1, "name": "go", "version": "0.1.0", "languages": ["go"],
  "test_paths": ["**/*_test.go"],
  "options": ["tags"],
  "env_passthrough": ["GOPATH"],
  "shared_inputs": ["go.work"],
  "tools": {
    "go": {"command": "go", "lookup": ["path"], "version_args": ["env", "GOVERSION"], "version_regex": "^go(\\S+)"}
  },
  "checks": {
    "go-vet": {"tool": "go", "args": ["vet", "-p={jobs}", "-json", "-tags={opt:tags}", "./..."],
               "format": "go-vet", "scope": "project", "weight": 2,
               "exit_codes": {"0": "clean", "1": "findings"}, "env": {"GOPROXY": "off"}}
  },
  "aliases": {"staticcheck/SA5009": "go-vet/printf"},
  "authority": ["go-vet", "staticcheck"],
  "lsp": {"tool": "gopls", "args": [], "languages": {".go": "go"}},
  "container": {"dockerfile": "go/Dockerfile", "files": [], "tools": {"go": "/usr/local/go/bin/go"}}
}
```

Rules (enforced by `ParseManifest`; unknown fields are rejected):

- **Tools** list a `lookup` order: `repo:<dir>`, `node-package:<pkg>:<bin>` (run with the `runtime`
  tool, e.g. node), `managed`, `path`. A tool with `acquire` can be installed by `plugins install`
  (`go-install`, `npm`, `pip`, `embedded`, `dotnet-embedded`, `dotnet-tool`). `version_from: "pack"` or
  `"package-json"` avoids running tools that have no version flag.
- **Checks** use argument arrays — never shell strings. Placeholders: `{jobs}`, `{cache}`, `{root}`,
  `{opt:NAME}` (project option) and `{var:NAME}` (computed by pistos); an argument whose placeholder is
  empty is dropped. Every exit code not listed in `exit_codes` is a failure.
- **Formats** are parsers in `internal/plugin/formats*.go`. A parser must fail on malformed output and
  set `Incomplete` whenever coverage is partial; "no diagnostics" must only come from a clean run.
- **Facts checks** (`"facts": "metrics" | "imports"`) are served by a worker (`"format": "worker"`) or
  by `builtin-go`.
- `unsupported` declares a selectable backend that is not integrated, so selecting it is reported
  instead of silently substituted.

## Workers

Custom adapters run out of process over newline-delimited JSON-RPC 2.0 (`docs/worker-protocol.md`):
`initialize`, `analyze` or `facts`, `cancel`, `shutdown`. Echo the tree and project you analyzed;
pistos treats a mismatch as incomplete. Facts must follow `docs/metrics.md` and `docs/rules.md`; add
hand-computed fixtures to `testdata/fixtures/metrics-langs` and expectations to
`internal/cli/metrics_langs_test.go`.

## Checklist for a new pack or tool version

1. Verify flags and output against the **pinned** version's documentation and real output; capture
   samples into parser tests.
2. Document runtime requirements, exit codes, scope and unsupported features in `docs/packs/<name>.md`.
3. Add clean, defective and configuration-preserving live tests (they skip without the runtime and
   fail under `PISTOS_REQUIRE_LIVE=1`).
4. Pin base images by digest in the pack's Dockerfile, include only the pack's own runtimes, and add it
   to `TestPackImagesExcludeUnrelatedRuntimes` and a native/container equivalence test.
5. Bump the pack `version` whenever behaviour changes; embedded workers are re-installed and stale
   ones are refused.
