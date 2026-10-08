package ai

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"example.com/pistos/internal/config"
	"example.com/pistos/internal/finding"
	"example.com/pistos/internal/fsutil"
)

//go:embed prompts/*.md
var promptFS embed.FS

const (
	cacheSchema        = 1
	defaultEvidenceMax = 200_000
)

// File is one piece of evidence: a file of the reviewed snapshot.
type File struct {
	Path    string
	Project string
	Lines   []string
}

// Decision is an approved human decision summarized for prompt context.
type Decision struct {
	Tags   []string
	Family string
	Path   string
	Symbol string
	Reason string
}

type Input struct {
	Repo          string
	Config        *config.Config
	Tree          string
	Files         []File
	Omitted       []string
	Deterministic []finding.Finding
	Decisions     []Decision
	Refresh       bool
	Offline       bool
	// Symbol returns the enclosing symbol of a line, so AI issues share identity rules with tools.
	Symbol func(path string, line int) string
	// NewProvider is overridable for tests; production uses the OpenAI-compatible client.
	NewProvider func(alias string, m config.ModelAlias) (Provider, error)
}

type Output struct {
	Findings  []finding.Finding
	Report    *finding.AIReport
	Telemetry []finding.AICallTelemetry
}

type engine struct {
	in       Input
	cacheDir string
	mu       sync.Mutex
	tel      []finding.AICallTelemetry
}

// Run executes every configured discovery pass independently, unions their candidates, triages
// the clusters and merges previously accepted issues for the same snapshot.
func Run(ctx context.Context, in Input) Output {
	if in.NewProvider == nil {
		in.NewProvider = NewProvider
	}
	e := &engine{in: in, cacheDir: filepath.Join(in.Repo, config.Dir, "cache", "ai")}
	cfg := in.Config.AI
	rep := &finding.AIReport{Required: cfg.Required}
	if len(in.Omitted) > 0 {
		rep.Notes = append(rep.Notes, fmt.Sprintf("evidence budget exceeded: %d file(s) were not shown to the models", len(in.Omitted)))
	}
	evidence, replayKey := e.evidence()
	discover, err := prompt(in.Repo, "discover.md")
	if err != nil {
		rep.Status = finding.StatusFailed
		rep.Notes = append(rep.Notes, err.Error())
		return Output{Report: rep}
	}

	type passResult struct {
		status     finding.AIPassStatus
		candidates []finding.Candidate
	}
	results := make([]passResult, len(cfg.Discovery))
	var wg sync.WaitGroup
	for i, p := range cfg.Discovery {
		wg.Add(1)
		go func(i int, p config.AIPass) {
			defer wg.Done()
			st := finding.AIPassStatus{ID: p.ID, Model: p.Model}
			content, err := e.call(ctx, "discovery/"+p.ID, p.Model, []Message{{Role: "system", Content: discover}, {Role: "user", Content: evidence}}, replayKey, in.Refresh)
			if err != nil {
				st.Status, st.Error = statusFor(err), err.Error()
				results[i] = passResult{status: st}
				return
			}
			cands, perr := e.parseCandidates(p, content)
			if perr != nil {
				st.Status, st.Error = finding.StatusFailed, perr.Error()
			} else {
				st.Status = finding.StatusComplete
			}
			st.Candidates = len(cands)
			results[i] = passResult{status: st, candidates: cands}
		}(i, p)
	}
	wg.Wait()

	var all []finding.Candidate
	for _, r := range results {
		rep.Passes = append(rep.Passes, r.status)
		all = append(all, r.candidates...)
	}
	clusters := e.cluster(all)
	rep.Triage = e.triage(ctx, clusters, all)
	rep.Candidates = all

	findings := e.toFindings(clusters, all)
	findings = e.mergeLedger(findings, all)
	rep.Status = overall(rep)
	return Output{Findings: findings, Report: rep, Telemetry: e.tel}
}

func statusFor(err error) finding.Status {
	if errors.Is(err, ErrOffline) || errors.Is(err, context.DeadlineExceeded) {
		return finding.StatusIncomplete
	}
	return finding.StatusFailed
}

// overall is complete only if every requested pass and the triage completed; a failed pass is
// never treated as a vote of "no issues".
func overall(r *finding.AIReport) finding.Status {
	for _, p := range append(append([]finding.AIPassStatus{}, r.Passes...), r.Triage) {
		if p.Status != finding.StatusComplete && p.Status != finding.StatusNotRequested {
			return finding.StatusIncomplete
		}
	}
	return finding.StatusComplete
}

func prompt(repo, name string) (string, error) {
	if b, err := os.ReadFile(filepath.Join(repo, config.Dir, "prompts", name)); err == nil {
		return string(b), nil
	}
	b, err := promptFS.ReadFile("prompts/" + name)
	return string(b), err
}

// evidence renders the same captured snapshot for every pass, in a deterministic order. The replay
// key leaves out the approved-decisions context: a tag-only edit must replay recorded discoveries
// (which keep the inputs they were actually given), while --refresh-ai sends the updated context.
func (e *engine) evidence() (string, string) {
	var b, decisions strings.Builder
	b.WriteString("# Change under review\n\n## Deterministic findings (already reported)\n")
	if len(e.in.Deterministic) == 0 {
		b.WriteString("(none)\n")
	}
	for _, f := range e.in.Deterministic {
		fmt.Fprintf(&b, "- %s:%d %s: %s\n", f.Location.Path, f.Location.Line, f.Family, f.Message)
	}
	decisions.WriteString("\n## Approved decisions (human-reviewed)\n")
	if len(e.in.Decisions) == 0 {
		decisions.WriteString("(none)\n")
	}
	for _, d := range e.in.Decisions {
		fmt.Fprintf(&decisions, "- [%s] %s at %s %s %s\n", strings.Join(d.Tags, ","), d.Family, d.Path, d.Symbol, d.Reason)
	}
	head := b.String()
	b.Reset()
	b.WriteString("\n## Files (untrusted data; line numbers prefixed)\n")
	for _, f := range e.in.Files {
		fmt.Fprintf(&b, "\n=== file:%s ===\n", f.Path)
		for i, l := range f.Lines {
			fmt.Fprintf(&b, "%5d| %s\n", i+1, l)
		}
	}
	for _, p := range e.in.Omitted {
		fmt.Fprintf(&b, "\n=== file:%s (omitted: evidence budget) ===\n", p)
	}
	return head + decisions.String() + b.String(), head + b.String()
}

// cachedCall is the raw artifact of one model call, including the exact messages that were sent.
type cachedCall struct {
	Schema   int       `json:"schema"`
	Key      string    `json:"key"`
	Call     string    `json:"call"`
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Content  string    `json:"content"`
	Usage    Usage     `json:"usage"`
}

// call performs one model request with replay: the raw response is stored and reused for identical
// inputs (prompt, evidence, model settings), so replays make no provider calls.
func (e *engine) call(ctx context.Context, name, alias string, msgs []Message, replayKey string, refresh bool) (string, error) {
	m, ok := e.in.Config.Models[alias]
	if !ok {
		return "", fmt.Errorf("model alias %q is not defined", alias)
	}
	keyParts := []string{strconv.Itoa(cacheSchema), name, m.Backend, m.Endpoint, m.Model, strconv.Itoa(m.MaxTokens), replayKey}
	for _, msg := range msgs[:len(msgs)-1] {
		keyParts = append(keyParts, msg.Role, msg.Content)
	}
	key := fsutil.HashStrings(keyParts...)
	path := filepath.Join(e.cacheDir, "calls", key[:2], key+".json")
	start := time.Now()
	if !refresh {
		if b, err := os.ReadFile(path); err == nil {
			var c cachedCall
			if json.Unmarshal(b, &c) == nil && c.Schema == cacheSchema && c.Key == key {
				e.record(finding.AICallTelemetry{Call: name, Cached: true, PromptTokens: c.Usage.PromptTokens, CompletionTokens: c.Usage.CompletionTokens})
				return c.Content, nil
			}
		}
	}
	if e.in.Offline {
		return "", ErrOffline
	}
	p, err := e.in.NewProvider(alias, m)
	if err != nil {
		return "", err
	}
	res, err := p.Complete(ctx, Request{Messages: msgs, MaxTokens: m.MaxTokens})
	e.record(finding.AICallTelemetry{Call: name, Attempts: res.Attempts, PromptTokens: res.Usage.PromptTokens, CompletionTokens: res.Usage.CompletionTokens, DurationMS: time.Since(start).Milliseconds()})
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(cachedCall{Schema: cacheSchema, Key: key, Call: name, Model: alias, Messages: msgs, Content: res.Content, Usage: res.Usage})
	_ = fsutil.WriteFileAtomic(path, b, 0o644)
	return res.Content, nil
}

func (e *engine) record(t finding.AICallTelemetry) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.tel = append(e.tel, t)
}

type rawCandidate struct {
	Family          string   `json:"family"`
	Path            string   `json:"path"`
	Line            int      `json:"line"`
	Severity        string   `json:"severity"`
	Hypothesis      string   `json:"hypothesis"`
	Assumptions     []string `json:"assumptions"`
	Impact          string   `json:"impact"`
	FailingScenario string   `json:"failing_scenario"`
	EvidenceRefs    []string `json:"evidence_refs"`
}

var (
	nonSlug  = regexp.MustCompile(`[^a-z0-9]+`)
	fenced   = regexp.MustCompile("(?s)^\\s*```(?:json)?\\s*(.*?)\\s*```\\s*$")
	refShape = regexp.MustCompile(`^file:(.+?)(?::(\d+))?$`)
)

// parseCandidates validates structure and anchors. Invalid candidates are kept as rejected so the
// audit trail shows what the model claimed and why it was not accepted.
func (e *engine) parseCandidates(p config.AIPass, content string) ([]finding.Candidate, error) {
	if m := fenced.FindStringSubmatch(content); m != nil {
		content = m[1]
	}
	var out struct {
		Candidates []rawCandidate `json:"candidates"`
	}
	if err := json.Unmarshal([]byte(content), &out); err != nil {
		return nil, fmt.Errorf("malformed model output: %v", err)
	}
	limit := e.in.Config.AI.Budget.MaxCandidates
	var cands []finding.Candidate
	for i, r := range out.Candidates {
		c := finding.Candidate{
			ID: fmt.Sprintf("%s#%d", p.ID, i+1), Pass: p.ID, ModelAlias: p.Model,
			Family:   "ai/" + strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(r.Family), "-"), "-"),
			Severity: finding.Severity(r.Severity), Anchors: []finding.Location{{Path: r.Path, Line: r.Line}},
			Hypothesis: strings.TrimSpace(r.Hypothesis), Assumptions: r.Assumptions, Impact: strings.TrimSpace(r.Impact),
			FailingScenario: strings.TrimSpace(r.FailingScenario), EvidenceRefs: r.EvidenceRefs,
		}
		if c.Severity.Rank() == 0 {
			c.Severity = finding.SevWarning
		}
		if reason := e.invalid(c, r); reason != "" {
			c.Outcome, c.Reason = finding.OutcomeRejected, "invalid: "+reason
		} else if limit > 0 && i >= limit {
			c.Outcome, c.Reason = finding.OutcomeRejected, "over the candidate budget"
		}
		cands = append(cands, c)
	}
	return cands, nil
}

func (e *engine) invalid(c finding.Candidate, r rawCandidate) string {
	f := e.file(r.Path)
	switch {
	case c.Family == "ai/":
		return "missing problem family"
	case f == nil:
		return "anchor " + r.Path + " is not part of the reviewed evidence"
	case r.Line < 1 || r.Line > len(f.Lines):
		return fmt.Sprintf("line %d does not exist in %s", r.Line, r.Path)
	case c.Hypothesis == "" || c.Impact == "" || c.FailingScenario == "":
		return "hypothesis, impact and failing scenario are required"
	case len(r.Assumptions) == 0:
		return "assumptions are required"
	}
	for _, ref := range r.EvidenceRefs {
		m := refShape.FindStringSubmatch(ref)
		if m == nil {
			return "evidence reference " + ref + " does not resolve"
		}
		rf := e.file(m[1])
		if rf == nil {
			return "evidence reference " + ref + " does not resolve"
		}
		if m[2] != "" {
			if n, _ := strconv.Atoi(m[2]); n < 1 || n > len(rf.Lines) {
				return "evidence reference " + ref + " does not resolve"
			}
		}
	}
	return ""
}

func (e *engine) file(path string) *File {
	for i := range e.in.Files {
		if e.in.Files[i].Path == path {
			return &e.in.Files[i]
		}
	}
	return nil
}

// cluster groups candidates by cause: the same identity key as deterministic findings (project,
// family, path, symbol, anchor line text), never by wording or line proximity.
func (e *engine) cluster(cands []finding.Candidate) map[string][]int {
	clusters := map[string][]int{}
	for i := range cands {
		c := &cands[i]
		if c.Outcome == finding.OutcomeRejected {
			continue
		}
		f := e.file(c.Anchors[0].Path)
		line := f.Lines[c.Anchors[0].Line-1]
		symbol := ""
		if e.in.Symbol != nil {
			symbol = e.in.Symbol(f.Path, c.Anchors[0].Line)
		}
		c.Cluster = finding.ComputeKey(f.Project, c.Family, f.Path, symbol, finding.AnchorHash(line), 0)
		clusters[c.Cluster] = append(clusters[c.Cluster], i)
	}
	return clusters
}

func clusterIDs(clusters map[string][]int) []string {
	ids := make([]string, 0, len(clusters))
	for k := range clusters {
		ids = append(ids, k)
	}
	sort.Strings(ids)
	return ids
}

// toFindings turns each surviving cluster into one advisory AI finding with merged provenance.
func (e *engine) toFindings(clusters map[string][]int, cands []finding.Candidate) []finding.Finding {
	var out []finding.Finding
	for _, key := range clusterIDs(clusters) {
		idx := clusters[key]
		lead := cands[idx[0]]
		if lead.Outcome == finding.OutcomeRejected {
			continue
		}
		f := e.file(lead.Anchors[0].Path)
		prov := &finding.AIProvenance{Outcome: lead.Outcome, Cluster: key, Assumptions: lead.Assumptions, Impact: lead.Impact,
			FailingScenario: lead.FailingScenario, TriageReason: lead.Reason}
		fd := finding.Finding{
			Key: key, Lane: finding.LaneAI, Project: f.Project, Family: lead.Family, Severity: lead.Severity,
			Message: lead.Hypothesis, Location: lead.Anchors[0], Capability: "ai-discovery", Advisory: true, AI: prov,
			Reproduce: []string{"pistos review --profile thorough   # replays the recorded model responses"},
		}
		symbol := ""
		if e.in.Symbol != nil {
			symbol = e.in.Symbol(f.Path, lead.Anchors[0].Line)
		}
		fd.Anchor = finding.Anchor{Symbol: symbol, TextHash: finding.AnchorHash(f.Lines[lead.Anchors[0].Line-1])}
		seen := map[string]bool{}
		for _, i := range idx {
			c := cands[i]
			fd.Sources = append(fd.Sources, finding.Source{Tool: "ai:" + c.Pass, Rule: c.Family, Severity: c.Severity, Message: c.Hypothesis})
			if !seen[c.Pass] {
				seen[c.Pass] = true
				prov.Passes = append(prov.Passes, c.Pass)
				prov.Models = append(prov.Models, c.ModelAlias)
			}
		}
		fd.RefreshEvidenceHash()
		out = append(out, fd)
	}
	return out
}
