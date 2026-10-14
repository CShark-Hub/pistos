package report

import (
	_ "embed"
	"fmt"
	"html/template"
	"io"
	"sort"
	"strings"

	"example.com/pistos/internal/feedback"
	"example.com/pistos/internal/finding"
)

//go:embed html.tmpl
var htmlTemplate string

var htmlTmpl = template.Must(template.New("report").Funcs(template.FuncMap{
	"upper": strings.ToUpper,
	"short": short,
	"join":  strings.Join,
}).Parse(htmlTemplate))

// HTMLOptions controls the browsable report. Source reads a file as it was in the reviewed tree;
// without it the report has no code excerpts.
type HTMLOptions struct {
	Source func(path string) ([]byte, error)
	// Context is the number of lines shown around an issue's location (default 4).
	Context int
	// Token enables saving decisions back to a `pistos report` server; a static file instead shows
	// the equivalent `pistos tag` commands.
	Token   string
	Version string
}

// maxHighlight bounds the excerpt of an issue whose range covers a large block (e.g. a long function).
const maxHighlight = 40

type htmlLine struct {
	N    int
	Code template.HTML
	Hit  bool
	// Gap, when non-zero, marks a placeholder for that many lines left out of a long range.
	Gap int
}

type htmlIssue struct {
	finding.Issue
	Number  int
	Where   string
	Tools   []string
	Lines   []htmlLine
	NoCode  string
	Hidden  bool
	KindHit int
}

// htmlSelectable is what the page script needs to build decisions; everything else is rendered here.
type htmlSelectable struct {
	Key      string `json:"key"`
	Number   int    `json:"number"`
	Project  string `json:"project"`
	Family   string `json:"family"`
	Path     string `json:"path"`
	Decided  bool   `json:"decided"`
	KindHits int    `json:"kind_hits"`
}

type htmlData struct {
	Report     *finding.Report
	Env        *finding.Envelope
	Issues     []htmlIssue
	Selectable []htmlSelectable
	Counts     map[string]int
	Token      string
	Version    string
	Tags       []string
}

// HTML renders a self-contained page (no external assets, so it works offline and from file://).
func HTML(w io.Writer, env *finding.Envelope, o HTMLOptions) error {
	if o.Context <= 0 {
		o.Context = 4
	}
	r := env.Canonical
	files := map[string][]string{}
	fileErr := map[string]error{}
	lines := func(p string) ([]string, error) {
		if l, ok := files[p]; ok {
			return l, fileErr[p]
		}
		if o.Source == nil {
			fileErr[p] = fmt.Errorf("source not available")
			files[p] = nil
			return nil, fileErr[p]
		}
		b, err := o.Source(p)
		if err != nil {
			fileErr[p] = fmt.Errorf("source not available: %v", err)
		}
		l := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
		files[p] = l
		return l, fileErr[p]
	}

	kinds := map[string]int{}
	for _, is := range r.Issues {
		kinds[is.Project+"\x00"+is.Family]++
	}
	d := htmlData{
		Report: r, Env: env, Token: o.Token, Version: o.Version, Counts: map[string]int{},
		Tags: []string{feedback.TagIrrelevant, feedback.TagFalsePositive, feedback.TagAcceptedRisk},
	}
	for _, is := range r.Issues {
		h := htmlIssue{Issue: is, Number: env.Numbers[is.Key], Where: is.Location.Path, KindHit: kinds[is.Project+"\x00"+is.Family]}
		if is.Location.Line > 0 {
			h.Where = fmt.Sprintf("%s:%d", is.Location.Path, is.Location.Line)
		}
		for _, s := range is.Sources {
			h.Tools = append(h.Tools, s.Tool+":"+s.Rule)
		}
		sort.Strings(h.Tools)
		h.Hidden = is.Disposition.State == finding.DispSuppressed || is.Change == "existing"
		switch {
		case is.Disposition.State == finding.DispSuppressed:
			d.Counts["suppressed"]++
		case is.Change == "existing":
			d.Counts["existing"]++
		case is.Blocking:
			d.Counts["blocking"]++
		default:
			d.Counts["other"]++
		}
		if src, err := lines(is.Location.Path); err != nil {
			h.NoCode = err.Error()
		} else if is.Location.Line <= 0 || is.Location.Line > len(src) {
			h.NoCode = "the issue applies to the whole file"
		} else {
			h.Lines = excerpt(is.Location.Path, src, is.Location, o.Context)
		}
		d.Issues = append(d.Issues, h)
		d.Selectable = append(d.Selectable, htmlSelectable{
			Key: is.Key, Number: h.Number, Project: is.Project, Family: is.Family, Path: is.Location.Path,
			Decided: is.Disposition.Decision != "", KindHits: h.KindHit,
		})
	}
	return htmlTmpl.Execute(w, d)
}

// excerpt returns the highlighted lines around loc with the issue's range marked. Ranges longer
// than maxHighlight keep their head and tail around a placeholder for the lines left out.
func excerpt(p string, src []string, loc finding.Location, ctx int) []htmlLine {
	first, last := loc.Line, max(loc.Line, loc.EndLine)
	last = min(last, len(src))
	from, to := max(1, first-ctx), min(len(src), last+ctx)
	code := highlightLines(p, src[from-1:to])
	var out []htmlLine
	gap := -1
	for i, c := range code {
		n := from + i
		if last-first+1 > maxHighlight && n >= first+maxHighlight/2 && n <= last-maxHighlight/2 {
			if gap < 0 {
				gap = len(out)
				out = append(out, htmlLine{})
			}
			out[gap].Gap++
			continue
		}
		out = append(out, htmlLine{N: n, Code: c, Hit: n >= first && n <= last})
	}
	return out
}
