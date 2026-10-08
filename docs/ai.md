# BYOK AI discovery and triage

AI review is **advisory**: AI issues never block, never select the deterministic check plan and never
create feedback decisions. It is disabled until configured and runs only with `--profile thorough`.

```toml
[ai]
enabled = true
required = false          # true: an incomplete AI plan makes the review exit 2 (AI issues still never block)
aggregation = "union"
reuse_results = true

[ai.budget]
max_candidates = 20       # per pass; extra candidates are recorded as rejected
max_tokens = 4096         # sent as max_tokens on every request
max_evidence_chars = 200000

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
api_key_env = "PISTOS_REVIEWER_A_KEY"     # the variable's name; the key itself is never written anywhere
timeout = "120s"
concurrency = 2                            # simultaneous requests to this endpoint
```

## Backend

One backend speaks the OpenAI Chat Completions request shape (`POST {endpoint}/chat/completions`
with `model`, `messages`, `temperature: 0`, `max_tokens`, `response_format: {"type": "json_object"}`;
answers read from `choices[0].message.content` and `usage`). Hosted providers that offer this API and
local servers (vLLM, SGLang, llama.cpp, Ollama's OpenAI endpoint) work unchanged. The key is read from
`api_key_env` at request time, sent only as `Authorization: Bearer …` to that endpoint, removed from
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
