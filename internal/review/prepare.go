package review

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"example.com/pistos/internal/config"
	"example.com/pistos/internal/fsutil"
)

// preparation carries the ecosystem-specific inputs of one task.
type preparation struct {
	vars map[string]string
	// fingerprint identifies installed dependencies that live outside the snapshot (node_modules,
	// virtualenvs); it is part of the cache key.
	fingerprint string
	limits      []string
}

// dotnetProps is injected through CustomBeforeMicrosoftCommonTargets so every project and target
// framework writes its own SARIF log; a shared file name would let builds overwrite each other.
const dotnetProps = `<Project>
  <PropertyGroup>
    <ErrorLog>$(PistosSarifDir)/$(MSBuildProjectName)-$([MSBuild]::StableStringHash($(MSBuildProjectFullPath)))-$(TargetFramework).sarif,version=2.1</ErrorLog>
  </PropertyGroup>
</Project>
`

func (e *engine) prepare(t task, workRoot, scratch string) (preparation, error) {
	p := preparation{vars: map[string]string{}}
	repoProject := filepath.Join(e.opts.Repo, filepath.FromSlash(t.project.Root))
	switch t.project.Ecosystem {
	case config.JSTS:
		p.vars["tsconfig"] = "tsconfig.json"
		if v := t.project.Options["tsconfig"]; v != "" {
			p.vars["tsconfig"] = v
		}
		linked, fp, err := linkNodeModules(e.opts.Repo, workRoot, t.project.Root)
		if err != nil {
			return p, err
		}
		p.fingerprint = fp
		if !linked {
			p.limits = append(p.limits, "no installed node_modules found for this project; imports of dependencies cannot resolve")
		}
	case config.Python:
		python := t.project.Options["python"]
		if python != "" {
			if !filepath.IsAbs(python) {
				python = filepath.Join(e.opts.Repo, filepath.FromSlash(python))
			}
		} else {
			python = findVenvPython(repoProject, e.opts.Repo)
		}
		if python != "" {
			p.vars["python"] = python
			p.fingerprint = sitePackagesFingerprint(python)
		} else if usesVar(e.manifests[t.project.Ecosystem].Checks[t.Capability].Args, "python") {
			p.limits = append(p.limits, "no project virtualenv found (.venv/ or venv/); imports resolve against the default interpreter")
		}
	case config.CSharp:
		props := filepath.Join(scratch, "pistos.props")
		if err := fsutil.WriteFileAtomic(props, []byte(dotnetProps), 0o644); err != nil {
			return p, err
		}
		p.vars["props"] = props
		p.vars["sarif_dir"] = filepath.Join(scratch, "sarif")
		p.vars["artifacts"] = filepath.Join(scratch, "artifacts")
		p.vars["restore_sources"] = nugetLocalSource(scratch)
	}
	return p, nil
}

// linkNodeModules exposes the repository's installed node_modules (project and ancestors, for
// hoisted workspaces) inside the snapshot: dependencies are reused, never installed by a review.
func linkNodeModules(repo, workRoot, root string) (bool, string, error) {
	linked := false
	var parts []string
	for rel := root; ; rel = parentDir(rel) {
		src := filepath.Join(repo, filepath.FromSlash(rel), "node_modules")
		if fi, err := os.Stat(src); err == nil && fi.IsDir() {
			dst := filepath.Join(workRoot, filepath.FromSlash(rel), "node_modules")
			if _, err := os.Lstat(dst); os.IsNotExist(err) {
				// Checks sharing this tree link concurrently; losing the race is fine.
				if err := os.Symlink(src, dst); err != nil && !os.IsExist(err) {
					return false, "", err
				}
			}
			linked = true
			fp := ""
			for _, lock := range []string{".package-lock.json", ".modules.yaml", ".yarn-state.yml"} {
				if h, err := fsutil.HashFile(filepath.Join(src, lock)); err == nil {
					fp = lock + "=" + h
					break
				}
			}
			if fp == "" {
				fp = "listing=" + dirListing(src)
			}
			parts = append(parts, rel+":"+fp)
		}
		if rel == "." {
			break
		}
	}
	return linked, fsutil.HashStrings(parts...), nil
}

func findVenvPython(dirs ...string) string {
	for _, d := range dirs {
		for _, v := range []string{".venv", "venv"} {
			p := filepath.Join(d, v, "bin", "python")
			if runtime.GOOS == "windows" {
				p = filepath.Join(d, v, "Scripts", "python.exe")
			}
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}

// sitePackagesFingerprint lists installed distributions so dependency changes invalidate results.
func sitePackagesFingerprint(python string) string {
	venv := filepath.Dir(filepath.Dir(python))
	matches, _ := filepath.Glob(filepath.Join(venv, "lib", "python*", "site-packages", "*.dist-info"))
	win, _ := filepath.Glob(filepath.Join(venv, "Lib", "site-packages", "*.dist-info"))
	matches = append(matches, win...)
	for i, m := range matches {
		matches[i] = filepath.Base(m)
	}
	sort.Strings(matches)
	return fsutil.HashStrings(append([]string{python}, matches...)...)
}

func dirListing(dir string) string {
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return fsutil.HashStrings(names...)
}

// nugetLocalSource restricts restore to the local package cache so a review never downloads packages.
func nugetLocalSource(scratch string) string {
	if p := os.Getenv("NUGET_PACKAGES"); p != "" {
		return p
	}
	if home, err := os.UserHomeDir(); err == nil {
		p := filepath.Join(home, ".nuget", "packages")
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			return p
		}
	}
	empty := filepath.Join(scratch, "empty-feed")
	_ = os.MkdirAll(empty, 0o755)
	return empty
}

func usesVar(args []string, name string) bool {
	for _, a := range args {
		if strings.Contains(a, "{var:"+name+"}") {
			return true
		}
	}
	return false
}
