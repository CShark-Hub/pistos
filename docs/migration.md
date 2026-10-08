# Schema and migration notes

| File | Current schema | Compatibility |
|---|---|---|
| `.pistos/config.toml` | `schema = 1` | Unknown keys and other schema numbers are rejected with a message; there is no implicit upgrade. |
| `.pistos/feedback.yaml` | `schema: 1` | A file without `schema` (pre-release drafts) is read as schema 1 with `version: 1` on every decision; the migration is in `feedback.migrate` and tested (`TestMigratesUnversionedFile`). Unknown fields are rejected and invalid files are never rewritten. |
| `.pistos/lock.json` | `schema: 1`, `protocol: 1` | Other values are rejected; re-run `pistos plugins install`. |
| Worker protocol | 1 | `initialize` must echo protocol 1 and the pack identity. |
| Issue keys | `k1-…` | The key version is part of every key; a future `k2` must ship a mapping for existing decisions. |
| Metrics | `metrics-v1` | Recorded in every metric finding; changed definitions bump the algorithm. |
| Caches/state | internal | Safe to delete at any time; entries with another schema are ignored. |

When a schema changes: keep reading the previous schema, migrate in memory, write only the new
schema on an explicit mutation, and add a fixture-based migration test.
