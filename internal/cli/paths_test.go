package cli

import (
	"strings"
	"testing"
)

func TestProjectPathFiltersScopeAndFindings(t *testing.T) {
	r := setupGo(t, "go-clean", configFor(goProject+"exclude = [\"gen\"]\n"))
	r.Write("gen/gen.go", "package gen\n\nimport \"fmt\"\n\nfunc G(n int) string { return fmt.Sprintf(\"%s\", n) }\n")

	code, env, errs := runReview(t, r.Dir)
	if code != ExitOK || len(env.Canonical.Scope.Projects) != 0 || len(env.Canonical.Issues) != 0 {
		t.Fatalf("a change only in excluded paths must not trigger the project: code=%d projects=%v issues=%+v %s",
			code, env.Canonical.Scope.Projects, env.Canonical.Issues, errs)
	}

	r.Write("main.go", strings.Replace(r.Read("main.go"), "hello, %s", "hello, %d", 1))
	code, env, errs = runReview(t, r.Dir)
	is := env.Canonical.Issues
	if code != ExitBlocking || len(is) != 1 || is[0].Location.Path != "main.go" {
		t.Fatalf("only the included finding must remain: code=%d %+v %s", code, is, errs)
	}
	vet := capability(env, "app", "go-vet", "head")
	if !strings.Contains(strings.Join(vet.Limitations, "\n"), "1 finding(s) dropped by the project's path filters (exclude gen)") {
		t.Fatalf("coverage must say what the filters dropped: %v", vet.Limitations)
	}
}
