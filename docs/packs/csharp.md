# C# pack (`csharp`, version 0.1.0)

Verified with .NET SDK 10.0.401 (Roslyn 5.9) on linux/arm64, including a `net10.0;net8.0`
multi-target project.

| Check | Command (in the project directory of the snapshot) | Scope | Output | Exit codes |
|---|---|---|---|---|
| `dotnet-build` | `dotnet build -nologo -tl:off -nodeReuse:false -maxCpuCount:N --no-incremental …` | project | SARIF 2.1 files | 0 clean, 1 findings |

Each `.csproj` is one pistos project. Supported: SDK-style, cross-platform projects (.NET 8 SDK or
newer for `UseArtifactsOutput`). Legacy (non-SDK) projects, Windows-only workloads and
`.sln`-level-only settings are not verified.

## How diagnostics are collected

- A props file injected through `CustomBeforeMicrosoftCommonTargets` sets
  `ErrorLog=<scratch>/<project>-<hash>-<TargetFramework>.sarif`, so every project and target framework
  writes its own log; nothing overwrites another build's diagnostics.
- `UseArtifactsOutput`/`ArtifactsPath` place `bin`/`obj` in a per-task scratch directory: concurrent
  builds of projects that reference each other never share outputs, and the snapshot stays clean.
- `--no-incremental` makes every review recompile, so an up-to-date project cannot produce an empty
  (clean-looking) log. A build that produced no log is **incomplete**; a failed build without
  compiler errors (e.g. restore failure `NU1101`) is **failed/incomplete** with the MSBuild message.
- Restore is limited to the local package cache (`NUGET_PACKAGES` or `~/.nuget/packages`); reviews
  never download packages. Run `dotnet restore` yourself when dependencies change.
- `UseSharedCompilation=false` and `-nodeReuse:false` keep no compiler server or MSBuild node alive.

`.editorconfig`, `Directory.Build.*`, `Directory.Packages.props`, `global.json` and NuGet config in the
project or ancestor directories are honoured by MSBuild and are cache inputs.

## Filtering

Diagnostics for files in referenced projects are left to those projects (counted as a limitation),
diagnostics in files that are not part of the snapshot (generated sources under `obj/`, source
generators) are counted but not reported, in-source suppressions are honoured, identical diagnostics
from several target frameworks are reported once, and TFM-specific ones (`#if`) stay distinct.

Options: `configuration` (`-p:Configuration`), `framework` (`-p:TargetFramework`).
