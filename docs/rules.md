# Architecture rules

The `imports` check extracts dependency edges inside a project; the core applies cycle and layer rules.

| Pack | Nodes | Edges | Provenance |
|---|---|---|---|
| Go | package directories | module-internal import paths (`go/ast`, non-test files) | resolved; unknown if the target directory does not exist |
| Python | files | `import`/`from … import`, relative imports, literal `importlib.import_module`/`__import__` resolved by file layout (project root, `src/`) | syntax-only; non-literal dynamic imports are unknown |
| JS/TS | files | relative `import`/`export … from`/`import()`/`require()` resolved with TS/JS extensions and `index` files | syntax-only; non-literal dynamic imports and path aliases (`@/`, `~/`, `#`) are unknown |
| C# | namespace directories | `using` directives mapped to the directories that declare the namespace | syntax-only; fully qualified names and reflection are not traced |

External dependencies are not part of the graph. Build constraints (Go), `sys.path` manipulation
(Python), `tsconfig` path aliases and package `exports` (JS/TS) are not resolved.

```toml
[architecture]
gate = false             # true turns violations into blocking warnings
cycles = true
require_complete = false # true: any unresolved import makes the check incomplete (exit 2)

[[architecture.layers]]
name = "domain"
paths = ["internal/domain/**"]

[[architecture.layers]]
name = "infra"
paths = ["internal/infra/**"]

[[architecture.rules]]
from = "domain"
deny = ["infra"]
```

Findings: `arch/layer` at the offending import, `arch/cycle` anchored at the first edge of each
strongly connected component with the cycle path in the message. Unresolved edges are counted in the
check's limitations ("the absence of cycle or layer violations is not proven"); a strict gate needs
`require_complete = true`, which reports the check as incomplete while unknown edges remain.
