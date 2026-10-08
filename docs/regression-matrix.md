# Mandatory regression suite (plan §9) → tests

Status: ✅ automated and passing in this environment · ◐ automated but only partly covered or only
compile-checked here.

| # | Requirement | Tests | Status |
|---|---|---|---|
| 1 | No changes: no analyzer/model starts, explicit scope | `TestNoChangeReviewStartsNothing`, `TestAINoChangeMakesNoCalls` | ✅ |
| 2 | Same canonical output across runs and job counts | `TestReviewIsDeterministicAcrossRunsAndJobs` (jobs 1/2/4, five runs); bench 3-run check for every scenario | ✅ |
| 3 | Crash, truncated output, timeout → incomplete | `TestWorkerCheckFailuresAreNeverClean`, `TestWorkerFailures`, `TestTimeoutAndTruncationAreIncomplete`, `TestBrokenAndMissingToolsAreIncomplete` | ✅ |
| 4 | Findings exit code ≠ execution failure | `TestBrokenAndMissingToolsAreIncomplete`, `TestParseGoVetFailureWithoutDiagnostics`, `TestTscText`, `TestSARIFArtifacts` | ✅ |
| 5 | Config, imports, dependencies, targets, generated inputs invalidate caches | `TestCacheReuseAndInvalidation` (source, go.mod, generated file, deletion, rename, build tags, GOOS) | ◐ stubs/lockfiles covered by the same project-subtree hash, not by a dedicated case |
| 6 | Incremental no-op keeps diagnostics; baseline cannot contaminate head | `TestCSharpPackLive` (rebuild after cache loss), `TestPreexistingIssueIsNotNew`; per-side scratch and tree directories | ✅ |
| 7 | Snapshot changes during analysis isolated | `TestWorkingTreeCapturesAllChangesWithoutTouchingIndex`, `TestWorkingTreeSeesSameSizeEditRightAfterCommit` | ✅ |
| 8 | Unselected packs start nothing | `TestMixedRepositoryStartsOnlyAffectedPacks`, `TestUnrelatedFileChangeSkipsProject`, `TestReviewNeverStartsLanguageServers` | ✅ |
| 9 | Cancellation cleans process trees (Unix, Windows) | `TestCancellationLeavesNoChildren`, `TestRunCancelKillsTree`, `TestWorkerCancellationKillsProcessTree`, `TestContainerCancellationRemovesContainer`; `TestRunTimeoutKillsTreeWindows` | ◐ Windows test compile-checked only |
| 10 | `irrelevant` survives new diff, refs, AI refresh, cache/state deletion | `TestTagPersistsAcrossDiffsRefsMovesAndCacheDeletion`, `TestAIReplayTaggingAndRefreshLifecycle` | ✅ |
| 11 | Formatting, line shifts, verified renames keep matching | `TestTagPersistsAcrossDiffsRefsMovesAndCacheDeletion`, `TestVerifiedRenameKeepsDecision`, `TestAnchorIgnoresWhitespaceOnly` | ✅ |
| 12 | Changed assumptions revalidate; nearby issues stay visible | `TestChangedCauseAndNearbyDefectStayVisible`, `TestTestOnlyGuardInvalidatedByMovingToProduction`, `TestChangedAnchorRequestsRevalidation` | ✅ |
| 13 | Pattern scope only; ordinary tags don't change verdicts | `TestPatternScopeNeedsConfirmation`, `TestPatternScopeAndLabels`, `TestOrdinaryLabelDoesNotChangeVerdictAndDeferredStillGates` | ✅ |
| 14 | Reopen restores visibility; other decisions kept | `TestReopenRestoresVisibilityAndKeepsOtherDecisions` | ✅ |
| 15 | Concurrent tags keep both entries and manual content | `TestConcurrentUpdatesKeepEveryEntry`, `TestUpdatePreservesCommentsAndManualEntries` | ✅ (in-process concurrency through the file lock) |
| 16 | AI wording/model/line changes keep identity | `TestAIShapedCandidatesKeepTheirDecision`, `TestAIReplayTaggingAndRefreshLifecycle`, `TestKeyIgnoresLineMessageAndTool` | ✅ |
| 17 | Union retains unique discoveries; cluster+outcome per candidate | `TestAIUnionKeepsEveryDiscoveryWithProvenance` | ✅ |
| 18 | Silence cannot close; failed passes are not negative votes | `TestAIReplayTaggingAndRefreshLifecycle`, `TestAIFailureIsNotACleanVote` | ✅ |
| 19 | Replay makes zero calls; tag-only edits need no rediscovery | `TestAIReplayTaggingAndRefreshLifecycle`, `TestAIOfflineReplaysButNeverCalls` | ✅ |
| 20 | Prompt injection treated as data | `TestAIOutputCannotChangeDecisionsOrRunCommands`; live run ignored the injection comment | ✅ |
| 21 | Keys absent from analyzer env, config, logs; redaction | `TestAIKeysNeverPersistOrLeak`, `TestAnalyzerEnvironmentExcludesSecrets`, `TestRunDoesNotInheritEnvironment`, `TestRestrictedProfileIsEnforced`, `TestParseRejects` | ✅ |
| 22 | Native/container agree; target-specific builds qualified | `TestNativeAndContainerAgree{Go,Python,JSTS,CSharp}`, `TestCompleteImageRunsCoreDirectly` | ◐ qualification notes (cgo off in the Go image, no host virtualenv) are documented, not asserted |

Run everything: `PISTOS_REQUIRE_LIVE=1 go test -race -count=1 ./...` with Go 1.26.9 (+ Staticcheck 0.8.1),
Node 24, Python 3.12, .NET SDK 10 and Docker on PATH. Opt-in: `TestAILive` (needs an endpoint),
`TestPerformanceBudget` (`PISTOS_PERF_BUDGET=1`).
