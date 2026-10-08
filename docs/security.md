# Trust boundaries and execution profiles

Pistos runs third-party analyzers, the project's own build logic (MSBuild targets, TypeScript
plugins, ESLint configs, Python imports evaluated by type checkers) and optional model calls. Choose
the profile by how far you trust the code under review.

## Trusted local profile (default: `execution.mode = "native"`)

For your own working tree and trusted branches.

- Analyzers run as your user, with an environment built from an allowlist (PATH, HOME, locale, temp,
  and per-pack variables such as GOPATH or NUGET_PACKAGES). Provider API keys and other secrets are
  never passed to analyzers.
- Reviews never download: Go runs with `GOPROXY=off -mod=readonly GOTOOLCHAIN=local`, NuGet restore
  uses only the local package folder, `npm`/`pip` run only inside `pistos plugins install`, always with
  install scripts disabled for npm.
- Every process runs in its own process group, with output caps and timeouts. The whole tree is
  killed on cancellation.
- **Native mode is not a sandbox.** A malicious project can still reach the network or your files
  through build logic that tools execute (MSBuild targets, ESLint plugins, `conftest.py` imports).

## Restricted untrusted-analysis profile (`mode = "docker"`, `profile = "restricted"`)

For pull requests from untrusted contributors.

- `--network none`, non-root (`--user <uid>:<gid>`), `--read-only` root filesystem with a private
  `/tmp`, `--cap-drop ALL`, `--security-opt no-new-privileges`, `--pids-limit`, `--memory`, `--cpus`.
- Only the snapshot (read-only), the task's scratch directory and dependency caches (read-only) are
  mounted. The user's checkout, home directory and credentials are not.
- The container environment holds only analyzer settings; no provider secrets.
- Containers are named and force-removed on cancellation or timeout (tested).
- Combine with `pistos review --feedback-from-base`, so the change cannot exempt itself through
  `feedback.yaml`.
- A container is a strong isolation layer but not a complete security boundary: kernel and container
  runtime vulnerabilities, resource exhaustion inside the limits, and malicious *base images* are out
  of scope. Pin images (done by digest) and keep Docker updated. For hostile code, run on a disposable
  VM or CI runner.

## AI with untrusted code

The provider client runs in the pistos core on the host, never inside the analysis container. It
receives only curated evidence: changed source files, deterministic findings and decision
summaries. API keys stay in the core process.

Repository content is marked as untrusted data in every prompt. Model output is parsed as data only:
it cannot run commands, edit files or create decisions, and invalid anchors are rejected. Sending
untrusted code to a hosted model is still disclosure of that code to the provider. Use a local
endpoint, or keep AI disabled, when that matters.
