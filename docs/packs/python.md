# Python pack (`python`, version 0.1.0)

Verified with Python 3.12.3, Ruff 0.16.10 (pip), Pyright 1.1.414 (npm) on Node 24.21.0.

| Check | Command (project root of the snapshot) | Scope | Output | Exit codes |
|---|---|---|---|---|
| `ruff` | `ruff check --output-format json --no-cache .` | file | `ruff-json` | 0 clean, 1 findings; 2 fails |
| `pyright` | `node <pyright>/index.js --outputjson --threads=N [--pythonpath=…]` | project | `pyright-json` | 0, 1; 2/3/4 fail |
| `basedpyright` | same flags, `basedpyright` npm package | project | `pyright-json` | 0, 1 |
| `ty`, `mypy` | — | — | — | **unsupported** in 0.1.0: selecting them reports `unsupported` (exit 2) instead of substituting another checker |

`pistos init` keeps the repository's choice: `[tool.pyright]`/`pyrightconfig.json` → pyright,
`basedpyright` → basedpyright, `[tool.ty]` → ty, `[tool.mypy]` → mypy; pyright otherwise.

## Tools

- Ruff: the repository's `.venv/bin/ruff` or `venv/bin/ruff`, otherwise a managed copy installed by
  `pistos plugins install` (`python3 -m venv` + `pip install ruff==0.16.10`). PATH is deliberately not
  searched: the snap build of ruff cannot read files outside its sandbox and prints `[]` with exit 0.
  The parser treats any "Encountered error" on stderr as **incomplete** for the same reason.
- Pyright/basedpyright: the repository's `node_modules` package, otherwise a managed npm install
  (`--ignore-scripts`), run as `node <entry>`; Node is taken from PATH and locked.

## Interpreter and imports

The project interpreter is `options.python` (repository-relative or absolute) or an auto-detected
`.venv/`/`venv/` in the project or repository root. It is passed as `--pythonpath`, and the installed
distributions (`*.dist-info`) are part of the cache key. Without an interpreter pyright resolves
imports against its default Python and the result carries that limitation.

Pyright ranges are zero-based and converted to one-based lines/columns. A run that analyzed zero
files is incomplete. Dynamic behaviour (monkey-patching, `__getattr__`, runtime imports) is not
modelled beyond what the type checker supports.

Aliases: `ruff/F401` ≡ `pyright/reportUnusedImport` (`python/unused-import`), `ruff/F821` ≡
`pyright/reportUndefinedVariable`. Symbols for identity come from `def`/`class` indentation.
