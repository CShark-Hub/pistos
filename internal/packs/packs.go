// Package packs embeds the manifests of the bundled language packs. Manifests are inert data.
package packs

import "embed"

//go:embed */manifest.json
var FS embed.FS
