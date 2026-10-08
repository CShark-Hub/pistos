# Tutorial

## 1. First review of a Go service

```sh
cd my-service
pistos init                    # detects go.mod; writes .pistos/ (config, feedback, .gitignore)
pistos plugins install         # locks go, acquires Staticcheck 0.8.1; writes .pistos/lock.json
git add .pistos && git commit -m "Configure pistos"
# … edit code …
pistos review                  # working tree (staged, unstaged, untracked) vs HEAD
```

The report lists the scope, every check with its status (`complete`, `cached-complete`,
`incomplete`, …) and issues that are **new** relative to the baseline. `✗` marks blocking issues;
the exit code is 0 (pass), 1 (blocking), 2 (incomplete/config error) or 130 (cancelled).

## 2. Compare a branch

```sh
pistos review --base main                  # working tree vs merge-base(main, HEAD)
pistos review --base main --head feature   # the committed branch only, ignoring local edits
pistos review --all                        # everything, no baseline
pistos review --format sarif > pistos.sarif
```

## 3. Decide about an issue

```sh
pistos review
#  ✗ #3  warning internal/x.go:42:7  fmt.Sprintf format %d has arg … wrong type
pistos tag 3 irrelevant --reason "intentional for the legacy client"
pistos reproduce 3               # evidence, provenance and the exact command
pistos reopen 3                  # undo
pistos feedback                  # list/edit decisions; widen one to a rule/pattern with a preview
```

Decisions live in `.pistos/feedback.yaml` (commit it). They follow the issue through line moves,
reformatting and verified renames. They survive `rm -rf .pistos/cache .pistos/state`. If the code at
the issue changes, or a guard such as `test-only` no longer holds, the issue reappears as
`needs-revalidation`.

## 4. Maintainability gates

Every pack has a `metrics` check (in `checks` by default). New or worsened complexity is reported as
advisory; make it blocking with `[metrics] gate = true`. Add `imports` to `checks` and describe layers to
enforce dependency direction and catch cycles (see `docs/rules.md`).

## 5. Ask the language server

```sh
pistos plugins install --lsp
pistos facts references internal/x.go:42:7
pistos facts diagnostics src/app.ts
```

## 6. Optional AI review

Configure models (see `docs/ai.md`), export the key variable named by `api_key_env`, then:

```sh
pistos review --profile thorough           # discovery passes + triage; replays recorded responses
pistos review --profile thorough --refresh-ai
```

AI issues are advisory, carry provenance (passes, models, triage outcome) and accept `pistos tag`
like any other issue.

## 7. Untrusted pull requests in CI

```toml
[execution]
mode = "docker"
[execution.docker]
profile = "restricted"
```

```sh
pistos plugins install --locked
pistos review --base origin/main --head HEAD --feedback-from-base --format sarif > pistos.sarif
```
