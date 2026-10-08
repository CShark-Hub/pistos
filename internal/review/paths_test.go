package review

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestRepoRelativeNormalizesAndConfines(t *testing.T) {
	root := filepath.Join(t.TempDir(), "snap")
	project := filepath.Join(root, "svc")
	for _, tc := range []struct {
		in   string
		want string
		ok   bool
	}{
		{filepath.Join(project, "pkg", "a.go"), "svc/pkg/a.go", true},
		{filepath.Join("pkg", "a.go"), "svc/pkg/a.go", true},
		{filepath.Join("..", "other", "b.go"), "other/b.go", true},
		{filepath.Join("..", "..", "escape.go"), "", false},
		{filepath.Join(filepath.Dir(root), "outside.go"), "", false},
		{"", "", false},
	} {
		got, ok := repoRelative(tc.in, root, root, project)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%q → %q %v, want %q %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
	if runtime.GOOS == "windows" {
		got, ok := repoRelative(`C:\elsewhere\x.cs`, root, root, project)
		if ok || got != "" {
			t.Errorf("a different drive must be outside the snapshot: %q", got)
		}
		mixed := project + `/pkg\a.go`
		if got, ok := repoRelative(mixed, root, root, project); !ok || got != "svc/pkg/a.go" {
			t.Errorf("mixed separators: %q %v", got, ok)
		}
	}
}
