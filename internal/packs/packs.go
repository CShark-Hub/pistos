// Package packs embeds the bundled language packs: manifests (inert data) and the facts worker
// sources that `pistos plugins install` installs into the managed tools directory.
package packs

import "embed"

//go:embed */manifest.json */Dockerfile python/facts.py jsts/facts.mjs csharp/worker/PistosFacts.csproj csharp/worker/Program.cs
var FS embed.FS
