# BYOK AI discovery and triage

AI review is **advisory**: AI issues never block, never select the deterministic check plan and never
create feedback decisions. It is disabled until configured and runs only with `--profile thorough`.

## Setup

`pistos ai setup` configures it interactively; `pistos plugins install` offers to run it at the end
when AI is off and the session is interactive. The flow:

1. **Provider**: any OpenAI-compatible endpoint, or OpenRouter (one key for models from many vendors,
   with context sizes and prices shown while picking; only models that accept `response_format` are
   offered).
2. **Endpoint** (OpenAI-compatible only): a missing scheme, trailing slash or full
   `…/chat/completions` URL is normalized; HTTPS is required except for `localhost`/`127.0.0.1`, and
   reachability is checked.
3. **Key**, read without echo on a terminal and verified without spending tokens (`GET /models`,
   or OpenRouter's `GET /key`); a refused key is asked again, three refusals abort. An existing
   `$OPENROUTER_API_KEY`, `$OPENAI_API_KEY` or saved key is offered first. Local servers may have no key.
4. **Key storage**: config.toml gets only the variable name. The key either stays in your environment
   or is saved in your user configuration directory (`pistos/credentials.json`, mode 0600;
   `PISTOS_CREDENTIALS_FILE` relocates it), which is used when the variable is unset.
5. **Models per review** (default 3, the union + triage design point), then one model per pass,
   searched by name or picked by number; pass N defaults to pass N−1's model.
6. **Triage**: a model (default for 2+ passes) or evidence only.
7. A preview of the tables, with an estimated cost per review when the catalog has prices (an upper
   estimate: every call with the full evidence budget and its whole completion allowance), then they
   are written. Catalog prices are written as `prompt_usd_per_mtok` / `completion_usd_per_mtok`.
   `[ai]`, `[[ai.discovery]]`, `[ai.triage]` and `[models.*]` are replaced; `[ai.budget]`,
   `[[ai.instructions]]` and everything else are left untouched. With `max_review_usd` set, setup
   refuses models without published prices.

Manual configuration:

```toml
[ai]
enabled = true
required = false          # true: an incomplete AI plan makes the review exit 2 (AI issues still never block)
aggregation = "union"
reuse_results = true

[ai.budget]
max_candidates = 20       # per pass; extra candidates are recorded as rejected
max_tokens = 4096         # sent as max_tokens on every request (a model's own max_tokens wins)
max_evidence_chars = 200000
max_review_tokens = 0     # per-review spend cap in tokens; 0 = unlimited
max_review_usd = 0.50     # per-review spend cap in USD; needs prices on every alias used; 0 = unlimited

[[ai.instructions]]       # optional; without entries .pistos/review.md is used when it exists
file = ".pistos/review.md"
[[ai.instructions]]
file = "docs/api-review.md"
paths = ["internal/api/**"]   # only when an evidence file matches

[[ai.discovery]]
id = "pass-1"
model = "reviewer-a"
[[ai.discovery]]
id = "pass-2"
model = "reviewer-b"
[[ai.discovery]]
id = "pass-3"
model = "reviewer-c"

[ai.triage]
mode = "model"            # or "evidence" (no model call)
model = "triager"
keep_supported_singletons = true

[models.reviewer-a]
backend = "openai-compatible"
endpoint = "https://api.example.com/v1"   # HTTPS, or http://localhost / 127.0.0.1 for local servers
model = "provider-model-id"
api_key_env = "PISTOS_REVIEWER_A_KEY"     # the variable's name; the key itself is never in the repository
timeout = "120s"
concurrency = 2                            # simultaneous requests to this endpoint
prompt_usd_per_mtok = 0.15                 # optional prices (USD per million tokens) for estimates and caps
completion_usd_per_mtok = 0.60
```

## Backend

One backend speaks the OpenAI Chat Completions request shape (`POST {endpoint}/chat/completions`
with `model`, `messages`, `temperature: 0`, `max_tokens`, `response_format: {"type": "json_object"}`;
answers read from `choices[0].message.content` and `usage`). Hosted providers that offer this API and
local servers (vLLM, SGLang, llama.cpp, Ollama's OpenAI endpoint) work unchanged. The key is read from
`api_key_env` at request time (falling back to a key saved by `pistos ai setup` under that name), sent only as `Authorization: Bearer …` to that endpoint, removed from
every error message, and never placed in analyzer environments, config, lock, feedback, cache, state or
reports. Repository `.env` files are never loaded. HTTP 429/5xx and network errors are retried at most
twice with backoff; attempts are recorded in telemetry.

## Flow

1. **Evidence**: the changed files of analyzed projects in the head snapshot (every source file for
   `--all`), line-numbered and capped by `max_evidence_chars`, plus the deterministic findings and a
   summary of approved suppressing decisions. Pistos' own `.pistos/` files are never evidence.
2. **Independent discovery**: every pass receives the same evidence in a fresh request; passes run
   concurrently (bounded per endpoint) and never see each other's output. The prompt marks repository
   content as untrusted data; model output is only parsed as data — it cannot run commands or change
   decisions.
3. **Validation**: a candidate needs a problem family, an anchor on an existing line of an evidence file,
   a hypothesis, assumptions, impact, a failing scenario and resolvable `file:path:line` evidence
   references. Invalid or over-budget candidates are recorded as `rejected` with a reason.
4. **Union**: candidates are clustered by issue identity — project, family, path, enclosing symbol and the
   anchor line's normalized text — not by wording or line proximity. Every raw candidate keeps its
   cluster; repeats are `duplicate`. Single-pass candidates survive (`keep_supported_singletons`).
5. **Triage**: `evidence` mode marks well-formed clusters `supported`; `model` mode sends neutral ids,
   hypotheses, assumptions and ±25 lines of code — no pass or model identity, no vote counts — and asks
   the triager to refute. `refuted` → `rejected` (kept in the audit list), `unresolved` →
   `needs-investigation`. A triage failure leaves clusters `needs-investigation`, never rejected. Only a
   deterministic finding on the same line makes a cluster `verified`.
6. **Publication**: surviving clusters become advisory AI issues with merged provenance (passes, models,
   assumptions, failing scenario); feedback decisions apply to them like to any other issue.

A failed or offline pass is a degraded result (`incomplete`), never a vote of "no issues".

## Review guidelines

Repository-specific review guidance (focus areas, conventions, known pitfalls) goes in
`.pistos/review.md`, which is used automatically when it exists and no `[[ai.instructions]]` entry
is configured. Other files, such as `AGENTS.md` or `CLAUDE.md`, are used only when listed. An entry
with `paths` (globs relative to the repository root, same syntax as project include/exclude) applies
only when one of them matches a file in the evidence. Files must be clean repository-relative paths;
unknown keys are rejected.

- **Placement**: each file is appended to the discovery and triage system prompts as
  `## Repository review guidelines (from <file>)`, after the built-in rules and under an explicit
  statement that guidelines may add focus areas and conventions but never override those rules
  (repository content is untrusted data, no commands, no decision changes, the fixed answer format).
- **Source**: read from git, not the working directory: from the reviewed snapshot normally, from the
  baseline under `--feedback-from-base` (see docs/security.md). The AI report lists each file used
  (`guidelines`: file, side, tree, blob, bytes, truncated); listed files missing from that tree are noted.
- **Size**: at most 16,000 characters in total, in configuration order; the file that crosses the cap
  is cut (marked `[truncated by pistos]`), later ones are sent empty, and a note says so.
- **Replay**: guidelines are part of the system messages, which are part of the replay key, so
  editing a guideline re-runs discovery and triage; unchanged guidelines replay. The prompt names the
  file but not the tree, so unrelated commits do not invalidate recordings.

## Spend cap

`[ai.budget] max_review_tokens` and `max_review_usd` cap what one review may spend on provider calls
(`pistos review --max-ai-usd N` overrides the USD cap; `0` lifts it). The USD cap fails closed: a
model alias used by discovery or triage without both prices is a configuration error naming it.

- **Reservation**: before a call that is not a replay, pistos reserves a conservative estimate: one
  token per three prompt characters (bytes) plus the full `max_tokens` completion allowance, priced
  with the alias's prices. When a cap is set and no `max_tokens` is configured, requests are bounded
  at 4096 completion tokens (this bound is not part of the replay key). If charged plus reserved spend
  would exceed a cap, the call is not made: the pass is `skipped` with an error starting
  `skipped: AI budget`. A skipped pass is never a clean vote; the AI status is incomplete, and with
  `ai.required` the review exits 2.
- **Order**: discovery passes reserve in configuration order before any of them starts, so the
  passes a cap skips are always the last configured ones. Model triage reserves after discovery has
  settled; a skipped triage leaves every cluster `needs-investigation`.
- **Settlement**: a successful call is charged its reported usage. Attempts that failed, or a
  success without reported usage, stay charged at their reservation: they may have been billed.
- **Retries**: every retry of a transient failure reserves another attempt first; when the cap has
  no room, the call stops retrying and fails (`not retried: skipped: AI budget`). Retries run inside
  concurrent passes, so which retry is refused can vary; the first attempts cannot.
- **Replays** cost nothing and are never refused, so an exhausted cap still replays offline.
- **Reporting**: JSON `telemetry.ai_spend` (`calls`, `replayed`, `prompt_tokens`,
  `completion_tokens`, `charged_tokens`, `charged_usd`, `usd_known`, `max_review_tokens`,
  `max_review_usd`, `skipped`) and per-call `estimated_tokens` / `skipped` in `telemetry.ai`. It is
  volatile telemetry, not part of the canonical report (which already records skipped passes). The
  text output prints a spend line under `ai`.

USD figures are estimates from configured prices, not invoices. Not built yet: daily or monthly
cumulative caps across reviews, and using a provider's reported cost (OpenRouter's `usage.cost`)
instead of token × price.

## Replay and lifecycle

Each call's raw artifact (exact messages sent, response, usage) is stored under `.pistos/cache/ai/`.
Identical evidence, prompts and model settings replay the recording with zero provider calls, also
under `--offline`. The replay key deliberately excludes the approved-decisions context, so a tag-only
edit replays; `--refresh-ai` makes fresh calls, which receive the updated context.

Accepted issues (`verified`, `supported`, `needs-investigation`) are kept in `.pistos/state/ai-issues.json`
for their snapshot. A refreshed run that omits one keeps it with a "not rediscovered" note — omission is
not refutation; only an explicit refutation, a decision or changed code removes it. On a new snapshot an
earlier issue is carried as `needs-investigation` while its anchor line still exists, and dropped once
that code changed.

## Live test (opt-in)

Tests use a fake HTTP server; no key is needed. To exercise a real endpoint with the fixture code only:

```sh
PISTOS_LIVE_AI_ENDPOINT=http://localhost:8000/v1 PISTOS_LIVE_AI_MODEL=<model-id> \
  [PISTOS_LIVE_AI_KEY_ENV=NAME_OF_KEY_VARIABLE] go test ./internal/cli -run TestAILive -v
```

Recorded run (8 October 2026): local SGLang serving `google/gemma-4-31B-it`, one pass, 81 s; it reported
the seeded out-of-bounds loop and nil dereference (both `supported`) and ignored the prompt-injection
comment in the fixture. No hosted/paid model has been exercised.

Variant comparison (`docs/benchmarks/ai-results.md`): on a 7-defect labeled corpus with the same local
model, one pass, three same-model passes and three passes with model triage all reached recall 0.71 and
precision 1.00. At temperature 0, repeating the same model adds cost but no diversity, so prefer
different models (or prompts) per pass when the budget allows three passes.
