# Contributing

- Go 1.26+; keep the core CGo-free (`CGO_ENABLED=0 go build ./...`).
- Before sending a change: `gofmt -l .` (must be empty), `go vet ./...`, `go test -race ./...`.
- Live tests need real runtimes (Go, Node, Python, .NET, Docker); they skip with a reason when one is
  missing. CI's live jobs set `PISTOS_REQUIRE_LIVE=1`, which turns skips into failures.
- Tests never use real API keys or paid models. AI behaviour is tested against `internal/testutil/fakeai`;
  `TestAILive` is opt-in.
- Comments explain *why*, not *what*. Prefer explicit data structures and small interfaces at real
  boundaries (processes, HTTP, git).
- Parsers must fail closed: malformed or partial tool output is a failure or `incomplete`, never clean.
- New packs and tool versions: follow `docs/pack-authors.md`.
- Update `docs/CHECKLIST.md` and the relevant docs together with behaviour changes.
