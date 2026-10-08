# AI evaluation corpus

Small, hand-labeled cases for comparing AI review variants. Each directory is reviewed as a change
on top of an empty base commit. `labels.json` lists the seeded defects (path, line, description); a
reported issue counts as a true positive when it is on the same file within ±3 lines of a label.
`go-clean` has no seeded defects and measures false positives. The corpus is deliberately tiny and
only indicative; it is not a benchmark of model quality.
