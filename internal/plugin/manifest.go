package plugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"

	"example.com/pistos/internal/fsutil"
	"example.com/pistos/internal/packs"
)

const ManifestSchema = 1

type Manifest struct {
	Schema         int               `json:"schema"`
	Name           string            `json:"name"`
	Version        string            `json:"version"`
	Languages      []string          `json:"languages"`
	TestPaths      []string          `json:"test_paths"`
	Options        []string          `json:"options"`
	EnvPassthrough []string          `json:"env_passthrough"`
	SharedInputs   []string          `json:"shared_inputs"`
	Tools          map[string]Tool   `json:"tools"`
	Checks         map[string]Check  `json:"checks"`
	Aliases        map[string]string `json:"aliases"`
	Authority      []string          `json:"authority"`
}

type Tool struct {
	Command string `json:"command"`
	// Lookup is the search order: "repo:<dir>" (relative to project, then repository root), "managed", "path".
	Lookup       []string `json:"lookup"`
	VersionArgs  []string `json:"version_args"`
	VersionRegex string   `json:"version_regex"`
	// Runtime names another tool of the pack that must run this one (e.g. node for a JS entry point).
	Runtime string   `json:"runtime,omitempty"`
	Acquire *Acquire `json:"acquire,omitempty"`
}

type Acquire struct {
	Method  string `json:"method"`
	Package string `json:"package"`
	Version string `json:"version"`
}

type Check struct {
	Tool string `json:"tool"`
	// Uses lists further tools the check depends on at runtime (e.g. the Go toolchain for staticcheck).
	Uses      []string          `json:"uses,omitempty"`
	Args      []string          `json:"args"`
	Format    string            `json:"format"`
	Scope     string            `json:"scope"`
	Weight    int               `json:"weight"`
	ExitCodes map[string]string `json:"exit_codes"`
	Env       map[string]string `json:"env"`
	// Optional checks report their status but never make the deterministic gate incomplete.
	Optional bool `json:"optional,omitempty"`
	// Unsupported explains why a selectable backend cannot run yet; selecting it is reported, not ignored.
	Unsupported string `json:"unsupported,omitempty"`
	// Artifacts is a directory (template) the tool writes structured results into instead of stdout.
	Artifacts   string   `json:"artifacts,omitempty"`
	Limitations []string `json:"limitations,omitempty"`
}

// LoadManifest reads an embedded pack manifest. It never starts a process.
func LoadManifest(name string) (*Manifest, error) {
	b, err := fs.ReadFile(packs.FS, path.Join(name, "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("no bundled pack %q", name)
	}
	return ParseManifest(b)
}

func ParseManifest(b []byte) (*Manifest, error) {
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("pack manifest: %w", err)
	}
	if m.Schema != ManifestSchema {
		return nil, fmt.Errorf("pack %s: manifest schema %d is not supported", m.Name, m.Schema)
	}
	for _, id := range m.CheckIDs() {
		c := m.Checks[id]
		if c.Unsupported != "" {
			continue
		}
		if _, ok := m.Tools[c.Tool]; !ok {
			return nil, fmt.Errorf("pack %s: check %s uses undeclared tool %q", m.Name, id, c.Tool)
		}
		for _, u := range c.Uses {
			if _, ok := m.Tools[u]; !ok {
				return nil, fmt.Errorf("pack %s: check %s uses undeclared tool %q", m.Name, id, u)
			}
		}
		if _, ok := parsers[c.Format]; !ok && c.Format != FormatWorker {
			return nil, fmt.Errorf("pack %s: check %s has unsupported output format %q", m.Name, id, c.Format)
		}
		if c.Scope != "file" && c.Scope != "project" {
			return nil, fmt.Errorf("pack %s: check %s scope must be file or project", m.Name, id)
		}
	}
	for name, t := range m.Tools {
		if _, err := regexp.Compile(t.VersionRegex); err != nil {
			return nil, fmt.Errorf("pack %s: tool %s version_regex: %w", m.Name, name, err)
		}
		if t.Runtime != "" {
			if _, ok := m.Tools[t.Runtime]; !ok {
				return nil, fmt.Errorf("pack %s: tool %s runtime %q is undeclared", m.Name, name, t.Runtime)
			}
		}
	}
	return &m, nil
}

func (m *Manifest) CheckIDs() []string {
	ids := make([]string, 0, len(m.Checks))
	for id := range m.Checks {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// ToolsFor returns the tools a check needs, its own tool first.
func (m *Manifest) ToolsFor(checkID string) []string {
	c := m.Checks[checkID]
	out := []string{c.Tool}
	if rt := m.Tools[c.Tool].Runtime; rt != "" {
		out = append(out, rt)
	}
	for _, u := range c.Uses {
		if !contains(out, u) {
			out = append(out, u)
		}
	}
	return out
}

// Family maps a tool rule to its problem family through declared aliases.
func (m *Manifest) Family(tool, rule string) string {
	id := tool + "/" + rule
	if f, ok := m.Aliases[id]; ok {
		return f
	}
	return id
}

// IsTestPath classifies a project-relative path using the pack's test globs.
func (m *Manifest) IsTestPath(rel string) bool {
	for _, g := range m.TestPaths {
		if fsutil.MatchGlob(g, rel) {
			return true
		}
	}
	return false
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
