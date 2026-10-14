# Secrets pack (`secrets`, version 0.1.0)

A repository-wide secret scan. It is a pack, not a language: it is configured as one project at the
repository root and listed in `languages` so configurations stay uniform. Verified with gitleaks
8.30.1 (linux/arm64), natively and in the pack image.

```toml
languages = ["go", "secrets"]

[[projects]]
id = "secrets"
root = "."
ecosystem = "secrets"
checks = ["gitleaks"]
```

`pistos init` adds this project (and `secrets` to `languages`) whatever `--lang` selects. Use
`--no-secrets` to leave it out, or `--lang secrets` alone to configure only the secret scan.
Re-running `init` on an existing configuration without it prints a suggestion and changes nothing.

## Check

| Check | Command (in the snapshot root) | Scope | Output | Exit codes |
|---|---|---|---|---|
| `gitleaks` | `gitleaks dir . --report-format=json --report-path=- --redact --no-banner --no-color --log-level=warn --exit-code=3 --gitleaks-ignore-path=.` | file | `gitleaks-json` | 0 clean, 3 findings; other codes fail |

- The leak exit code is 3 because gitleaks also exits 1 on fatal errors (an invalid `.gitleaks.toml`,
  a partial scan); with the default code 1 a broken configuration would be indistinguishable from
  findings. Exit 1 therefore makes the check **failed**, never clean.
- `--report-path=-` writes the JSON array to stdout (verified); logs go to stderr.
- The check is file-scoped, so the `fast` profile (and pre-commit use) runs it.
- Every finding is `gitleaks/<RuleID>`, severity error, message = the rule description. Columns are
  as gitleaks reports them (8.30.1 reports one column too many on lines after the first).

## What is scanned

Exactly the materialized snapshot: tracked (and, for the working snapshot, untracked non-ignored)
files. Symlinks are not followed (`--follow-symlinks` is off), so the `node_modules` links the JS/TS
pack places in snapshots are skipped; diagnostics outside the snapshot's file list are dropped like
those of every other pack. Archives are not opened (gitleaks default `--max-archive-depth=0`).

Both sides of a review are scanned, so a secret already present in the baseline is `existing` and does
not block; a newly added one blocks. `--all` lists every secret in the tree.

## Configuration honoured

- `.gitleaks.toml` at the repository root (gitleaks loads it from the scan target). Without
  `[extend] useDefault = true` it replaces the default rules.
- `.gitleaksignore` at the repository root. In directory mode its fingerprints are
  `path:rule:line`, so moving a flagged line invalidates its entry.
- `gitleaks:allow` comments on the flagged line.

Both files are declared shared inputs; any change to the tree already invalidates the cached result,
because the project root is the repository root. `GITLEAKS_CONFIG` and `GITLEAKS_CONFIG_TOML` are not
passed to the analyzer (environment allowlist), so results depend only on the repository.

## Secret handling

- gitleaks runs with `--redact`, and the parser decodes only rule, description, file and range:
  `Match`, `Secret`, `Line`, `Fingerprint`, entropy and commit metadata are discarded at parse time.
  Nothing secret reaches findings, the result cache, `.pistos/state` or any output format.
- Findings are marked `"sensitive": true`. The HTML report (static and served) shows
  `[line withheld: possible secret]` instead of every flagged line, also where it appears as context
  of another issue. Text output never prints source lines; SARIF results carry no snippets (they get
  a `sensitive` property).
- AI evidence replaces flagged lines with `[line withheld by pistos: possible secret (<family>)]`
  before any model call, so recorded calls in `.pistos/cache/ai` hold no secret text either. Files
  owned only by the secrets project (outside every language project) are never sent as evidence.
- The identity anchor is the usual hash of the whitespace-normalized flagged line; the line itself is
  not stored. Low-entropy secrets on short lines could in principle be guessed from that hash.
- The materialized snapshot under the pistos cache is a copy of the reviewed files and contains the
  secret like the working tree and the git object store do.

## Tools and acquisition

- `gitleaks`: managed copy (`pistos plugins install` runs
  `go install github.com/zricethezav/gitleaks/v8@v8.30.1` into the pistos tools directory, which needs
  the Go toolchain on `PATH` and network access) or `PATH`. Plain `go install` builds print "version
  is set by build process", so the version is read from the binary's Go build information
  (`version_from: "go-buildinfo"`), falling back to `gitleaks version`.
- Docker mode: the pack image (`internal/packs/secretscan/Dockerfile`, tag `pistos-pack-secrets:0.1.0`)
  builds gitleaks with its version stamped in a digest-pinned golang image and copies the static binary into slim Debian; no language runtime
  is included. Native and container results are compared by `TestNativeAndContainerAgreeSecrets`.

## Not supported

Git history scanning (`gitleaks git`), other secret scanners, per-path selection of the scan (the
project is always the whole repository), and verification of whether a detected credential is live.
