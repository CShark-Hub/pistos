package cli

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"example.com/pistos/internal/finding"
	"example.com/pistos/internal/testutil/fakeai"
	"example.com/pistos/internal/testutil/gitrepo"
)

// Fake secrets are assembled at run time so the test sources do not trip secret scanners; each
// probe is distinctive enough to search for in everything pistos writes.
const (
	leakProbe    = "PistosLeakProbe"
	envLeakProbe = "PistosEnvLeakProbe"
)

var (
	fakeGitHubToken = "ghp_" + leakProbe + "0123456789abcdefghijk"
	fakeEnvToken    = "sk_" + "live_" + envLeakProbe + "0123456789abcdefgh"
	oldGitHubToken  = "ghp_" + "PistosOldTokenInBase0123456789abcdef"
)

const secretsProject = "\n[[projects]]\nid = \"secrets\"\nroot = \".\"\necosystem = \"secrets\"\nchecks = [\"gitleaks\"]\n"

// setupSecrets configures the secrets pack (plus extra projects) and acquires gitleaks through
// `plugins install`, which needs the Go toolchain and the module proxy.
func setupSecrets(t *testing.T, cfg string, edit func(r *gitrepo.Repo)) *gitrepo.Repo {
	t.Helper()
	useGoToolchain(t)
	r := gitrepo.New(t, "go-clean")
	r.Write(".pistos/config.toml", cfg)
	r.Write(".pistos/.gitignore", "cache/\nstate/\n")
	if edit != nil {
		edit(r)
	}
	if code, out, errs := run(t, r.Dir, "plugins", "install"); code != 0 {
		if strings.Contains(out+errs, "go install") || strings.Contains(out+errs, "acquir") {
			skipLive(t, "gitleaks acquisition unavailable: %s %s", out, errs)
		}
		t.Fatalf("plugins install: %s %s", out, errs)
	}
	r.Commit("configure pistos")
	return r
}

func secretsConfig(languages, projects string) string {
	return "schema = 1\nlanguages = " + languages + "\n\n[execution]\njobs = 4\n" + projects + secretsProject
}

func TestSecretsPackLive(t *testing.T) {
	r := setupSecrets(t, secretsConfig(`["secrets"]`, ""), func(r *gitrepo.Repo) {
		r.Write("deploy/old.txt", "token = \""+oldGitHubToken+"\"\n")
	})
	r.Write("main.go", r.Read("main.go")+"\nconst token = \""+fakeGitHubToken+"\"\n")

	code, env, errs := runReview(t, r.Dir)
	if code != ExitBlocking {
		t.Fatalf("a new secret blocks: code %d %s", code, errs)
	}
	requireStatus(t, env, "secrets", "gitleaks", finding.StatusComplete)
	byPath := map[string]finding.Issue{}
	for _, is := range env.Canonical.Issues {
		byPath[is.Location.Path] = is
	}
	added, old := byPath["main.go"], byPath["deploy/old.txt"]
	if added.Family != "gitleaks/github-pat" || added.Change != "new" || !added.Blocking || !added.Sensitive || added.Severity != finding.SevError || added.Location.Line != 13 {
		t.Fatalf("new secret: %+v", added)
	}
	if old.Change != "existing" || old.Blocking || !old.Sensitive {
		t.Fatalf("a secret already in the baseline is existing and does not block: %+v", old)
	}

	// The fast profile (and so pre-commit hooks) still scans: the check is file-scoped.
	if code, env, _ := runReview(t, r.Dir, "--profile", "fast"); code != ExitBlocking || capability(env, "secrets", "gitleaks", "head").Status == finding.StatusNotRequested {
		t.Fatalf("fast profile must run gitleaks: %d", code)
	}

	// .gitleaksignore in the reviewed tree is honoured (dir-mode fingerprints are path:rule:line).
	r.Write(".gitleaksignore", "main.go:github-pat:13\n")
	if code, env, errs := runReview(t, r.Dir); code != ExitOK || len(env.Canonical.Issues) != 1 {
		t.Fatalf(".gitleaksignore must suppress the new secret: %d %+v %s", code, env.Canonical.Issues, errs)
	}

	// A repository .gitleaks.toml replaces or extends the default rules.
	r.Write(".gitleaks.toml", "[extend]\nuseDefault = true\n\n[[rules]]\nid = \"pistos-canary\"\ndescription = \"Pistos canary value\"\nregex = '''PISTOS_CANARY_[0-9]{6}'''\n")
	r.Write("notes.md", "value: PISTOS_CANARY_123456\n")
	code, env, errs = runReview(t, r.Dir)
	if code != ExitBlocking || issueFamilies(env)["gitleaks/pistos-canary"].Location.Path != "notes.md" {
		t.Fatalf("custom .gitleaks.toml rules must apply: %d %v %s", code, keys(issueFamilies(env)), errs)
	}

	// An invalid configuration fails the check instead of reporting a clean scan.
	r.Write(".gitleaks.toml", "garbage [[[\n")
	code, env, _ = runReview(t, r.Dir)
	if c := capability(env, "secrets", "gitleaks", "head"); code != ExitError || c.Status != finding.StatusFailed {
		t.Fatalf("invalid .gitleaks.toml: code %d %+v", code, c)
	}
}

// TestSecretsNeverLeave reviews a change with secrets, AI enabled against the fake endpoint, and
// searches every pistos output, cache and recorded AI call for the secret text.
func TestSecretsNeverLeave(t *testing.T) {
	t.Setenv("PISTOS_TEST_KEY", "sk-test-0123456789-secret")
	srv := fakeai.New()
	t.Cleanup(srv.Close)
	srv.Respond = func(fakeai.Request) (string, int) { return respond(), http.StatusOK }
	cfg := strings.Replace(aiConfig(srv.URL, "mode = \"evidence\"\n"), `languages = ["go"]`, `languages = ["go", "secrets"]`, 1) + secretsProject
	r := setupSecrets(t, cfg, nil)
	r.Write("main.go", r.Read("main.go")+"\nconst token = \""+fakeGitHubToken+"\"\n")
	r.Write(".env", "STRIPE_KEY="+fakeEnvToken+"\n")

	outputs := map[string]string{}
	for _, format := range []string{"json", "canonical-json", "sarif", "text", "agent"} {
		code, out, errs := run(t, r.Dir, "review", "--profile", "thorough", "--format", format)
		if code != ExitBlocking {
			t.Fatalf("%s: code %d %s", format, code, errs)
		}
		outputs["review --format "+format] = out
		outputs["review --format "+format+" (stderr)"] = errs
	}
	var env finding.Envelope
	if err := json.Unmarshal([]byte(outputs["review --format json"]), &env); err != nil {
		t.Fatalf("json output: %v", err)
	}
	flagged := 0
	for _, is := range env.Canonical.Issues {
		if is.Sensitive {
			flagged++
		}
	}
	if flagged != 2 {
		t.Fatalf("both secrets must be found: %+v", env.Canonical.Issues)
	}
	if !strings.Contains(outputs["review --format sarif"], `"sensitive": true`) {
		t.Fatal("SARIF marks sensitive results")
	}

	// The served report renders the same withheld excerpts as report.html.
	ts := httptest.NewUnstartedServer(nil)
	a := &app{stdout: io.Discard, stderr: io.Discard}
	ts.Config.Handler = a.reportHandler(context.Background(), r.Dir, "tok", ts.Listener.Addr().String())
	ts.Start()
	defer ts.Close()
	res, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(page), finding.WithheldLine) {
		t.Fatalf("served report must show the withheld placeholder: %d", res.StatusCode)
	}
	outputs["served report"] = string(page)

	reqs := srv.Requests()
	if len(reqs) == 0 {
		t.Fatal("AI discovery must have run")
	}
	sent, _ := json.Marshal(reqs)
	outputs["AI requests"] = string(sent)
	if !strings.Contains(string(sent), "[line withheld by pistos: possible secret (gitleaks/github-pat)]") {
		t.Fatal("the flagged line of a changed source file must be replaced in AI evidence")
	}
	// The Go project sits at the repository root, so it owns .env too; its flagged line is withheld.
	if !strings.Contains(string(sent), "[line withheld by pistos: possible secret (gitleaks/stripe-access-token)]") {
		t.Fatal("every flagged line of an evidence file must be replaced")
	}

	// Everything pistos persisted. Materialized snapshot trees are excluded: they are copies of the
	// reviewed source (like git's object store), not outputs.
	cacheRoot := os.Getenv("PISTOS_CACHE_DIR")
	for _, dir := range []string{filepath.Join(r.Dir, ".pistos", "cache"), filepath.Join(r.Dir, ".pistos", "state"), cacheRoot} {
		filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() && p == filepath.Join(cacheRoot, "trees") {
				return filepath.SkipDir
			}
			if !d.IsDir() {
				b, _ := os.ReadFile(p)
				outputs[p] = string(b)
			}
			return nil
		})
	}
	if _, ok := outputs[filepath.Join(r.Dir, ".pistos", "state", "report.html")]; !ok {
		t.Fatal("the HTML report must have been written")
	}
	for where, content := range outputs {
		for _, probe := range []string{leakProbe, envLeakProbe} {
			if strings.Contains(content, probe) {
				t.Errorf("secret text %s leaked into %s", probe, where)
			}
		}
	}
}
