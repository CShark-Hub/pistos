// Package packs embeds the bundled language packs: manifests (inert data) and the facts worker
// sources that `pistos plugins install` installs into the managed tools directory.
package packs

import "embed"

//go:embed */manifest.json */Dockerfile python/facts.py jsts/facts.mjs csharp/worker/PistosFacts.csproj csharp/worker/Program.cs
var FS embed.FS

// dirs lists the packs whose embedded directory differs from the pack name.
var dirs = map[string]string{"secrets": "secretscan"}

// Dir returns the directory of a pack inside FS.
func Dir(name string) string {
	if d, ok := dirs[name]; ok {
		return d
	}
	return name
}
