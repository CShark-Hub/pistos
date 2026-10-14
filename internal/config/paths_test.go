package config

import "testing"

func TestPathFilter(t *testing.T) {
	f := Project{Root: "svc", Include: []string{"src/**", "cmd"}, Exclude: []string{"src/gen/", "**/*_mock.go"}}.Filter()
	for p, want := range map[string]bool{
		"svc/src/a.go":           true,
		"svc/cmd/main.go":        true, // a directory glob covers what is below it
		"svc/src/gen/x.go":       false,
		"svc/src/deep/a_mock.go": false,
		"svc/docs/readme.md":     false, // not included
		"other/src/a.go":         false, // outside the root
	} {
		if got := f.Contains(p); got != want {
			t.Errorf("Contains(%s) = %v, want %v", p, got, want)
		}
	}
	if f.Drops("other/src/gen/x.go") || !f.Drops("svc/src/gen/x.go") {
		t.Error("Drops judges only paths under the root")
	}
	if all := (Project{Root: "."}).Filter(); all.Active() || !all.Contains("any/path.go") {
		t.Error("a project without globs keeps everything under its root")
	}
}
