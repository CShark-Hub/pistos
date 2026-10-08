# Pack worker protocol (version 1)

Workers are executables started by pistos for checks whose manifest `format` is `"worker"`.

- Transport: JSON-RPC 2.0, one UTF-8 JSON object per line on stdin/stdout. This is **not** LSP framing.
- Frames larger than 16 MiB are a protocol violation; pistos kills the worker.
- stderr is for logs only and is never parsed.
- Notifications (e.g. `progress`) are ignored for results; only the response to the request counts.

## Methods

| Method | Params | Result |
|---|---|---|
| `initialize` | `{protocol, pack, pack_version}` | `{protocol, pack, pack_version, capabilities[]}` — must echo the same protocol and pack identity |
| `analyze` | `{capability, project, root, tree, options}` | `{status, tree, project, diagnostics[], limitations[]}` |
| `facts` | reserved for milestone 5 | |
| `cancel` | notification `{id}` | none; pistos kills the worker if it does not stop within 2 s |
| `shutdown` | none | `null`, then exit |

`diagnostics[]` items: `{path, line, column, end_line, end_column, rule, severity, message}`; paths are
absolute inside `root` or relative to it.

A result counts as complete only if `status` is `"complete"` **and** `tree`/`project` echo the request.
Crashes, malformed frames, version mismatches, timeouts and cancellation are failed or incomplete
outcomes — never clean.
