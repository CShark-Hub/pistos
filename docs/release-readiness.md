# Release readiness (target release, plan §10)

| Criterion | State | Evidence |
|---|---|---|
| init/review/tag/reopen locally and through Docker | ✅ | scenario tests; `TestNativeAndContainerAgree*`, `TestCompleteImageRunsCoreDirectly` |
| Four ecosystems with real deterministic adapters; LSP/metrics documented | ✅ | live pack tests; `docs/packs`, `docs/lsp.md`, `docs/metrics.md` |
| Lazy packs, locked versions, no install/upgrade during review | ✅ | `TestMixedRepositoryStartsOnlyAffectedPacks`, `TestBrokenAndMissingToolsAreIncomplete` |
| Feedback survives diffs and cache deletion; explicit broader preferences | ✅ | `docs/regression-matrix.md` #10–15 |
| BYOK discovery and union triage with fakes; opt-in live endpoint | ✅ | `TestAI*`; one local-model live run recorded (`docs/ai.md`) |
| Reproducible gates, canonical replay, incomplete handling | ✅ | #2, #3, #19 |
| No AI vote selects the plan or creates an exception | ✅ | AI issues advisory; `TestAIOutputCannotChangeDecisionsOrRunCommands` |
| Every issue has evidence/provenance and reproduction | ✅ | `pistos reproduce`; SARIF properties |
| CI/build/schema/SARIF checks, portability tests, benchmarks recorded | ◐ | CI defined but **not executed** (no remote runs); Linux arm64 only; benchmarks in `docs/benchmarks` |
| README, contribution guide, pack protocol, security, limitations | ✅ | README, CONTRIBUTING.md, `docs/*` |

Before tagging a release:

1. Choose the module path (currently the placeholder `example.com/pistos`) and update imports.
2. Run CI on Linux, macOS and Windows; fix Windows process-tree and path issues if any appear.
3. Run the live-pack and docker jobs with `PISTOS_REQUIRE_LIVE=1`.
4. Re-measure benchmarks on the release machine class and adjust the budgets in `internal/cli/perf_test.go`.
5. Evaluate AI variants on a larger labeled corpus and with at least two different models before
   recommending a default AI configuration.
6. Review third-party licenses (`docs/third-party.md`) for any redistributed images.
