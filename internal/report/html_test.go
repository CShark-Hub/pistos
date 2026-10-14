package report

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"example.com/pistos/internal/finding"
)

func TestHighlightKeepsLinesAndEscapes(t *testing.T) {
	src := []string{"/* a", "<b> */ x := \"<s>\" // c", "func f() int { return 42 }"}
	got := highlightLines("x.go", src)
	if len(got) != 3 {
		t.Fatalf("lines: %d", len(got))
	}
	if string(got[0]) != `<span class="tk-comment">/* a</span>` || !strings.HasPrefix(string(got[1]), `<span class="tk-comment">&lt;b&gt; */</span>`) {
		t.Fatalf("block comment must be reopened per line: %q", got[:2])
	}
	for _, want := range []string{`<span class="tk-string">&#34;&lt;s&gt;&#34;</span>`, `<span class="tk-comment">// c</span>`} {
		if !strings.Contains(string(got[1]), want) {
			t.Fatalf("missing %s in %q", want, got[1])
		}
	}
	for _, want := range []string{`tk-keyword">func`, `tk-func">f`, `tk-type">int`, `tk-number">42`} {
		if !strings.Contains(string(got[2]), want) {
			t.Fatalf("missing %s in %q", want, got[2])
		}
	}
	if plain := highlightLines("x.unknown", []string{"<a>"}); string(plain[0]) != "&lt;a&gt;" {
		t.Fatalf("unknown languages are escaped only: %q", plain)
	}
}

func TestHTMLRendersExcerptsAndDegrades(t *testing.T) {
	var lines []string
	for i := 1; i <= 120; i++ {
		lines = append(lines, "line")
	}
	env := &finding.Envelope{
		Canonical: &finding.Report{Verdict: finding.Verdict{Outcome: "fail"}, Issues: []finding.Issue{
			{Finding: finding.Finding{Key: "k1-a", Project: "p", Family: "f/x", Severity: finding.SevError, Message: "<boom>", Location: finding.Location{Path: "a.go", Line: 10, EndLine: 100}}, Blocking: true},
			{Finding: finding.Finding{Key: "k1-b", Project: "p", Family: "f/x", Severity: finding.SevWarning, Message: "gone", Location: finding.Location{Path: "missing.go", Line: 3}}},
		}},
		Numbers: map[string]int{"k1-a": 1, "k1-b": 2},
	}
	var buf bytes.Buffer
	err := HTML(&buf, env, HTMLOptions{Source: func(p string) ([]byte, error) {
		if p == "a.go" {
			return []byte(strings.Join(lines, "\n")), nil
		}
		return nil, errors.New("no such file")
	}})
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"&lt;boom&gt;", "more line(s)", "source not available", `"kind_hits":2`, `"token": ""`} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q", want)
		}
	}
	if strings.Contains(out, "<boom>") {
		t.Fatal("messages must be escaped")
	}
	if n := strings.Count(out, `class="ln hit"`); n != maxHighlight {
		t.Fatalf("a long range keeps head and tail only: %d marked lines", n)
	}
}
