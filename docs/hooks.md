# Git hooks and coding-agent hooks

## Git hooks

```sh
pistos hooks install            # pre-commit and pre-push (or name one: pistos hooks install pre-commit)
pistos hooks status
pistos hooks uninstall
```

`install` writes a tiny script into the directory git uses for hooks (`git rev-parse --git-path hooks`,
so `core.hooksPath` and linked worktrees are respected). The script contains a `# pistos-hook:` marker,
checks `command -v pistos` (missing binary: warning on stderr, exit 0) and otherwise runs
`exec pistos hooks run <kind> "$@"`, so upgrading pistos never needs a reinstall. Install and uninstall
are idempotent; a hook without the marker is never overwritten or removed — add
`pistos hooks run pre-commit` to it yourself.

When another hook manager is detected nothing is written; `install` prints the snippet to add instead:

| Manager | Detected by |
|---|---|
| husky | `core.hooksPath` containing `.husky` |
| lefthook | `lefthook.yml`, `.lefthook.yml`, `lefthook.yaml`, `.lefthook.yaml` |
| pre-commit | `.pre-commit-config.yaml` |

The lefthook snippet (`use_stdin: true`, `{1} {2}`) and the pre-commit snippet were written from
those tools' documentation and have not been run against them here. The pre-commit framework does
not forward git's pre-push stdin, so only its pre-commit stage is offered.

### What runs

| Hook | Review | Profile |
|---|---|---|
| pre-commit | `review --staged`: the index against HEAD | `[hooks] pre_commit_profile` (default `fast`) |
| pre-push | each pushed ref: `review --base <remote sha> --head <local sha>` | `[hooks] pre_push_profile` (default `balanced`) |

`--staged` reads the index git hands to the hook through `GIT_INDEX_FILE`, so `git commit -a` and
`git commit <paths>` are reviewed exactly as they will be committed; without it, the repository's index.
The tree is written from a copy of that index, so neither the user's index nor git's temporary one is
modified. `GIT_INDEX_FILE` is removed from the environment of every other git command pistos runs.
`--staged` may be combined with `--base` (index against `merge-base(base, HEAD)`), not with `--head`
or `--all`.

The fast profile runs only file-scoped checks (for Go: metrics; `go vet` and Staticcheck are
project-aware and skipped). Set `pre_commit_profile = "balanced"` to run them on every commit.

pre-push reads `<local ref> <local sha> <remote ref> <remote sha>` lines. Deletions are skipped. A new
branch is compared against `refs/remotes/<remote>/HEAD` (skipped with a note when that is unknown; run
`git remote set-head <remote> --auto`). A remote sha missing locally falls back to the remote-tracking
ref of the pushed branch, otherwise the ref is skipped with a note. Each ref is reviewed separately;
the last one becomes the current report that `pistos tag N` refers to.

### Exit codes

| Review outcome | Hook |
|---|---|
| blocking issues (also when the review is incomplete) | exit 1, commit/push stopped |
| incomplete or configuration error | warning, exit 0; exit 2 with `[hooks] block_on_incomplete = true` |
| cancelled (Ctrl-C) | non-zero, stopped |
| repository not configured (`pistos init` not run) | note, exit 0 |

Bypass once with git's `--no-verify` or `PISTOS_SKIP_HOOKS=1`. Hook output is the brief text summary
plus the link to `.pistos/state/report.html`.

```toml
[hooks]
pre_commit_profile = "fast"
pre_push_profile = "balanced"
claude_profile = "fast"
block_on_incomplete = false
```

## Claude Code Stop hook

```sh
pistos hooks install claude           # print the settings snippet
pistos hooks install claude --write   # merge it into .claude/settings.local.json
```

```json
{
  "hooks": {
    "Stop": [
      { "hooks": [ { "type": "command", "command": "pistos hooks run claude-stop" } ] }
    ]
  }
}
```

`--write` keeps every existing key and hook (keys are rewritten in sorted order), adds the entry only
once, and refuses to touch a file whose `hooks` / `hooks.Stop` has an unexpected shape.
`pistos hooks uninstall claude` removes only the pistos entry. Claude Code adds
`.claude/settings.local.json` to the global git excludes only when it writes that file itself; if
pistos creates it, ignore it yourself or use `.claude/settings.json` to share the hook with the team.

`pistos hooks run claude-stop` reads the Stop hook JSON on stdin:

- `stop_hook_active: true` (Claude is already continuing because of an earlier block) → exit 0
  without reviewing, so the agent gets one round of feedback per stop and cannot loop forever.
- Otherwise it reviews the working snapshot (profile `[hooks] claude_profile`, default `fast`) with
  `--feedback-from-base`, so decisions the agent writes to `.pistos/feedback.yaml` suppress nothing.
  The repository is located from the input's `cwd`.
- Blocking issues → the `--format agent` report on stderr and exit 2; Claude Code then keeps working
  with stderr as the reason. Clean, incomplete, configuration errors and non-repositories → exit 0
  (incomplete reviews add a one-line note on stderr).

Contract checked against the Claude Code hooks documentation (code.claude.com/docs/en/hooks): Stop
input fields (`session_id`, `transcript_path`, `cwd`, `permission_mode`, `hook_event_name`,
`stop_hook_active`), exit 2 blocking the stop with stderr fed back, other non-zero codes being
non-blocking errors, no matcher for Stop, default command timeout 600 seconds. A missing `pistos`
binary therefore surfaces as a non-blocking error in Claude Code.

## Other coding agents

Any agent runner that can execute a command when the agent finishes a turn can use the same pieces:

```sh
pistos review --format agent --feedback-from-base --profile fast --no-html
```

Exit codes are the review's (`0` pass, `1` blocking, `2` incomplete/configuration, `130` cancelled);
stdout is the compact agent report. Map exit `1` to whatever the runner uses to send text back to the
agent, and treat `2` as an environment problem to show the user. How Cursor, Codex or other tools
configure such hooks, and how they pass output back to the model, has not been verified here; consult
their documentation. `pistos hooks run claude-stop` is specific to Claude Code's Stop hook contract.
