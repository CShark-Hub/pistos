// Package detect finds project roots and their configured backends without running any tool.
package detect

import (
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"example.com/pistos/internal/config"
)

// skipDirs are never crawled: dependency stores, build outputs and generated trees.
var skipDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true, ".pistos": true,
	"node_modules": true, "vendor": true, "third_party": true,
	".venv": true, "venv": true, "env": true, "__pycache__": true, ".tox": true, ".mypy_cache": true, ".ruff_cache": true,
	"bin": true, "obj": true, "dist": true, "build": true, "out": true, "target": true, "coverage": true,
	"testdata": true, "generated": true, ".next": true, ".idea": true, ".vscode": true,
}

// Projects walks the repository and returns one project per detected root, ordered by ID.
func Projects(repo string, only []string) ([]config.Project, error) {
	want := map[string]bool{}
	for _, l := range only {
		want[l] = true
	}
	allowed := func(eco string) bool { return len(want) == 0 || want[eco] }

	var projects []config.Project
	seen := map[string]bool{}
	add := func(eco, rel string, checks []string) {
		id := projectID(eco, rel)
		if seen[id] || !allowed(eco) {
			return
		}
		seen[id] = true
		projects = append(projects, config.Project{ID: id, Root: rel, Ecosystem: eco, Checks: checks})
	}

	err := filepath.WalkDir(repo, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(repo, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel != "." && (skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		dir := path.Dir(rel)
		switch name := d.Name(); {
		case name == "go.mod":
			add(config.Go, dir, []string{"go-vet", "staticcheck", "metrics"})
		case name == "pyproject.toml" || name == "setup.py" || name == "setup.cfg" || name == "requirements.txt":
			add(config.Python, dir, append(pythonChecks(filepath.Dir(p)), "metrics"))
		case name == "package.json" || name == "tsconfig.json":
			add(config.JSTS, dir, append(jsChecks(filepath.Dir(p)), "metrics"))
		case strings.HasSuffix(name, ".csproj"):
			add(config.CSharp, dir, []string{"dotnet-build", "metrics"})
		}
		return nil
	})
	// Nested Python markers (docs/requirements.txt, sub-packages) belong to the enclosing project.
	var kept []config.Project
	for _, p := range projects {
		if p.Ecosystem != config.Python || !hasAncestorProject(projects, p) {
			kept = append(kept, p)
		}
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].ID < kept[j].ID })
	return kept, err
}

func hasAncestorProject(ps []config.Project, child config.Project) bool {
	for _, p := range ps {
		if p.Ecosystem == child.Ecosystem && p.Root != child.Root && (p.Root == "." || strings.HasPrefix(child.Root, p.Root+"/")) {
			return true
		}
	}
	return false
}

var nonID = regexp.MustCompile(`[^a-z0-9]+`)

func projectID(eco, rel string) string {
	if rel == "." {
		return eco
	}
	slug := strings.Trim(nonID.ReplaceAllString(strings.ToLower(rel), "-"), "-")
	id := eco + "-" + slug
	if len(id) > 64 {
		id = id[:64]
	}
	return strings.TrimRight(id, "-._")
}

// pythonChecks keeps the repository's chosen type checker rather than substituting a faster one.
func pythonChecks(dir string) []string {
	text := readText(filepath.Join(dir, "pyproject.toml")) + readText(filepath.Join(dir, "requirements-dev.txt")) + readText(filepath.Join(dir, "requirements.txt"))
	checks := []string{"ruff"}
	switch {
	case exists(filepath.Join(dir, "pyrightconfig.json")) || strings.Contains(text, "[tool.pyright]"):
		checks = append(checks, "pyright")
	case strings.Contains(text, "[tool.basedpyright]") || strings.Contains(text, "basedpyright"):
		checks = append(checks, "basedpyright")
	case strings.Contains(text, "[tool.ty]") || exists(filepath.Join(dir, "ty.toml")):
		checks = append(checks, "ty")
	case strings.Contains(text, "[tool.mypy]") || exists(filepath.Join(dir, "mypy.ini")):
		checks = append(checks, "mypy")
	default:
		checks = append(checks, "pyright")
	}
	return checks
}

// jsChecks keeps the repository's existing linter; it never swaps ESLint for a faster alternative.
func jsChecks(dir string) []string {
	var checks []string
	if exists(filepath.Join(dir, "tsconfig.json")) {
		checks = append(checks, "tsc")
	}
	deps := packageDeps(filepath.Join(dir, "package.json"))
	switch {
	case deps["eslint"] || globExists(dir, "eslint.config.*") || globExists(dir, ".eslintrc*"):
		checks = append(checks, "eslint")
	case deps["@biomejs/biome"] || exists(filepath.Join(dir, "biome.json")) || exists(filepath.Join(dir, "biome.jsonc")):
		checks = append(checks, "biome")
	case deps["oxlint"] || globExists(dir, ".oxlintrc*"):
		checks = append(checks, "oxlint")
	}
	return checks
}

func packageDeps(file string) map[string]bool {
	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	_ = json.Unmarshal([]byte(readText(file)), &pkg)
	deps := map[string]bool{}
	for k := range pkg.Dependencies {
		deps[k] = true
	}
	for k := range pkg.DevDependencies {
		deps[k] = true
	}
	return deps
}

func readText(p string) string {
	b, _ := os.ReadFile(p)
	return string(b)
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func globExists(dir, pattern string) bool {
	m, _ := filepath.Glob(filepath.Join(dir, pattern))
	return len(m) > 0
}
