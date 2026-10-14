package cli

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"example.com/pistos/internal/feedback"
)

func TestReviewWritesHTMLReportAndAnnouncesIt(t *testing.T) {
	r := setupGo(t, "go-clean", configFor(goProject))
	r.Write("main.go", strings.Replace(r.Read("main.go"), "hello, %s", "hello, %d", 1))
	code, _, errs := runReview(t, r.Dir)
	if code != ExitBlocking {
		t.Fatalf("review: %d %s", code, errs)
	}
	p := filepath.Join(r.Dir, ".pistos", "state", "report.html")
	if !strings.Contains(errs, "report   file://") || !strings.Contains(errs, "report.html") {
		t.Fatalf("stderr must announce the report link:\n%s", errs)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	html := string(b)
	if !strings.Contains(html, `class="ln hit"`) || !strings.Contains(html, "hello, %d") {
		t.Fatalf("report must show the issue's code with the line marked")
	}
	if !strings.Contains(html, `"token": ""`) {
		t.Fatalf("the static report must not carry a save token")
	}

	if code, _, errs := run(t, r.Dir, "review", "--no-html"); code != ExitBlocking || strings.Contains(errs, "report   ") {
		t.Fatalf("--no-html: code=%d %s", code, errs)
	}
}

func TestReportServerSavesExactAndKindDecisions(t *testing.T) {
	r := setupGo(t, "go-clean", configFor(goProject))
	r.Write("main.go", strings.Replace(r.Read("main.go"), "hello, %s", "hello, %d", 1)+
		"\nfunc twice(n int) string { return fmt.Sprintf(\"%s\", n) }\n")
	_, env, _ := runReview(t, r.Dir)
	if len(env.Canonical.Issues) != 2 {
		t.Fatalf("want two printf issues, got %+v", env.Canonical.Issues)
	}
	k1, k2 := env.Canonical.Issues[0].Key, env.Canonical.Issues[1].Key

	ts := httptest.NewUnstartedServer(nil)
	a := &app{stdout: io.Discard, stderr: io.Discard}
	ts.Config.Handler = a.reportHandler(context.Background(), r.Dir, "tok", ts.Listener.Addr().String())
	ts.Start()
	defer ts.Close()

	post := func(token, body string) (int, string) {
		req, _ := http.NewRequest("POST", ts.URL+"/api/decisions", strings.NewReader(body))
		req.Header.Set("X-Pistos-Token", token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}

	req, _ := http.NewRequest("GET", ts.URL+"/", nil)
	req.Host = "attacker.example"
	if res, err := http.DefaultClient.Do(req); err != nil || res.StatusCode != http.StatusMisdirectedRequest {
		t.Fatalf("foreign Host must be refused: %v %v", res, err)
	}
	res, err := http.Get(ts.URL + "/")
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("GET /: %v %v", res, err)
	}
	page, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(page), `"token": "tok"`) {
		t.Fatalf("served page must carry the token")
	}
	if code, _ := post("wrong", `{"decisions":[]}`); code != http.StatusForbidden {
		t.Fatalf("wrong token: %d", code)
	}

	// Both selections name the same kind, so they collapse into one pattern decision; saving it a
	// second time extends that decision instead of failing on a duplicate ID.
	kind := `{"decisions":[{"key":"` + k1 + `","scope":"pattern","tags":["false-positive"],"reason":"generated","paths":[]},` +
		`{"key":"` + k2 + `","scope":"pattern","tags":["false-positive"],"paths":[]}]}`
	for i := 0; i < 2; i++ {
		if code, body := post("tok", kind); code != http.StatusOK || !strings.Contains(body, `"errors":[]`) {
			t.Fatalf("save %d: %d %s", i, code, body)
		}
	}
	fb, err := feedback.Load(r.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(fb.Decisions) != 1 || fb.Decisions[0].Scope != feedback.ScopePattern || fb.Decisions[0].Reason != "generated" {
		t.Fatalf("want one pattern decision, got %+v", fb.Decisions)
	}
	if code, body := post("tok", `{"decisions":[{"key":"`+k1+`","scope":"issue","tags":["irrelevant"]}]}`); code != http.StatusOK || !strings.Contains(body, `"errors":[]`) {
		t.Fatalf("exact save: %d %s", code, body)
	}
	if fb, _ = feedback.Load(r.Dir); len(fb.Decisions) != 2 {
		t.Fatalf("want pattern and issue decisions, got %+v", fb.Decisions)
	}
	assertSuppressed(t, r, "after saving from the report")
}
