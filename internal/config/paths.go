package config

import (
	"fmt"
	"path"
	"strings"

	"example.com/pistos/internal/fsutil"
)

// PathFilter narrows a project to part of its root. Globs are relative to the root; "*" stays within
// a segment, "**" spans segments, and a glob naming a directory also covers everything below it.
type PathFilter struct {
	Root    string
	Include []string
	Exclude []string
}

func (p Project) Filter() PathFilter {
	root, _ := CleanRel(p.Root)
	return PathFilter{Root: root, Include: p.Include, Exclude: p.Exclude}
}

// rel returns p relative to the root, or false when p lies outside it.
func (f PathFilter) rel(p string) (string, bool) {
	switch {
	case f.Root == "." || f.Root == "":
		return p, true
	case strings.HasPrefix(p, f.Root+"/"):
		return p[len(f.Root)+1:], true
	}
	return "", false
}

func (f PathFilter) selected(rel string) bool {
	if len(f.Include) > 0 && !matchAny(f.Include, rel) {
		return false
	}
	return !matchAny(f.Exclude, rel)
}

// Contains reports whether the repository-relative path belongs to the project.
func (f PathFilter) Contains(p string) bool {
	rel, ok := f.rel(p)
	return ok && f.selected(rel)
}

// Drops reports whether the path is under the root but filtered out. Paths outside the root are not
// the filter's to judge (a tool may report into a referenced project).
func (f PathFilter) Drops(p string) bool {
	rel, ok := f.rel(p)
	return ok && !f.selected(rel)
}

func (f PathFilter) Active() bool { return len(f.Include)+len(f.Exclude) > 0 }

func (f PathFilter) String() string {
	var parts []string
	if len(f.Include) > 0 {
		parts = append(parts, "include "+strings.Join(f.Include, ", "))
	}
	if len(f.Exclude) > 0 {
		parts = append(parts, "exclude "+strings.Join(f.Exclude, ", "))
	}
	return strings.Join(parts, "; ")
}

func matchAny(globs []string, rel string) bool {
	for _, g := range globs {
		g = strings.TrimSuffix(g, "/")
		if fsutil.MatchGlob(g, rel) || fsutil.MatchGlob(g+"/**", rel) {
			return true
		}
	}
	return false
}

func validateGlob(g string) error {
	if strings.TrimSpace(g) == "" {
		return fmt.Errorf("empty glob")
	}
	s := strings.TrimSuffix(g, "/")
	if _, err := CleanRel(strings.ReplaceAll(s, "*", "x")); err != nil {
		return err
	}
	if path.Clean(s) != s && s != "" {
		return fmt.Errorf("%q is not a clean relative path (no ./, .. or duplicate slashes)", g)
	}
	for _, seg := range strings.Split(s, "/") {
		if _, err := path.Match(seg, ""); err != nil {
			return fmt.Errorf("%q: %v", g, err)
		}
	}
	return nil
}
