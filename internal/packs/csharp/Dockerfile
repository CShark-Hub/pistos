# C# pack image: .NET SDK (compiler and analyzers), the Roslyn facts worker and csharp-ls.
FROM mcr.microsoft.com/dotnet/sdk:10.0.401-noble@sha256:e70cdb7f80b0348f5cb85f19a8f670fca061f033d57eed12fa003d58b0e06317
ENV DOTNET_CLI_TELEMETRY_OPTOUT=1 DOTNET_NOLOGO=1 DOTNET_SKIP_FIRST_TIME_EXPERIENCE=1
COPY PistosFacts.csproj Program.cs /opt/pistos/src/
RUN mkdir -p /tmp/feed \
    && dotnet build /opt/pistos/src -c Release -nologo -o /opt/pistos/bin -p:PistosTargetFramework=net10.0 -p:RestoreSources=/tmp/feed \
    && dotnet tool install csharp-ls --version 0.28.0 --tool-path /opt/pistos/tools \
    && rm -rf /opt/pistos/src/obj /tmp/feed /root/.nuget
