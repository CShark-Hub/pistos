| Environment | |
|---|---|
| arch | arm64 |
| cpu | 0xd87 |
| cpus | 20 |
| date | 2026-10-08 |
| dotnet | 10.0.401 |
| go | go version go1.26.9 linux/arm64 |
| memory | 127535272 kB |
| node | v24.21.0 |
| os | linux |
| pistos | pistos version 0.1.0-dev |
| python | Python 3.12.3 |

Startup (`pistos --version`): p50 2.7 ms, p95 3.2 ms (25 runs)

| Scenario | Source files / lines | Install | No-op review p50/p95 | Cold review p50/p95 | Warm function edit p50/p95 | Peak RSS (all processes) | 3-run canonical identical |
|---|---|---|---|---|---|---|---|
| go-small | 1 / 11 | 35 ms | 18 / 19 ms | 782 / 782 ms | 113 / 114 ms | 258 MB | true |
| go-medium (pistos itself) | 89 / 15615 | 34 ms | 26 / 29 ms | 1758 / 1758 ms | 989 / 1056 ms | 535 MB | true |
| python-small | 3 / 11 | 7154 ms | 19 / 20 ms | 471 / 471 ms | 477 / 487 ms | 705 MB | true |
| jsts-small | 3 / 7 | 1376 ms | 19 / 20 ms | 248 / 248 ms | 272 / 293 ms | 232 MB | true |
| csharp-small | 2 / 19 | 1808 ms | 18 / 19 ms | 1593 / 1593 ms | 1674 / 1711 ms | 320 MB | true |
