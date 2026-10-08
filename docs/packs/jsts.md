# JavaScript/TypeScript pack (`jsts`, version 0.1.0)

Verified with Node 24.21.0, TypeScript 7.0.2 (native compiler), ESLint 10.12.0, Oxlint 1.87.0.

| Check | Command | Scope | Output | Exit codes |
|---|---|---|---|---|
| `tsc` | `node <typescript>/bin/tsc --noEmit --pretty false -p <tsconfig>` | project | `tsc-text` | 0 clean, 1/2 findings |
| `eslint` | `node <eslint>/bin/eslint.js --format json .` | file | `eslint-json` | 0, 1; 2 (config/crash) fails |
| `oxlint` | `node <oxlint>/bin/oxlint --format json .` | file | `oxlint-json` | 0, 1 |
| `biome` | — | — | — | **unsupported** in 0.1.0 |

## Repository-selected tools

TypeScript and ESLint are only taken from the repository's `node_modules` (project directory and its
ancestors, for hoisted workspaces). Pistos never substitutes another compiler version or linter; if they
are not installed, `plugins install` and `review` report them as not found. Install them with your
package manager. Oxlint may additionally be acquired (pinned 1.87.0, npm `--ignore-scripts`).
`pistos init` selects ESLint, Biome or Oxlint according to the repository's dependencies/config files.

## Dependencies in snapshots

Snapshots do not contain `node_modules`; review links the repository's installed `node_modules`
(project and ancestors) into the snapshot. `node_modules/.package-lock.json` (npm), `.modules.yaml`
(pnpm) or `.yarn-state.yml` (Yarn) — or a directory listing — is part of the cache key.

## Configuration

`tsconfig.json` (or `options.tsconfig`) and the ESLint/Oxlint config files are used as found in the
snapshot; shared root configs (`tsconfig.base.json`, `package.json`, linter configs) in ancestor
directories are cache inputs. Project references are checked only when each referenced project is a
pistos project of its own. tsc offers no JSON output; `--pretty false` text is parsed strictly and a
project-level error (e.g. TS5058) makes the result incomplete.

Aliases: `eslint/no-unused-vars` ≡ `oxlint/eslint/no-unused-vars` (`js/unused-variable`).
