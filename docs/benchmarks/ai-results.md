Model: `google/gemma-4-31B-it` (all variants, temperature 0, max_tokens 2048, identical evidence)

| Variant | Recall | Precision | TP | FP | Accepted singletons | True defects rejected by triage | Raw candidates | Calls | Prompt / completion tokens | Wall time |
|---|---|---|---|---|---|---|---|---|---|---|
| single pass | 0.71 | 1.00 | 5/7 | 0 | 0 | 0 | 5 | 5 | 3629 / 938 | 240 s |
| 3 passes, same model | 0.71 | 1.00 | 5/7 | 0 | 0 | 0 | 15 | 15 | 10887 / 3045 | 259 s |
| 3 passes + model triage | 0.71 | 1.00 | 5/7 | 0 | 0 | 0 | 15 | 19 | 13405 / 3323 | 330 s |

Corpus: `testdata/ai-corpus` (5 cases, 7 seeded defects, one clean decoy file). Local SGLang server on
the benchmark machine, 8 October 2026. All 15 reviews completed (no degraded AI results).

Interpretation (indicative only — the corpus is tiny):

- All variants found the same 5 of 7 defects with no false positives, including on the clean decoy.
- Three passes of the **same model at temperature 0** returned identical candidates (15 raw
  candidates → the same 5 clusters): 3× the tokens for no additional recall. Union only pays off with
  diverse passes — different models, prompts or sampling — which could not be measured here.
- **Model triage** (same model) rejected no true defect, but there were no false claims for it to
  remove; it added 4 calls and ~27% wall time. Its value cannot be judged on this corpus.
- Not measured: mixed-model passes (one general model available), hosted models, larger corpora.
  The configuration default stays conservative: AI is off until configured and advisory when on.
