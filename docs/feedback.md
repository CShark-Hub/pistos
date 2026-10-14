# Review decisions (`.pistos/feedback.yaml`)

`pistos tag <issue> <tag>...` records a decision; `pistos reopen <issue>` removes its suppressing tags;
`pistos feedback` lists and edits decisions interactively (`--list` for scripts). The file is committed;
cache and state can be deleted at any time without losing a decision.

## Tags

| Tag | Effect |
|---|---|
| `irrelevant`, `false-positive`, `accepted-risk` | Suppressed from normal output and the gate; still listed with `--include-ignored` and in JSON |
| `deferred` | Stays visible and gating; tracked as deferred |
| `test-only` | Label; additionally records a guard that the path must remain test code |
| anything else (`security`, …) | Label only; no policy effect |

## Matching

A decision applies to an issue when, after both lanes produced findings:

1. the canonical key equals the decision subject (same project, family, path, enclosing symbol,
   whitespace-normalized anchor line and ordinal — line numbers, wording, severity and model are
   not part of identity); or
2. the same selectors match at a new path and the move is a **verified rename** (identical file blob,
   or git rename detection from the decision's origin tree/commit to the reviewed tree); or
3. a `pattern` decision covers the family (canonical or any source rule) under its path globs.

Guards are rechecked on every review. When a guard fails (e.g. a `test-only` fixture moved into
production code) or the anchor line changed in the same function, the issue stays visible with state
`needs-revalidation`; nothing is silently suppressed. Decisions for code outside the current diff are
never removed.

## Scope

The default scope is *this issue*. Wider *rule/pattern* scope needs an explicit choice: `pattern N` in
`pistos feedback` (preview + confirmation) or `pistos tag N TAG --scope pattern --paths GLOB --yes`
after the printed preview.

## From the browser

`pistos review` writes `.pistos/state/report.html` (self-contained, offline) with a highlighted code
excerpt for every issue, and prints its `file://` link plus the platform's open command (`open`,
`xdg-open`, `explorer.exe`, `wslview`; over SSH, a port-forwarding hint). `pistos report` serves the
current report on `http://127.0.0.1:41211/` (`--port N` to change it, `--port 0` for a free one)
and opens it: select issues, choose per issue **this exact
issue** (issue scope) or **this kind of issue** (pattern scope: same family in the same project,
optionally limited to path globs; the dialog shows how many current issues it covers), pick
`irrelevant`, `false-positive` or `accepted-risk` and an optional reason, and save. Saving goes
through the same code as `pistos tag`; selections of the same kind become one pattern decision.
The server only accepts its own `Host` and a per-run token sent in a custom header, so other sites
cannot write decisions. The static file cannot write; it shows the equivalent `pistos tag` commands.
`tag` and `reopen` regenerate an existing `report.html`.

## Concurrency and edits

Mutations hold `.pistos/feedback.yaml.lock`, re-read the file, change only the affected entries in
the YAML node tree (comments and unrelated entries are preserved) and write atomically. An invalid
manual edit makes `review` exit 2 with the offending entry and is never overwritten by `tag`.

## Untrusted changes

`pistos review --feedback-from-base` applies the baseline's committed decisions; any edit the change
makes to `feedback.yaml` is reported but not applied, so a change cannot exempt itself.
